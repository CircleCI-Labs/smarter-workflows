package analyze

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBareRelativeScriptStepFollows(t *testing.T) {
	root := t.TempDir()
	scriptDir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\necho ok\n"
	if err := os.WriteFile(filepath.Join(scriptDir, "step.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	into := newInputSet()
	proof := &JobProof{}
	if op := (&Engine{}).analyzeRunCommand(root, ".", "scripts/step.sh", into, proof); op != "" {
		t.Fatalf("opaque %q", op)
	}
	found := false
	for p, notes := range into.notes {
		if p != "scripts/step.sh" {
			continue
		}
		for _, n := range notes {
			if n.Signal == SignalScript {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("want followed_script on scripts/step.sh, notes=%v", into.notes)
	}
}

func TestOrbValidateIncludesOrbSrcArg(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	m, rep, _, err := Run(context.Background(), RunOptions{
		RepoRoot:   root,
		ConfigPath: filepath.Join(root, ".circleci", "continue.yml"),
		Workflows:  []string{"ci"},
		AIMode:     AIModeOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	j := m.Jobs["orb-validate"]
	if j == nil || !j.Complete {
		t.Fatalf("orb-validate %+v why=%q", j, jobReason(rep, "orb-validate"))
	}
	if !j.ListsDir("orb/src") && !j.CoversPath("orb/src/@orb.yml") {
		t.Fatalf("orb-validate missing orb/src; dirs=%v", j.DirPaths())
	}
}

func TestScriptUnboundPositionalMakesOpaque(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `#!/bin/sh
cat "$1"
`
	if err := os.WriteFile(filepath.Join(dir, "need-arg.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	into := newInputSet()
	proof := &JobProof{}
	if op := (&Engine{}).analyzeRunCommand(root, ".", "scripts/need-arg.sh", into, proof); op == "" {
		t.Fatal("expected opaque step when $1 is unbound")
	}
}

func TestReleaseDryRunFollowsGoClosure(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".circleci", "continue.yml")); err != nil {
		t.Skip(err)
	}
	m, rep, _, err := Run(context.Background(), RunOptions{
		RepoRoot:   root,
		ConfigPath: filepath.Join(root, ".circleci", "continue.yml"),
		Workflows:  []string{"ci"},
		AIMode:     AIModeOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	j := m.Jobs["release-dry-run"]
	if j == nil || !j.Complete {
		t.Fatalf("release-dry-run complete=%v reason=%v", j != nil && j.Complete, jobReason(rep, "release-dry-run"))
	}
	if !j.CoversPath("cmd/circleci-smarter") {
		t.Fatalf("release-dry-run should cover go build of cmd/circleci-smarter; dirs=%v", j.DirPaths())
	}
	if !j.ReadsFile("go.mod") || !j.ReadsFile("go.sum") {
		t.Fatal("release-dry-run should list go.mod and go.sum")
	}
	ov := m.Jobs["orb-validate"]
	if ov == nil || !ov.Complete {
		t.Fatalf("orb-validate complete=%v", ov != nil && ov.Complete)
	}
	if !ov.ReadsFile("scripts/test-orb-integration.sh") || !ov.ReadsFile("scripts/validate-orb.sh") {
		t.Fatal("orb-validate should follow validation scripts")
	}
}

func jobReason(rep *Report, name string) string {
	if rep == nil {
		return ""
	}
	for _, j := range rep.Jobs {
		if j.Name == name {
			return j.IncompleteWhy
		}
	}
	return ""
}
