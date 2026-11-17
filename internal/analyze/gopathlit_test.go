package analyze

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoPathLiteralRules(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "docs", "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package b

import (
	"os"
	"path/filepath"
)

var allJobs = []string{"docs"}

func touches() {
	_, _ = os.ReadFile("docs/x.md")
	_ = filepath.Join("..", "..", "docs")
}
`
	goFile := filepath.Join(pkg, "sample.go")
	if err := os.WriteFile(goFile, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	into := newInputSet()
	(&Engine{}).scanGoFilePathLiterals(root, root, goFile, into)
	if _, ok := into.files["docs/x.md"]; !ok {
		t.Fatalf("want docs/x.md input, files=%v dirs=%v", into.files, into.dirs)
	}
	if _, ok := into.dirs["docs"]; !ok {
		t.Fatalf("want docs/ from filepath.Join, files=%v dirs=%v", into.files, into.dirs)
	}
	if len(into.files) != 1 || len(into.dirs) != 1 {
		t.Fatalf("bare \"docs\" in a slice must not add inputs; files=%v dirs=%v", into.files, into.dirs)
	}
}

func TestGoDotDotPathResolution(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"policies", "x", filepath.Join("a", "b")} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pkg := filepath.Join(root, "a", "b")
	src := `package b

import "path/filepath"

const bundle = "../../policies"

func touches() {
	_, _ = filepath.Abs("../../x")
	_ = bundle
	_ = "../../../../etc"
}
`
	goFile := filepath.Join(pkg, "sample.go")
	if err := os.WriteFile(goFile, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	into := newInputSet()
	(&Engine{}).scanGoFilePathLiterals(root, root, goFile, into)
	if _, ok := into.dirs["policies"]; !ok {
		t.Fatalf("want policies/ from ../../policies const, dirs=%v", into.dirs)
	}
	if _, ok := into.dirs["x"]; !ok {
		t.Fatalf("want x/ from filepath.Abs, dirs=%v", into.dirs)
	}
	for d := range into.dirs {
		if d == "etc" {
			t.Fatalf("must not escape repo; dirs=%v", into.dirs)
		}
	}
}

func TestLiteralLooksLikePath(t *testing.T) {
	tests := map[string]bool{
		"docs":            false,
		"docs/x.md":       true,
		"go.mod":          true,
		"README.md":       true,
		"testdata/x.json": true,
	}
	for s, want := range tests {
		if got := literalLooksLikePath(s); got != want {
			t.Errorf("literalLooksLikePath(%q) = %v, want %v", s, got, want)
		}
	}
}
