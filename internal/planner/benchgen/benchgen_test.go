package benchgen_test

import (
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner/benchgen"
)

func TestGeneratedConfigsParse(t *testing.T) {
	for _, shape := range benchgen.AllShapes {
		for _, n := range benchgen.JobCounts {
			src := benchgen.ConfigYAML(shape, n)
			if _, err := config.Parse(src); err != nil {
				t.Fatalf("%s jobs=%d: %v", shape, n, err)
			}
		}
	}
}

func TestGeneratedImpactMapsParse(t *testing.T) {
	for _, files := range benchgen.MapFileCounts {
		jobs := 100
		if files == 10000 {
			jobs = 10 // unique paths stay under MaxUniqueFiles
		}
		names := benchgen.JobNames(benchgen.ShapeFanout, jobs)
		v1 := benchgen.ImpactMapV1JSON(names, files)
		if len(v1) <= impactmap.MaxMapFileBytes {
			if _, err := impactmap.Parse(v1); err != nil {
				t.Fatalf("v1 files=%d: %v", files, err)
			}
		}
		if _, err := impactmap.Parse(benchgen.ImpactMapV2Gzip(names, files)); err != nil {
			t.Fatalf("v2 files=%d: %v", files, err)
		}
	}
}
