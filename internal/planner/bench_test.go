package planner_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner/benchgen"
)

type benchRepo struct {
	changes []diff.Change
	dirs    map[string]bool
}

func (b *benchRepo) Changes(ctx context.Context, base, head string) ([]diff.Change, error) {
	return b.changes, nil
}

func (b *benchRepo) DirsExist(ctx context.Context, rev string, dirs []string) (map[string]bool, error) {
	out := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		out[d] = b.dirs[d]
	}
	return out, nil
}

func benchPlannerInput(shape benchgen.Shape, jobs, mapFiles, diffN int) (planner.Input, planner.Options) {
	cfg := benchgen.ConfigYAML(shape, jobs)
	names := benchgen.JobNames(shape, jobs)
	m := benchgen.ImpactMap(names, mapFiles)
	changes := benchgen.DiffChanges(diffN)
	dirs := map[string]bool{".": true, "pkg": true, "changed": true}
	for _, name := range names {
		dirs["pkg/"+name] = true
	}
	o := planner.DefaultOptions()
	o.Mode = planner.ModeSelect
	o.Workflow = "ci"
	o.Branch = "feature"
	o.Base, o.Head = "base1", "head1"
	o.Version = "bench"
	o.BinPath = "/usr/local/bin/circleci-smarter"
	return planner.Input{
		Config:        cfg,
		Map:           m,
		Repo:          &benchRepo{changes: changes, dirs: dirs},
		CommitMessage: "bench",
	}, o
}

func BenchmarkPlan(b *testing.B) {
	for _, shape := range benchgen.AllShapes {
		for _, jobs := range benchgen.JobCounts {
			for _, mapFiles := range benchgen.MapFileCounts {
				for _, diffN := range benchgen.DiffCounts {
					label := benchgen.BenchLabel(shape, jobs, mapFiles, diffN)
					b.Run(label, func(b *testing.B) {
						in, o := benchPlannerInput(shape, jobs, mapFiles, diffN)
						b.ReportAllocs()
						b.ResetTimer()
						for b.Loop() {
							if _, err := planner.Run(context.Background(), in, o); err != nil {
								b.Fatal(err)
							}
						}
					})
				}
			}
		}
	}
}

// BenchmarkPlanLatency2000 records per-iteration wall times for p95 reporting.
func BenchmarkPlanLatency2000(b *testing.B) {
	const jobs = 2000
	const mapFiles = 10000
	const diffN = 50
	shape := benchgen.ShapeFanout
	in, o := benchPlannerInput(shape, jobs, mapFiles, diffN)
	times := make([]float64, 0, b.N)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		if _, err := planner.Run(context.Background(), in, o); err != nil {
			b.Fatal(err)
		}
		times = append(times, time.Since(start).Seconds())
	}
	sort.Float64s(times)
	p95 := times[int(float64(len(times))*0.95+0.5)-1]
	b.ReportMetric(p95, "p95_sec")
	b.ReportMetric(times[len(times)-1], "max_sec")
}

// ImpactMapParse measures JSON decode of a large map (used while loading maps).
func BenchmarkImpactMapParse(b *testing.B) {
	for _, jobs := range []int{100, 500, 2000} {
		for _, mapFiles := range benchgen.MapFileCounts {
			names := benchgen.JobNames(benchgen.ShapeFanout, jobs)
			data := benchgen.ImpactMapJSON(names, mapFiles)
			b.Run(fmt.Sprintf("jobs%d/files%d", jobs, mapFiles), func(b *testing.B) {
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				for b.Loop() {
					if _, err := impactmap.Parse(data); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
