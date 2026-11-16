package monorepo_test

import (
	"os"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

// baseTree is the repository at the commit the impact map was built from. It
// holds every file the map names, and a few that no job reads.
func baseTree() map[string]string {
	return map[string]string{
		"Makefile":                       "lint:\n\t@echo lint\nbuild:\n\t@echo build\n",
		"README.md":                      "# Example monorepo\n",
		"go.mod":                         "module example.test/monorepo\n\ngo 1.26\n",
		"go.sum":                         "",
		"mkdocs.yml":                     "site_name: Example\n",
		"package.json":                   "{\"name\": \"monorepo\", \"private\": true}\n",
		"package-lock.json":              "{\"lockfileVersion\": 3}\n",
		"CHANGELOG.md":                   "# Changelog\n",
		"docs/index.md":                  "# Welcome\n",
		"docs/guide.md":                  "# Guide\n",
		"e2e/smoke.test.ts":              "test('smoke', () => {});\n",
		"libs/shared/schema.json":        "{\"version\": 1}\n",
		"libs/shared/errors.json":        "{\"codes\": []}\n",
		"scripts/deploy.sh":              "#!/bin/sh\necho deploy\n",
		"services/api/main.go":           "package api\n\nfunc main() {}\n",
		"services/api/handler.go":        "package api\n\n// Handle answers requests.\nfunc Handle() {}\n",
		"services/api/handler_test.go":   "package api\n",
		"services/web/package.json":      "{\"name\": \"web\"}\n",
		"services/web/src/index.ts":      "export const hello = 'hi';\n",
		"services/web/src/index.test.ts": "test('hello', () => {});\n",
	}
}

// TestImpactMapMatchesTheExample keeps the three example artifacts in step:
// the map, the config and the repository tree the scenarios run on.
func TestImpactMapMatchesTheExample(t *testing.T) {
	raw, err := os.ReadFile("impact-map.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	m, err := impactmap.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	// The file is in the canonical form `map build` writes.
	canon, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	again, err := impactmap.Parse(canon)
	if err != nil {
		t.Fatal(err)
	}
	if m.Revision != again.Revision || len(m.Jobs) != len(again.Jobs) {
		t.Fatalf("round-trip header mismatch: got %+v", again)
	}
	for name, job := range m.Jobs {
		aj := again.Jobs[name]
		if aj == nil || job.Complete != aj.Complete || job.Nodes != aj.Nodes {
			t.Errorf("job %s mismatch after round-trip", name)
			continue
		}
		if !reflect.DeepEqual(job.FilePaths(), aj.FilePaths()) || !reflect.DeepEqual(job.DirPaths(), aj.DirPaths()) {
			t.Errorf("job %s paths mismatch after round-trip", name)
		}
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(m.Revision) || m.Method == "" {
		t.Errorf("revision = %q, method = %q", m.Revision, m.Method)
	}
	if len(m.Problems) != 0 {
		t.Errorf("problems = %v", m.Problems)
	}

	tree := baseTree()
	dirs := map[string]bool{".": true}
	for file := range tree {
		for d := path.Dir(file); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	for name, job := range m.Jobs {
		if !job.Complete || job.Nodes != 1 {
			t.Errorf("%s: complete = %v, nodes = %d", name, job.Complete, job.Nodes)
		}
		for _, f := range job.FilePaths() {
			if _, ok := tree[f]; !ok {
				t.Errorf("%s reads %s, which is not in the base tree", name, f)
			}
		}
		for _, d := range job.DirPaths() {
			if !dirs[d] {
				t.Errorf("%s lists %s, which is not a directory of the base tree", name, d)
			}
		}
		if !slices.IsSorted(job.FilePaths()) || !slices.IsSorted(job.DirPaths()) {
			t.Errorf("%s: lists are not sorted", name)
		}
	}

	// Every job that ran in the baseline full run has a map entry; the deploy
	// chain is absent from this fixture map, so it has none.
	cfg, err := config.Parse([]byte(exampleConfig()))
	if err != nil {
		t.Fatal(err)
	}
	w, err := cfg.Workflow("ci")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range w.Entries {
		names = append(names, e.Name)
		_, inMap := m.Jobs[e.Name]
		switch e.Name {
		case "hold-deploy", "deploy":
			if inMap {
				t.Errorf("%s is not in the fixture map, so the map must have no entry for it", e.Name)
			}
		default:
			if !inMap {
				t.Errorf("job %s has no entry in the map", e.Name)
			}
		}
	}
	if !slices.Equal(names, allJobs) {
		t.Errorf("the workflow has %v, the tests expect %v", names, allJobs)
	}
	for name := range m.Jobs {
		if !slices.Contains(names, name) {
			t.Errorf("the map has an entry for %s, which is not a job of the workflow", name)
		}
	}

	// The example config has the pieces the scenarios exercise.
	text := exampleConfig()
	for _, want := range []string{"persist_to_workspace:", "attach_workspace:", "type: approval", "context: deploy"} {
		if !strings.Contains(text, want) {
			t.Errorf("continue.yml lacks %q", want)
		}
	}
}
