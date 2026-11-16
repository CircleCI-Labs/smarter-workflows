package analyze_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/analyze"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

func TestGoTestJobCoversPackagesAndNormalizedDirs(t *testing.T) {
	root := repoRoot(t)
	cfg := filepath.Join(root, ".circleci", "continue.yml")
	if _, err := os.Stat(cfg); err != nil {
		t.Skip("dogfood config missing")
	}
	m, _, _, err := analyze.Run(context.Background(), analyze.RunOptions{
		RepoRoot:   root,
		ConfigPath: cfg,
		Workflows:  []string{"ci"},
		AIMode:     analyze.AIModeOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	j := m.Jobs["go-test"]
	if j == nil {
		t.Fatal("no go-test job")
	}
	for _, d := range j.DirPaths() {
		if d != impactmap.NormalizeDir(d) {
			t.Fatalf("go-test dir not normalized: %q", d)
		}
		if strings.HasPrefix(d, "./") {
			t.Fatalf("go-test dir still uses ./ prefix: %q", d)
		}
		if impactmap.NormalizeDir(d) == "." {
			t.Fatalf("go-test should not list repository root from go test ./...; dirs=%v", j.DirPaths())
		}
	}
	if !j.CoversPath("internal/diff/git.go") {
		t.Fatal("go-test should cover internal/diff/git.go via go packages")
	}
	if j.CoversPath("docs/plan-regression-only.md") {
		t.Fatal("go-test should not cover arbitrary docs paths outside its package closure")
	}
	jl := m.Jobs["go-lint"]
	if jl == nil || !jl.ListsDir(".") {
		t.Fatal("go-lint should list repository root from gofmt -l .")
	}
	if !jl.CoversPath("docs/learn.md") {
		t.Fatal("go-lint root input should cover docs-only paths")
	}
	jd := m.Jobs["docs-site-build"]
	if jd == nil || !jd.CoversPath("docs/learn.md") {
		t.Fatal("docs-site-build should cover nav-listed docs")
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && strings.Contains(string(b), "smarter-workflows") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("module root not found")
		}
		dir = parent
	}
}
