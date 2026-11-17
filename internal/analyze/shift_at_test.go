package analyze

import "testing"

func TestAssertShiftAtCommand(t *testing.T) {
	root := t.TempDir()
	body := `assert() {
  assert_name=$1
  shift
  if "$@"; then echo ok; fi
}
assert "name" test -n "x"
`
	op := (&Engine{RepoRoot: root}).walkShellScript(root, ".", body, 0, newInputSet(), nil, nil)
	if op != "" {
		t.Fatalf("opaque %q", op)
	}
}
