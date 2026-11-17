package analyze

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShellEnvRootFromDirname(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "scripts/test-release-dry-run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	into := newInputSet()
	op := (&Engine{}).walkShellScriptFile(root, ".", "scripts/test-release-dry-run.sh", string(body), 1, into, nil, nil, nil, nil)
	if op != "" {
		t.Fatalf("opaque %q", op)
	}
	if !into.hasInputs() {
		t.Fatal("expected inputs from release.sh")
	}
}
