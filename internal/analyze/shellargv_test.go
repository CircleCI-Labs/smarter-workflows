package analyze

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellDynamicArgvCases(t *testing.T) {
	root := t.TempDir()
	e := &Engine{RepoRoot: root}
	// go module for build closure
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/t\n\ngo 1.22\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "cmd", "x"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "cmd", "x", "main.go"), []byte("package main\nfunc main(){}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "x"), []byte("x"), 0o644)

	cases := []struct {
		name    string
		script  string
		opaque  bool
		contain string
	}{
		{name: "binTest", script: `"$BIN" test -f x`, opaque: true, contain: "command name is not literal"},
		{name: "hugoVersion", script: `"$HOME/.local/bin/hugo" version`, opaque: false},
		{name: "goTestDyn", script: `go test "$PKG"`, opaque: true, contain: "dynamic argument"},
		{name: "goBuildLdflags", script: `go build -ldflags "-X main.v=$SHA" ./cmd/x`, opaque: false},
		{name: "tarTmp", script: `tar -xzf "/tmp/${archive}" -C "$HOME/bin"`, opaque: false},
		{name: "catDyn", script: `cat "$F"`, opaque: true, contain: "dynamic argument"},
		{name: "echoDyn", script: `echo "$X"`, opaque: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			into := newInputSet()
			op := e.walkShellScript(root, ".", tc.script, 0, into, nil, nil)
			if tc.opaque {
				if op == "" {
					t.Fatalf("expected opaque, got complete with inputs files=%v", into.sortedFiles())
				}
				if tc.contain != "" && !strings.Contains(strings.ToLower(op), strings.ToLower(tc.contain)) {
					t.Fatalf("opaque %q want substring %q", op, tc.contain)
				}
				return
			}
			if op != "" {
				t.Fatalf("unexpected opaque %q", op)
			}
		})
	}
}
