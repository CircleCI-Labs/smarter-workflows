package analyze

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestSelfScriptTestAndDocsComplete(t *testing.T) {
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
	for _, name := range []string{"script-test", "docs-site-build"} {
		j := m.Jobs[name]
		if j == nil || !j.Complete {
			why := jobReason(rep, name)
			t.Fatalf("%s complete=%v why=%q", name, j != nil && j.Complete, why)
		}
	}
}

func TestDemoE2EComplete(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	m, rep, _, err := Run(context.Background(), RunOptions{
		RepoRoot:   root,
		ConfigPath: filepath.Join(root, "examples", "demo", ".circleci", "continue.yml"),
		Workflows:  []string{"demo"},
		AIMode:     AIModeOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	j := m.Jobs["e2e"]
	if j == nil || !j.Complete {
		t.Fatalf("e2e complete=%v why=%q", j != nil && j.Complete, jobReason(rep, "e2e"))
	}
}

func TestListIncompleteSelfAndDemo(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic")
	}
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	for _, tc := range []struct{ cfg, wf string }{
		{filepath.Join(root, ".circleci", "continue.yml"), "ci"},
		{filepath.Join(root, "examples", "demo", ".circleci", "continue.yml"), "demo"},
	} {
		_, rep, _, err := Run(context.Background(), RunOptions{RepoRoot: root, ConfigPath: tc.cfg, Workflows: []string{tc.wf}, AIMode: AIModeOff})
		if err != nil {
			t.Fatal(err)
		}
		for _, j := range rep.Jobs {
			if !j.Complete {
				fmt.Printf("%s %s: %s\n", tc.wf, j.Name, j.IncompleteWhy)
			}
		}
	}
}
