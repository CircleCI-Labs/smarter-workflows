package analyze

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPythonPathLiteralRules(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "docs", "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `
names = ["docs"]
with open("docs/x.md") as f:
    f.read()
joined = __import__("os.path").join("a", "b", "docs")
`
	into := newInputSet()
	(&Engine{}).scanPythonSource(root, "pkg", src, into)
	if _, ok := into.files["docs/x.md"]; !ok {
		t.Fatalf("want docs/x.md from open, files=%v dirs=%v", into.files, into.dirs)
	}
	if len(into.files) != 1 {
		t.Fatalf(`bare "docs" in a list must not add inputs; files=%v dirs=%v`, into.files, into.dirs)
	}
}
