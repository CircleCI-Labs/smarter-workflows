package impactmap

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const traceHeader = "# smarter-trace v1"

// MaxPathsPerTrace bounds how many F and D lines one trace may carry.
const MaxPathsPerTrace = MaxPathsPerJob

// Trace is one job node's trace TSV (spec, "File formats").
type Trace struct {
	Job       string
	Node      int
	Method    string
	Supported bool
	Reason    string
	Files     []string
	Dirs      []string
}

// ParseTrace reads one trace TSV. The first line must be the format header.
// Lines are "key<TAB>value"; unknown keys are ignored so the collector can grow.
// A missing "supported" line means unsupported, which is the safe reading.
func ParseTrace(r io.Reader) (*Trace, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)

	t := &Trace{Method: MethodATime}
	seen := map[string]bool{}
	lineNo := 0
	sawHeader := false
	for sc.Scan() {
		lineNo++
		line := strings.TrimRight(sc.Text(), "\r")
		if !sawHeader {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if strings.TrimSpace(line) != traceHeader {
				return nil, fmt.Errorf("line %d: want header %q", lineNo, traceHeader)
			}
			sawHeader = true
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("line %d: want key and value separated by a tab", lineNo)
		}
		switch key {
		case "job", "node", "method", "supported", "reason":
			if seen[key] {
				return nil, fmt.Errorf("line %d: duplicate %q", lineNo, key)
			}
			seen[key] = true
		}
		switch key {
		case "job":
			t.Job = val
		case "node":
			n, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("line %d: bad node %q", lineNo, val)
			}
			t.Node = n
		case "method":
			t.Method = strings.TrimSpace(val)
		case "supported":
			switch strings.TrimSpace(val) {
			case "true":
				t.Supported = true
			case "false":
				t.Supported = false
			default:
				return nil, fmt.Errorf("line %d: supported must be true or false, got %q", lineNo, val)
			}
		case "reason":
			t.Reason = val
		case "F":
			if len(t.Files)+len(t.Dirs) >= MaxPathsPerTrace {
				return nil, fmt.Errorf("line %d: trace has more than %d paths", lineNo, MaxPathsPerTrace)
			}
			p, err := CleanPath(val, false)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
			t.Files = append(t.Files, p)
		case "D":
			if len(t.Files)+len(t.Dirs) >= MaxPathsPerTrace {
				return nil, fmt.Errorf("line %d: trace has more than %d paths", lineNo, MaxPathsPerTrace)
			}
			p, err := CleanPath(val, true)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
			t.Dirs = append(t.Dirs, p)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !sawHeader {
		return nil, errors.New("empty trace: missing header")
	}
	if t.Job == "" {
		return nil, errors.New("trace has no job line")
	}
	return t, nil
}

// ReadTraceDir parses every *.tsv file in dir, in file name order.
func ReadTraceDir(dir string) ([]*Trace, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.tsv"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	traces := make([]*Trace, 0, len(names))
	for _, name := range names {
		if info, err := os.Stat(name); err != nil || !info.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		t, perr := ParseTrace(f)
		f.Close()
		if perr != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(name), perr)
		}
		traces = append(traces, t)
	}
	return traces, nil
}

// Merge combines traces into one map. For each job, Files and Dirs are the
// union over its nodes, sorted, and Complete is true only if every node was
// supported by the atime method. Nodes counts the distinct node indexes.
func Merge(traces []*Trace, revision string, now time.Time) (*Map, error) {
	if len(traces) == 0 {
		return nil, errors.New("no traces to merge")
	}
	type acc struct {
		files, dirs map[string]struct{}
		nodes       map[int]struct{}
		complete    bool
	}
	jobs := map[string]*acc{}
	methods := map[string]struct{}{}
	for _, t := range traces {
		a := jobs[t.Job]
		if a == nil {
			a = &acc{files: map[string]struct{}{}, dirs: map[string]struct{}{}, nodes: map[int]struct{}{}, complete: true}
			jobs[t.Job] = a
		}
		a.nodes[t.Node] = struct{}{}
		if !t.Supported || t.Method != MethodATime {
			a.complete = false
		}
		methods[t.Method] = struct{}{}
		for _, f := range t.Files {
			a.files[f] = struct{}{}
		}
		for _, d := range t.Dirs {
			a.dirs[d] = struct{}{}
		}
	}
	m := &Map{
		Version:   Version,
		Revision:  revision,
		CreatedAt: now.UTC().Format(time.RFC3339),
		Method:    MethodATime,
		Jobs:      make(map[string]*Job, len(jobs)),
	}
	if len(methods) > 1 {
		m.Method = "mixed"
	} else {
		for method := range methods {
			m.Method = method
		}
	}
	for name, a := range jobs {
		m.Jobs[name] = &Job{
			Files:    sortedKeys(a.files),
			Dirs:     sortedKeys(a.dirs),
			Complete: a.complete,
			Nodes:    len(a.nodes),
		}
	}
	return m, nil
}
