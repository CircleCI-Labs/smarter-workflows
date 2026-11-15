package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPlanCommandParametersFile(t *testing.T) {
	c := newCLIRepo(t)
	c.Git("checkout", "-q", "main")
	params := filepath.Join(t.TempDir(), "params.json")
	if err := os.WriteFile(params, []byte(`{"run-ci": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	reportDir := filepath.Join(t.TempDir(), "plan")
	code, _, stderr := doPlan(t, c.planArgs("--branch", "feature", "--parameters", params, "--report-dir", reportDir)...)
	if code != 0 {
		t.Fatalf("plan = %d, %q", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(reportDir, "parameters.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["run-ci"] != true {
		t.Fatalf("parameters = %#v", got)
	}
}
