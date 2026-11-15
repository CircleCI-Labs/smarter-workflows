package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidOutputPath(t *testing.T) {
	if err := validOutputPath("out/continue.yml"); err != nil {
		t.Fatalf("relative path: %v", err)
	}
	if err := validOutputPath("\x00bad"); err == nil || !strings.Contains(err.Error(), "null") {
		t.Fatalf("nul: %v", err)
	}
	if err := validOutputPath("  "); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("blank: %v", err)
	}
}

func TestWriteOutputFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "out.yml")
	if err := writeOutputFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestPlanRejectsNulInOutputPaths(t *testing.T) {
	c := newCLIRepo(t)
	c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
	c.Write("a.txt", "changed\n")
	c.Commit("change a")
	bad := c.path("continue.yml") + "\x00"
	args := c.planArgs("--out", bad)
	code, _, stderr := doPlan(t, args...)
	if code != 1 || !strings.Contains(stderr, "null byte") {
		t.Fatalf("= %d, %q; want a fatal error about the output path", code, stderr)
	}
}

func TestPlanContinuationConfigIsPrivate(t *testing.T) {
	c := newCLIRepo(t)
	c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
	c.Write("a.txt", "changed\n")
	c.Commit("change a")
	if code, _, stderr := doPlan(t, c.planArgs()...); code != 0 {
		t.Fatalf("plan = %d, %q", code, stderr)
	}
	info, err := os.Stat(c.path("continue.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("continue.yml mode = %o, want 0600", info.Mode().Perm())
	}
}
