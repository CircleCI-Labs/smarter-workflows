package analyze_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/analyze"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

func TestInferGoJob(t *testing.T) {
	root := filepath.Join("testdata", "repo")
	cfg := filepath.Join("testdata", "minimal.yml")
	m, _, err := analyze.Build(analyze.Options{
		RepoRoot:   root,
		ConfigPath: cfg,
		Workflow:   "ci",
	})
	if err != nil {
		t.Fatal(err)
	}
	j := m.Jobs["unit"]
	if j == nil || !j.Complete {
		t.Fatalf("unit job: %+v", j)
	}
	if !j.ListsDir("internal/sample") && !j.ReadsFile("go.mod") {
		t.Fatalf("expected go inputs, got files=%v dirs=%v", j.FilePaths(), j.DirPaths())
	}
}

func TestEvaluateRecall(t *testing.T) {
	truth := &impactmap.Map{
		Version: impactmap.Version,
		Jobs: map[string]*impactmap.Job{
			"a": {Files: []string{"x/a.go", "x/b.go"}, Dirs: []string{"x"}, Complete: true, Nodes: 1},
		},
	}
	inf := &impactmap.Map{
		Version: impactmap.Version,
		Jobs: map[string]*impactmap.Job{
			"a": {Dirs: []string{"x"}, Complete: true, Nodes: 1},
		},
	}
	rep := analyze.Evaluate(inf, truth, nil)
	if rep.Jobs[0].Recall != 1 {
		t.Fatalf("recall %v", rep.Jobs[0].Recall)
	}
}

func TestMain(m *testing.M) {
	_ = os.MkdirAll(filepath.Join("testdata", "repo", "internal", "sample"), 0o755)
	_ = os.WriteFile(filepath.Join("testdata", "repo", "go.mod"), []byte("module example.com/t\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join("testdata", "repo", "internal", "sample", "sample.go"), []byte("package sample\n"), 0o644)
	os.Exit(m.Run())
}
