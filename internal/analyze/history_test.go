package analyze

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyHistoryAddsDirs(t *testing.T) {
	dir := t.TempDir()
	hist := filepath.Join(dir, "h.json")
	_ = os.MkdirAll(filepath.Join(dir, "ci"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "ci", "foo.sh"), []byte("#!/bin/sh\n"), 0o644)
	os.WriteFile(hist, []byte(`[{
  "commit": "abc",
  "base": "def",
  "branch": "main",
  "changed": [{"path": "ci/foo.sh", "status": "M"}],
  "jobs": [{"name": "lint-markdown", "status": "failed", "duration_s": 1}]
}]`), 0o644)
	jobs := map[string]*InputSet{
		"lint-markdown": func() *InputSet {
			s := newInputSet()
			s.addDir("tmp/mdl")
			return s
		}(),
	}
	corr, err := applyHistory(dir, hist, jobs)
	if err != nil {
		t.Fatal(err)
	}
	if len(corr) != 1 {
		t.Fatalf("corrections %d", len(corr))
	}
	if _, ok := jobs["lint-markdown"].dirs["ci"]; !ok {
		t.Fatalf("dirs %v", jobs["lint-markdown"].sortedDirs())
	}
	if _, ok := jobs["lint-markdown"].files["ci/foo.sh"]; !ok {
		t.Fatalf("files %v", jobs["lint-markdown"].sortedFiles())
	}
}
