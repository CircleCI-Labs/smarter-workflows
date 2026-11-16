package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCLI_BacktestStrictOnWarning(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "backtest", "testdata", "param-gated")
	params := filepath.Join(t.TempDir(), "params.json")
	if err := os.WriteFile(params, []byte(`{"run-ci":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--history", filepath.Join(dir, "history.json"),
		"--map", filepath.Join(dir, "map.json"),
		"--config", filepath.Join(dir, "config.yml"),
		"--workflow", "ci",
		"--parameters", params,
		"--format", "json",
		"--strict",
	}
	var outBuf, errBuf bytes.Buffer
	code := runBacktest(args, &outBuf, &errBuf)
	if code != 1 {
		t.Fatalf("exit %d stderr=%q", code, errBuf.String())
	}
}

func TestCLI_BacktestGolden(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "backtest", "testdata")
	args := []string{
		"--history", filepath.Join(dir, "history.json"),
		"--map", filepath.Join(dir, "map.json"),
		"--config", filepath.Join(dir, "monorepo.yml"),
		"--workflow", "ci",
		"--mandatory", "security-scan",
		"--format", "json",
	}
	var outBuf, errBuf bytes.Buffer
	code := runBacktest(args, &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("exit %d stderr=%q", code, errBuf.String())
	}
	want, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var gotV, wantV any
	if err := json.Unmarshal(outBuf.Bytes(), &gotV); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantV); err != nil {
		t.Fatal(err)
	}
	ga, _ := json.Marshal(gotV)
	wa, _ := json.Marshal(wantV)
	if string(ga) != string(wa) {
		t.Errorf("CLI report mismatch:\ngot:\n%s\nwant:\n%s", outBuf.Bytes(), want)
	}
}
