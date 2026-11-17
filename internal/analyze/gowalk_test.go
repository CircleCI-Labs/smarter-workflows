package analyze

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGoWalkSupersetOfGoList(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	walked, err := WalkGoPackages(root, "./...")
	if err != nil {
		t.Fatal(err)
	}
	walkSet := toSet(walked)
	ctx, cancel := context.WithTimeout(context.Background(), goListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-test", "-json", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	listSet := map[string]struct{}{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var item struct {
			Dir      string `json:"Dir"`
			Standard bool   `json:"Standard"`
		}
		if err := dec.Decode(&item); err != nil {
			break
		}
		if item.Standard || item.Dir == "" {
			continue
		}
		rel, err := filepath.Rel(root, item.Dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		listSet[filepath.ToSlash(rel)] = struct{}{}
	}
	for d := range listSet {
		if _, ok := walkSet[d]; !ok {
			t.Fatalf("walker missing dir %q from go list", d)
		}
	}
}

func toSet(list []string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, s := range list {
		m[s] = struct{}{}
	}
	return m
}

const goListTimeout = 30 * time.Second

func TestGoWalkTestdata(t *testing.T) {
	root := filepath.Join("testdata", "repo")
	dirs, err := WalkGoPackages(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) < 2 {
		t.Fatalf("dirs %v", dirs)
	}
}
