package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseBenchFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "scripts", "bench", "testdata")

	t.Run("legacy pipeline", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join(dir, "pipeline.json"))
		if err != nil {
			t.Fatal(err)
		}
		if got := commitFromPipeline(raw); got != "abc123deadbeef" {
			t.Fatalf("commit = %q", got)
		}
		if got := branchFromPipeline(raw); got != "feature/docs" {
			t.Fatalf("branch = %q", got)
		}
		var jobs jobPage
		if err := json.Unmarshal(mustRead(t, filepath.Join(dir, "workflow_jobs.json")), &jobs); err != nil {
			t.Fatal(err)
		}
		got := jobsFromSingleWorkflow(jobs, "")
		want := mustRead(t, filepath.Join(dir, "jobs_expected.json"))
		assertJSONEqual(t, got, want)
	})

	t.Run("rerun flaky", func(t *testing.T) {
		var bundles []workflowBundle
		if err := json.Unmarshal(mustRead(t, filepath.Join(dir, "workflow_rerun_bundle.json")), &bundles); err != nil {
			t.Fatal(err)
		}
		got := mergeWorkflowReruns(bundles)
		want := mustRead(t, filepath.Join(dir, "jobs_expected_rerun.json"))
		assertJSONEqual(t, got, want)
	})

	t.Run("github app pipeline", func(t *testing.T) {
		raw := mustRead(t, filepath.Join(dir, "pipeline_github_app.json"))
		if got := commitFromPipeline(raw); got != "b66ed439ad596cc4d801ed050a0589b732796f88" {
			t.Fatalf("commit = %q", got)
		}
		if got := branchFromPipeline(raw); got != "main" {
			t.Fatalf("branch = %q", got)
		}
	})
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertJSONEqual(t *testing.T, got any, wantJSON []byte) {
	t.Helper()
	gb, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	if err := json.Unmarshal(gb, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantJSON, &w); err != nil {
		t.Fatal(err)
	}
	ga, _ := json.Marshal(g)
	wa, _ := json.Marshal(w)
	if string(ga) != string(wa) {
		t.Fatalf("got %s want %s", ga, wa)
	}
}
