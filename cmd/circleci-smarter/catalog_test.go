package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/catalog"
)

const catalogConfig = `version: 2.1
jobs:
  build:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - persist_to_workspace: {root: ., paths: [dist]}
  test:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - attach_workspace: {at: .}
      - run: make test
  gate:
    type: no-op
workflows:
  ci:
    jobs:
      - build
      - test:
          requires: [build]
      - gate:
          requires: [test]
  nightly:
    jobs:
      - build
`

func doCatalog(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return run(t, func(a []string, o, e *bytes.Buffer) int { return runCatalog(a, o, e) }, args...)
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCatalogCommandPrintsTheWorkflowAsJSON(t *testing.T) {
	cfg := writeTemp(t, "continue.yml", catalogConfig)
	code, stdout, stderr := doCatalog(t, "--config", cfg, "--workflow", "ci")
	if code != 0 || stderr != "" {
		t.Fatalf("catalog = %d, %q", code, stderr)
	}
	var cat catalog.Catalog
	if err := json.Unmarshal([]byte(stdout), &cat); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if cat.Version != 1 || cat.Workflow != "ci" || len(cat.Jobs) != 3 || cat.MapRevision != "" {
		t.Fatalf("catalog = %+v", cat)
	}
	for i, want := range []struct{ name, kind string }{{"build", "build"}, {"gate", "no-op"}, {"test", "build"}} {
		if j := cat.Jobs[i]; j.Name != want.name || j.Kind != want.kind {
			t.Errorf("job %d = %s (%s), want %s (%s)", i, j.Name, j.Kind, want.name, want.kind)
		}
	}
	if again := func() string { _, out, _ := doCatalog(t, "--config", cfg, "--workflow", "ci"); return out }(); again != stdout {
		t.Error("two runs print different catalogs")
	}
}

func TestCatalogCommandAddsInputsFromTheMap(t *testing.T) {
	cfg := writeTemp(t, "continue.yml", catalogConfig)
	c := &cliRepo{dir: t.TempDir(), base: "abc123"}
	c.writeMap(t, map[string][]string{"test": {"services/api/main.go", "go.mod"}})

	code, stdout, stderr := doCatalog(t, "--config", cfg, "--workflow", "ci", "--map", c.path("map.json"))
	if code != 0 || stderr != "" {
		t.Fatalf("catalog = %d, %q", code, stderr)
	}
	var cat catalog.Catalog
	if err := json.Unmarshal([]byte(stdout), &cat); err != nil {
		t.Fatal(err)
	}
	if cat.MapRevision != "abc123" {
		t.Errorf("map_revision = %q", cat.MapRevision)
	}
	for _, j := range cat.Jobs {
		if want := j.Name == "test"; j.Inputs.Mapped != want {
			t.Errorf("%s mapped = %v, want %v", j.Name, j.Inputs.Mapped, want)
		}
		if j.Name == "test" && (j.Inputs.Files != 2 || strings.Join(j.Inputs.TopDirs, ",") != ".,services/api") {
			t.Errorf("test inputs = %+v", j.Inputs)
		}
	}
}

func TestCatalogCommandExitCodes(t *testing.T) {
	cfg := writeTemp(t, "continue.yml", catalogConfig)
	tests := []struct {
		name       string
		args       []string
		code       int
		stderrHas  string
		stdoutJSON bool
	}{
		{"two workflows and none named", []string{"--config", cfg}, 2, "2 workflows (ci, nightly)", false},
		{"an unknown workflow", []string{"--config", cfg, "--workflow", "nope"}, 2, `no workflow "nope"`, false},
		{"a stray argument", []string{"--config", cfg, "--workflow", "ci", "extra"}, 2, `unexpected argument "extra"`, false},
		{"an unknown flag", []string{"--nope"}, 2, "", false},
		{"no config", []string{"--config", filepath.Join(t.TempDir(), "missing.yml")}, 1, "cannot read the config", false},
		{"a config that is not YAML", []string{"--config", writeTemp(t, "bad.yml", "a: [")}, 1, "parse config", false},
		{"a map that is not JSON", []string{"--config", cfg, "--workflow", "ci", "--map", writeTemp(t, "map.json", "{")}, 1, "map.json", false},
		{"a map that is missing, which only warns", []string{"--config", cfg, "--workflow", "ci", "--map", filepath.Join(t.TempDir(), "none.json")}, 0, "there is no impact map at", true},
		{"help", []string{"-h"}, 0, "usage: circleci-smarter catalog", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := doCatalog(t, tc.args...)
			if code != tc.code {
				t.Fatalf("exit = %d, want %d (stderr %q)", code, tc.code, stderr)
			}
			if !strings.Contains(stderr, tc.stderrHas) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tc.stderrHas)
			}
			if tc.stdoutJSON != json.Valid([]byte(stdout)) {
				t.Errorf("stdout = %q, want JSON = %v", stdout, tc.stdoutJSON)
			}
		})
	}
}
