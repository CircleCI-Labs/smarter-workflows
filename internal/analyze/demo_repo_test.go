package analyze

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDemoAnalyzePathsAndWebTest(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "examples/demo/.circleci/continue.yml")
	m, _, _, err := Run(t.Context(), RunOptions{
		RepoRoot:   root,
		ConfigPath: cfgPath,
		Workflows:  []string{"demo"},
		AIMode:     AIModeOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, j := range m.Jobs {
		for _, f := range j.FilePaths() {
			if strings.Contains(f, "..") {
				t.Fatalf("%s: path traversal %q", name, f)
			}
			if !fileExists(root, f) {
				t.Fatalf("%s: file %q does not exist", name, f)
			}
		}
		for _, d := range j.DirPaths() {
			if strings.Contains(d, "..") {
				t.Fatalf("%s: path traversal %q", name, d)
			}
			if d == "." {
				continue
			}
			if !dirExists(root, d) {
				t.Fatalf("%s: dir %q does not exist", name, d)
			}
		}
	}
	web := m.Jobs["web-test"]
	if web == nil {
		t.Fatal("missing web-test")
	}
	hasWeb := false
	for _, d := range web.DirPaths() {
		if strings.HasPrefix(d, "examples/demo/services/web") {
			hasWeb = true
		}
	}
	if !hasWeb {
		t.Fatalf("web-test dirs %v", web.DirPaths())
	}
}

func TestSelfRepoAnalyzePaths(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	m, rep, _, err := Run(t.Context(), RunOptions{
		RepoRoot:   root,
		ConfigPath: filepath.Join(root, ".circleci/continue.yml"),
		AIMode:     AIModeOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, j := range m.Jobs {
		for _, f := range j.FilePaths() {
			if !fileExists(root, f) {
				t.Fatalf("%s: missing file %q", name, f)
			}
		}
		for _, d := range j.DirPaths() {
			if d != "." && !dirExists(root, d) {
				t.Fatalf("%s: missing dir %q", name, d)
			}
		}
	}
	for _, jr := range rep.Jobs {
		if !jr.Complete && jr.IncompleteWhy == "" {
			t.Fatalf("%s incomplete without reason", jr.Name)
		}
		if !jr.Complete && !strings.Contains(jr.IncompleteWhy, "not understood") {
			t.Logf("incomplete %s: %s", jr.Name, jr.IncompleteWhy)
		}
	}
}
