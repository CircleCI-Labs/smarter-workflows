package learn

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
)

func TestReportRecallNAWithNoFailingJobs(t *testing.T) {
	rep := BuildReport(BuildInput{
		Workflows: []string{"ci"},
		Commits:   3,
		After: &backtest.Report{
			Commits: 3,
			Summary: backtest.Summary{
				SavedDurationS: 10,
				SavingsPercent: 50,
			},
		},
	})
	if rep.Summary.Recall != nil {
		t.Fatalf("recall %+v", rep.Summary.Recall)
	}
	if rep.Summary.FailingJobs != 0 {
		t.Fatalf("failing_jobs %d", rep.Summary.FailingJobs)
	}
	md := rep.Markdown()
	if !strings.Contains(md, "n/a (no failing jobs in the replayed history)") {
		t.Fatalf("markdown:\n%s", md)
	}
	data, err := rep.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	sum := raw["summary"].(map[string]any)
	if sum["recall"] != nil {
		t.Fatalf("json recall %v", sum["recall"])
	}
	if int(sum["failing_jobs"].(float64)) != 0 {
		t.Fatalf("failing_jobs %v", sum["failing_jobs"])
	}
}

func TestReportRecallWithFailures(t *testing.T) {
	rep := BuildReport(BuildInput{
		After: &backtest.Report{
			Commits: 2,
			Summary: backtest.Summary{
				CaughtFailures: 2,
				Misses:         1,
				Recall:         2.0 / 3.0,
				SavedDurationS: 5,
			},
		},
	})
	if rep.Summary.Recall == nil || *rep.Summary.Recall != 2.0/3.0 {
		t.Fatalf("recall %+v", rep.Summary.Recall)
	}
	if rep.Summary.FailingJobs != 3 {
		t.Fatalf("failing_jobs %d", rep.Summary.FailingJobs)
	}
	md := rep.Markdown()
	if !strings.Contains(md, "**66.7%** (3 failing jobs)") {
		t.Fatalf("markdown:\n%s", md)
	}
}

func TestReportCorrectionsListAndJSON(t *testing.T) {
	var many []PathCorrection
	for i := 0; i < 25; i++ {
		many = append(many, PathCorrection{
			Job: "go-test", Path: "pkg/x.go", Commit: "abc1234567890", Pipeline: "abc1234567890",
		})
	}
	many = append(many, PathCorrection{
		Job: "lint", Path: "docs/a.md", Commit: "deadbeef", Pipeline: "deadbeef",
	})
	rep := BuildReport(BuildInput{
		Corrections: many,
		After:       &backtest.Report{Commits: 1, Summary: backtest.Summary{CaughtFailures: 1, Recall: 1}},
	})
	md := rep.Markdown()
	if !strings.Contains(md, "| Corrections added (go-test) | 25 |") {
		t.Fatalf("summary table missing job total:\n%s", md)
	}
	if !strings.Contains(md, "and 5 more") {
		t.Fatalf("expected truncation:\n%s", md)
	}
	if !strings.Contains(md, "commit `abc1234`") {
		t.Fatalf("expected short sha:\n%s", md)
	}
	data, err := rep.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var got Report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.CorrectionsAdded) != 26 {
		t.Fatalf("corrections %d", len(got.CorrectionsAdded))
	}
	if got.CorrectionsAdded[0].Job == "" || got.CorrectionsAdded[0].Path == "" {
		t.Fatalf("%+v", got.CorrectionsAdded[0])
	}
}
