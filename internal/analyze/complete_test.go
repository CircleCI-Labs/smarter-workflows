package analyze

import (
	"path/filepath"
	"testing"
)

func TestStaticProofRejectsGarbagePaths(t *testing.T) {
	in := newInputSet()
	in.addVerifiedDir(".", ".", "...", InputNote{})
	in.addVerifiedDir(".", ".", "0.0.0.0", InputNote{})
	if len(in.sortedDirs()) != 0 {
		t.Fatalf("unexpected dirs %v", in.sortedDirs())
	}
	proof := &JobProof{IncompleteReason: "step 1 not understood: make"}
	if proof.StaticComplete() {
		t.Fatal("expected incomplete proof")
	}
}

func TestWebTestPackageDir(t *testing.T) {
	root := filepath.Clean(filepath.Join(testingDir(t), "..", ".."))
	m, _, _, err := Run(t.Context(), RunOptions{
		RepoRoot:   root,
		ConfigPath: filepath.Join(root, "examples/demo/.circleci/continue.yml"),
		Workflows:  []string{"demo"},
		AIMode:     AIModeOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	wt := m.Jobs["web-test"]
	if wt == nil || !wt.Complete {
		t.Fatalf("web-test %+v", wt)
	}
	found := false
	for _, d := range wt.DirPaths() {
		if d == "examples/demo/services/web" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dirs %v", wt.DirPaths())
	}
}
