package backtest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
)

func TestWarningWorkflowDisabled(t *testing.T) {
	dir := filepath.Join("testdata", "param-gated")
	cfg, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	opts := backtest.DefaultOptions()
	opts.Workflow = "ci"
	opts.Parameters = map[string]any{"run-ci": false}

	warns, err := backtest.CheckWarnings(context.Background(), cfg, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 {
		t.Fatalf("warns = %#v", warns)
	}
	if !strings.Contains(warns[0].Message, "does not run under these parameters") {
		t.Fatalf("message = %q", warns[0].Message)
	}
}

func TestWarningManyUnmatchedJobs(t *testing.T) {
	cfg := []byte(`version: 2.1
jobs:
  lint:
    docker: [{image: "cimg/base:2026.11"}]
    steps: [checkout]
workflows:
  ci:
    jobs: [lint]
`)
	history := []backtest.CommitHistory{
		{
			Commit: "a", Base: "b", Branch: "main",
			Jobs: []backtest.RecordedJob{
				{Name: "old-a", Status: "success", DurationS: 10},
				{Name: "old-b", Status: "success", DurationS: 10},
				{Name: "lint", Status: "success", DurationS: 10},
			},
		},
	}
	opts := backtest.DefaultOptions()
	warns, err := backtest.CheckWarnings(context.Background(), cfg, history, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 {
		t.Fatalf("warns = %#v", warns)
	}
	if !strings.Contains(warns[0].Message, "more than half") {
		t.Fatalf("message = %q", warns[0].Message)
	}
}
