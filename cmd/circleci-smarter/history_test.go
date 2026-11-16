package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCLI_HistoryFetchMissingToken(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.json")
	args := []string{
		"--project-slug", "circleci/example/example",
		"--workflow", "ci",
		"--limit", "1",
		"--repo", dir,
		"--out", out,
	}
	var errBuf bytes.Buffer
	t.Setenv("CIRCLE_TOKEN", "")
	t.Setenv("CIRCLECI_TOKEN", "")
	code := runHistoryFetch(args, &bytes.Buffer{}, &errBuf)
	if code != 2 {
		t.Fatalf("exit = %d stderr=%q", code, errBuf.String())
	}
}

func TestCLI_HistoryFetchHelp(t *testing.T) {
	code := runHistory([]string{"fetch", "-h"}, os.Stdout, os.Stderr)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
}
