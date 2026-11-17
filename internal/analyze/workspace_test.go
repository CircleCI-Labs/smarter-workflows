package analyze

import (
	"testing"
)

func TestWorkspaceBinaryUnderAttachAt(t *testing.T) {
	root := t.TempDir()
	e := &Engine{RepoRoot: root, jobWorkspaceAt: []string{"/tmp/workspace"}}
	into := newInputSet()
	argv := []ArgWord{
		{Lit: "/tmp/workspace/api", Dynamic: false},
		{Lit: "-addr", Dynamic: false},
		{Lit: "127.0.0.1:18080", Dynamic: false},
	}
	if op := e.analyzeSimpleCommand(root, wdSet{"examples/demo": {}}, argv, nil, into, new(bool), 0, nil, nil, "", nil); op != "" {
		t.Fatalf("opaque %q", op)
	}
}

func TestWorkspaceBinaryOutsideAtOpaque(t *testing.T) {
	e := &Engine{RepoRoot: t.TempDir(), jobWorkspaceAt: []string{"/tmp/workspace"}}
	into := newInputSet()
	argv := []ArgWord{{Lit: "/opt/bin/tool", Dynamic: false}}
	if op := e.analyzeSimpleCommand(e.RepoRoot, wdSet{".": {}}, argv, nil, into, new(bool), 0, nil, nil, "", nil); op == "" {
		t.Fatal("expected opaque absolute binary outside workspace")
	}
}
