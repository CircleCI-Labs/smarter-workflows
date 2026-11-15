package monorepo_test

import (
	"os"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/catalog"
	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

// The catalog of the example, with its impact map, is a golden file: it is
// what `circleci-smarter catalog --config continue.yml --map impact-map.json.gz`
// prints, and the sample of the catalog format that the spec points to.
func TestCatalogOfTheExample(t *testing.T) {
	cfg, err := config.Parse([]byte(exampleConfig()))
	if err != nil {
		t.Fatal(err)
	}
	m, err := impactmap.Load("impact-map.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Build(cfg, "ci", m)
	if err != nil {
		t.Fatal(err)
	}
	out, err := cat.JSON()
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "catalog.json", out)

	// Every job of the workflow is in it, and the entries that need an
	// approval are where the example puts them.
	if got := len(cat.Jobs); got != len(allJobs) {
		t.Errorf("the catalog has %d jobs, the workflow %d", got, len(allJobs))
	}
	for _, j := range cat.Jobs {
		if j.Name == "hold-deploy" && j.Kind != "approval" {
			t.Errorf("hold-deploy is %s", j.Kind)
		}
	}
	if _, err := os.Stat("catalog.json"); err != nil {
		t.Error(err)
	}
}
