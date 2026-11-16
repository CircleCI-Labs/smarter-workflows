package planner_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

// monorepo is the config most tests plan: lint first, two services, a build
// that persists a workspace and an e2e job that attaches it, docs, a scan, and
// a deploy behind an approval that only runs on main. A second workflow must
// pass through untouched.
const monorepo = `version: 2.1
jobs:
  lint:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - run: make lint
  api-test:
    docker: [{image: "cimg/go:1.26"}]
    steps:
      - checkout
      - run: make api-test
  web-test:
    docker: [{image: "cimg/node:22.1"}]
    steps:
      - checkout
      - run: make web-test
  build:
    docker: [{image: "cimg/go:1.26"}]
    steps:
      - checkout
      - run: make build
      - persist_to_workspace:
          root: .
          paths: [dist]
  e2e:
    docker: [{image: "cimg/node:22.1"}]
    steps:
      - checkout
      - attach_workspace:
          at: .
      - run: make e2e
  docs:
    docker: [{image: "cimg/node:22.1"}]
    steps:
      - checkout
      - run: make docs
  security-scan:
    docker: [{image: "cimg/go:1.26"}]
    steps:
      - checkout
      - run: govulncheck ./...
  deploy:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - attach_workspace:
          at: .
      - run: ./scripts/deploy.sh
workflows:
  # the pipeline under test
  ci:
    jobs:
      - lint
      - security-scan
      - api-test:
          requires: [lint]
      - web-test:
          requires: [lint]
      - build:
          requires: [lint]
      - e2e:
          requires: [build]
      - docs
      - hold:
          type: approval
          requires: [e2e, api-test, web-test]
          filters:
            branches:
              only: main
      - deploy:
          requires: [hold]
          context: deploy
          filters:
            branches:
              only: main
  # untouched by the planner
  nightly:
    when: false
    jobs:
      - lint
`

// monorepoMap is the impact map of a traced full run of monorepo.
func monorepoMap() *impactmap.Map {
	job := func(files, dirs []string) *impactmap.Job {
		return &impactmap.Job{Files: files, Dirs: dirs, Complete: true, Nodes: 1}
	}
	return &impactmap.Map{
		Version: 1, Revision: "base1", CreatedAt: "2026-11-12T18:30:00Z", Method: "static",
		Jobs: map[string]*impactmap.Job{
			"lint": job(
				[]string{"Makefile", "README.md", "docs/index.md", "go.mod", "libs/shared/schema.json", "services/api/main.go", "services/web/src/index.ts"},
				[]string{"docs", "libs", "libs/shared", "services", "services/api", "services/web", "services/web/src"}),
			"api-test": job(
				[]string{"Makefile", "go.mod", "libs/shared/schema.json", "services/api/main.go"},
				[]string{"libs/shared", "services/api"}),
			"web-test": job(
				[]string{"libs/shared/schema.json", "package.json", "services/web/src/index.ts"},
				[]string{"libs/shared", "services/web/src"}),
			"build": job(
				[]string{"Makefile", "go.mod", "libs/shared/schema.json", "services/api/main.go", "services/web/src/index.ts"},
				[]string{"libs/shared", "services", "services/api", "services/web", "services/web/src"}),
			"e2e":           job([]string{"e2e/smoke.test.ts"}, []string{"e2e"}),
			"docs":          job([]string{"docs/index.md", "mkdocs.yml"}, []string{"docs"}),
			"security-scan": job([]string{"go.mod", "go.sum"}, nil),
		},
	}
}

// fakeRepo answers the planner's questions from memory.
type fakeRepo struct {
	changes    []diff.Change
	changesErr error
	// dirs[rev] is the set of directories that exist at rev.
	dirs     map[string]map[string]bool
	dirsErr  error
	panicMsg string
}

func (f *fakeRepo) Changes(ctx context.Context, base, head string) ([]diff.Change, error) {
	if f.panicMsg != "" {
		panic(f.panicMsg)
	}
	return f.changes, f.changesErr
}

func (f *fakeRepo) DirsExist(ctx context.Context, rev string, dirs []string) (map[string]bool, error) {
	if f.dirsErr != nil {
		return nil, f.dirsErr
	}
	out := map[string]bool{}
	for _, d := range dirs {
		out[d] = f.dirs[rev][d]
	}
	return out, nil
}

func changes(spec ...string) []diff.Change {
	var out []diff.Change
	for _, s := range spec {
		status, path, _ := strings.Cut(s, " ")
		out = append(out, diff.Change{Path: path, Status: status})
	}
	return out
}

// opts returns select-mode options for a feature branch.
func opts() planner.Options {
	o := planner.DefaultOptions()
	o.Mode = planner.ModeSelect
	o.Workflow = "ci"
	o.Branch = "feature"
	o.Base, o.Head = "base1", "head1"
	return o
}

// plan runs the planner and fails the test on an error.
func plan(t *testing.T, in planner.Input, o planner.Options) *planner.Result {
	t.Helper()
	res, err := planner.Run(context.Background(), in, o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Whatever is emitted must read back as a config.
	if _, err := config.Parse(res.Config); err != nil {
		t.Fatalf("emitted config does not parse: %v\n%s", err, res.Config)
	}
	return res
}

// input builds an Input for the monorepo fixture.
func input(repo *fakeRepo) planner.Input {
	return planner.Input{Config: []byte(monorepo), Map: monorepoMap(), Repo: repo, CommitMessage: "change things"}
}

// running lists the jobs the report says run.
func running(res *planner.Result) []string {
	var out []string
	for _, j := range res.Report.Jobs {
		if j.Run {
			out = append(out, j.Name)
		}
	}
	return out
}

func skipped(res *planner.Result) []string {
	var out []string
	for _, j := range res.Report.Jobs {
		if !j.Run {
			out = append(out, j.Name)
		}
	}
	return out
}

func reasons(t *testing.T, res *planner.Result, job string) []string {
	t.Helper()
	for _, j := range res.Report.Jobs {
		if j.Name == job {
			return j.Reasons
		}
	}
	t.Fatalf("no job %q in the report", job)
	return nil
}

// emittedJobs lists the entries of the emitted workflow.
func emittedJobs(t *testing.T, res *planner.Result, workflow string) []string {
	t.Helper()
	c, err := config.Parse(res.Config)
	if err != nil {
		t.Fatal(err)
	}
	w, err := c.Workflow(workflow)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range w.Entries {
		out = append(out, e.Name)
	}
	return out
}

func emittedEntry(t *testing.T, res *planner.Result, workflow, name string) *config.Entry {
	t.Helper()
	c, err := config.Parse(res.Config)
	if err != nil {
		t.Fatal(err)
	}
	w, err := c.Workflow(workflow)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range w.Entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no entry %q in the emitted workflow", name)
	return nil
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

var exprToken = regexp.MustCompile(`<<[^>]*>>`)

// noNewExpressions fails if out holds an << expression >> that in does not:
// the planner never adds pipeline parameters.
func noNewExpressions(t *testing.T, in, out []byte) {
	t.Helper()
	have := map[string]bool{}
	for _, m := range exprToken.FindAll(in, -1) {
		have[string(m)] = true
	}
	for _, m := range exprToken.FindAll(out, -1) {
		if !have[string(m)] {
			t.Errorf("the plan added an expression %s", m)
		}
	}
	if strings.Contains(string(out), "pipeline.parameters") && !strings.Contains(string(in), "pipeline.parameters") {
		t.Error("the plan added a pipeline parameter")
	}
}

var errBoom = errors.New("boom")

func fmtSet(s []string) string { return fmt.Sprintf("%v", s) }

// entryOf parses a config and returns the named entry of its first workflow
// that has one.
func entryOf(t *testing.T, cfgYAML, name string) *config.Entry {
	t.Helper()
	c, err := config.Parse([]byte(cfgYAML))
	if err != nil {
		t.Fatal(err)
	}
	for _, wfName := range c.Workflows() {
		w, err := c.Workflow(wfName)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range w.Entries {
			if e.Name == name {
				return e
			}
		}
	}
	t.Fatalf("no entry %q", name)
	return nil
}

// rawJob returns the definition of job name in cfg, read as plain YAML.
func rawJob(t *testing.T, cfg []byte, name string) map[string]any {
	t.Helper()
	return rbJob(t, rbDoc(t, cfg), name)
}
