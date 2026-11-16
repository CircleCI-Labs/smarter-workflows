// Package benchgen builds synthetic CircleCI configs, impact maps and diffs
// for planner performance benchmarks.
package benchgen

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

// Shape is the workflow topology to generate.
type Shape string

const (
	ShapeFanout  Shape = "fanout"
	ShapeChain   Shape = "chain"
	ShapeDiamond Shape = "diamond"
)

// AllShapes lists every supported topology.
var AllShapes = []Shape{ShapeFanout, ShapeChain, ShapeDiamond}

// JobCounts are the workflow sizes benchmarks use.
var JobCounts = []int{10, 100, 500, 2000}

// MapFileCounts are impact-map sizes (files per job).
var MapFileCounts = []int{100, 10000}

// DiffCounts are how many changed paths a diff carries.
var DiffCounts = []int{1, 50, 1000}

// ConfigYAML returns a version 2.1 config with n workflow entries in shape,
// with matrix entries and approvals sprinkled through the job list.
func ConfigYAML(shape Shape, n int) []byte {
	if n < 1 {
		n = 1
	}
	var b strings.Builder
	b.WriteString("version: 2.1\njobs:\n")
	names := workflowNames(shape, n)
	approvals := approvalNames(n)
	for _, name := range names {
		writeJobDef(&b, name)
	}
	for _, ap := range approvals {
		writeJobDef(&b, ap)
	}
	b.WriteString("workflows:\n  ci:\n    jobs:\n")
	for i, name := range names {
		writeWorkflowEntry(&b, shape, n, names, i, name)
	}
	return []byte(b.String())
}

func approvalNames(n int) []string {
	var out []string
	for i := 1; i < n; i++ {
		if i%11 == 0 {
			out = append(out, fmt.Sprintf("approve-%d", i/11))
		}
	}
	return out
}

func writeJobDef(b *strings.Builder, name string) {
	if strings.HasPrefix(name, "approve-") {
		fmt.Fprintf(b, "  %s:\n    type: approval\n", name)
		return
	}
	fmt.Fprintf(b, "  %s:\n    docker:\n      - image: cimg/base:2026.11\n    steps:\n      - checkout\n      - run: make %s\n", name, name)
}

func writeWorkflowEntry(b *strings.Builder, shape Shape, n int, names []string, i int, name string) {
	req := requires(shape, n, names, i)
	if i > 0 && i%11 == 0 {
		ap := fmt.Sprintf("approve-%d", i/11)
		fmt.Fprintf(b, "      - %s\n", ap)
		if len(req) > 0 {
			req = append([]string{ap}, req...)
		} else {
			req = []string{ap}
		}
	}
	if i > 0 && i%7 == 0 {
		fmt.Fprintf(b, "      - %s:\n          matrix:\n            parameters:\n              v: [a, b]\n          name: %s-<< matrix.v >>\n", name, name)
		if len(req) > 0 {
			fmt.Fprintf(b, "          requires:\n")
			for _, r := range req {
				fmt.Fprintf(b, "            - %s\n", r)
			}
		}
		return
	}
	if len(req) == 0 {
		fmt.Fprintf(b, "      - %s\n", name)
		return
	}
	fmt.Fprintf(b, "      - %s:\n          requires:\n", name)
	for _, r := range req {
		fmt.Fprintf(b, "            - %s\n", r)
	}
}

func workflowNames(shape Shape, n int) []string {
	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		names = append(names, fmt.Sprintf("job-%d", i))
	}
	return names
}

func requires(shape Shape, n int, names []string, i int) []string {
	if i == 0 {
		return nil
	}
	switch shape {
	case ShapeChain:
		return []string{names[i-1]}
	case ShapeFanout:
		return []string{names[0]}
	case ShapeDiamond:
		if n <= 4 {
			switch i {
			case 1, 2:
				return []string{names[0]}
			default:
				return []string{names[1], names[2]}
			}
		}
		// root, two wings of equal width, apex requiring both wings.
		wing := (n - 2) / 2
		if i <= wing {
			return []string{names[0]}
		}
		if i < n-1 {
			return []string{names[0]}
		}
		// apex: last job requires everyone in the wings (not the root).
		req := make([]string, 0, wing*2)
		for j := 1; j < n-1; j++ {
			req = append(req, names[j])
		}
		return req
	default:
		return []string{names[i-1]}
	}
}

// JobNames returns the workflow job names for a config of size n.
func JobNames(shape Shape, n int) []string {
	return workflowNames(shape, n)
}

// ImpactMap builds a map where every listed job has filesPerJob tracked paths.
func ImpactMap(jobs []string, filesPerJob int) *impactmap.Map {
	m := &impactmap.Map{
		Version:   impactmap.Version,
		Revision:  "bench-base",
		CreatedAt: "2026-11-15T12:00:00Z",
		Method:    impactmap.DefaultMapMethod,
		Jobs:      make(map[string]*impactmap.Job, len(jobs)),
	}
	for _, name := range jobs {
		files := make([]string, filesPerJob)
		for i := range files {
			files[i] = fmt.Sprintf("pkg/%s/file-%05d.go", name, i)
		}
		m.Jobs[name] = &impactmap.Job{
			Files:    files,
			Dirs:     []string{".", "pkg", "pkg/" + name},
			Complete: true,
			Nodes:    1,
		}
	}
	return m
}

// ImpactMapV1JSON is a v1 map for benchmarks that measure legacy JSON decode.
func ImpactMapV1JSON(jobs []string, filesPerJob int) []byte {
	data, err := ImpactMap(jobs, filesPerJob).MarshalV1()
	if err != nil {
		panic(err)
	}
	return data
}

// ImpactMapV2Gzip is a v2 on-disk map for benchmarks that measure production decode.
func ImpactMapV2Gzip(jobs []string, filesPerJob int) []byte {
	data, err := ImpactMap(jobs, filesPerJob).Marshal()
	if err != nil {
		panic(err)
	}
	return data
}

// ImpactMapJSON is ImpactMapV1JSON (BenchmarkImpactMapParse measures v1 JSON).
func ImpactMapJSON(jobs []string, filesPerJob int) []byte {
	return ImpactMapV1JSON(jobs, filesPerJob)
}

// DiffChanges returns n modified-file changes with distinct paths.
func DiffChanges(n int) []diff.Change {
	if n < 1 {
		n = 1
	}
	out := make([]diff.Change, n)
	for i := range out {
		out[i] = diff.Change{Path: fmt.Sprintf("changed/path-%05d.txt", i), Status: "M"}
	}
	return out
}

// BenchLabel returns a stable fragment for benchmark names.
func BenchLabel(shape Shape, jobs, mapFiles, diffN int) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s/jobs%d/map%d/diff%d", shape, jobs, mapFiles, diffN)
	return buf.String()
}

// ParseInt is a helper for shell drivers.
func ParseInt(s string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(s))
}
