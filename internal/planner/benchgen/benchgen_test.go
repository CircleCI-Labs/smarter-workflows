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
	names := benchgen.JobNames(benchgen.ShapeFanout, 100)
	for _, files := range benchgen.MapFileCounts {
		data := benchgen.ImpactMapJSON(names, files)
		if _, err := impactmap.Parse(data); err != nil {
			t.Fatalf("files=%d: %v", files, err)
		}
	}
}
