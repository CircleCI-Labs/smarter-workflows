package analyze

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceRelativeToScriptDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "scripts", "bench")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(dir, "libparse.sh")
	if err := os.WriteFile(lib, []byte("echo ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `dir=$(cd "$(dirname "$0")" && pwd)
. "$dir/libparse.sh"
`
	into := newInputSet()
	op := (&Engine{}).walkShellScriptFile(root, ".", "scripts/bench/run.sh", body, 1, into, nil, nil, nil, nil)
	if op != "" {
		t.Fatalf("opaque %q", op)
	}
}
