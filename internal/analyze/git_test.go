package analyze

import "testing"

func TestGitMetadataCommands(t *testing.T) {
	root := t.TempDir()
	e := &Engine{RepoRoot: root}
	cases := []string{
		`git rev-parse --show-toplevel`,
		`git -C scripts describe --tags`,
		`git config --get remote.origin.url`,
		`git remote get-url origin`,
	}
	for _, script := range cases {
		t.Run(script, func(t *testing.T) {
			into := newInputSet()
			if op := e.walkShellScript(root, ".", script, 0, into, nil, nil); op != "" {
				t.Fatalf("opaque %q", op)
			}
		})
	}
}

func TestGitLsFilesAddsPathspec(t *testing.T) {
	root := t.TempDir()
	e := &Engine{RepoRoot: root}
	into := newInputSet()
	if op := e.walkShellScript(root, ".", `git ls-files docs`, 0, into, nil, nil); op != "" {
		t.Fatalf("opaque %q", op)
	}
}

func TestGitUnknownSubcommandOpaque(t *testing.T) {
	e := &Engine{RepoRoot: t.TempDir()}
	if op := e.walkShellScript(e.RepoRoot, ".", `git unknown-subcommand plush`, 0, newInputSet(), nil, nil); op == "" {
		t.Fatal("expected opaque")
	}
}
