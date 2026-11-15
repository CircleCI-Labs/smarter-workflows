package backtest_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

func TestBacktestParamGatedFixture(t *testing.T) {
	dir := filepath.Join("testdata", "param-gated")
	cfg, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	mapData, err := os.ReadFile(filepath.Join(dir, "map.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := impactmap.Parse(mapData)
	if err != nil {
		t.Fatal(err)
	}
	hf, err := os.Open(filepath.Join(dir, "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()
	history, err := backtest.DecodeHistory(hf)
	if err != nil {
		t.Fatal(err)
	}
	paramsData, err := os.ReadFile(filepath.Join(dir, "parameters.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fileParams map[string]any
	if err := json.Unmarshal(paramsData, &fileParams); err != nil {
		t.Fatal(err)
	}

	opts := backtest.DefaultOptions()
	opts.Parameters = fileParams
	opts.Mandatory = []string{"security-scan"}
	report, err := backtest.Run(context.Background(), cfg, m, history, opts)
	if err != nil {
		t.Fatal(err)
	}

	got, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var gotV, wantV any
	if err := json.Unmarshal(got, &gotV); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantV); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(gotV, wantV) {
		t.Errorf("report mismatch:\ngot:\n%s\nwant from testdata/param-gated/report.json", got)
	}
}

func TestBacktestFixture(t *testing.T) {
	dir := filepath.Join("testdata")
	cfg, err := os.ReadFile(filepath.Join(dir, "monorepo.yml"))
	if err != nil {
		t.Fatal(err)
	}
	mapData, err := os.ReadFile(filepath.Join(dir, "map.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := impactmap.Parse(mapData)
	if err != nil {
		t.Fatal(err)
	}
	hf, err := os.Open(filepath.Join(dir, "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()
	history, err := backtest.DecodeHistory(hf)
	if err != nil {
		t.Fatal(err)
	}

	opts := backtest.DefaultOptions()
	opts.Mandatory = []string{"security-scan"}
	report, err := backtest.Run(context.Background(), cfg, m, history, opts)
	if err != nil {
		t.Fatal(err)
	}

	got, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var gotV, wantV any
	if err := json.Unmarshal(got, &gotV); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantV); err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(gotV, wantV) {
		t.Errorf("report mismatch:\ngot:\n%s\nwant from testdata/report.json", got)
	}
	if report.Commits != len(history) {
		t.Errorf("Commits = %d, want %d (len history)", report.Commits, len(history))
	}
}

func jsonEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}
