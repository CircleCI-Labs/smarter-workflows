package catalog_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/catalog"
	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

// rich has one of every kind of entry. The jobs of the workflow are written out
// of order, so a sorted catalog shows.
const rich = `
version: 2.1
orbs:
  node: circleci/node@5.1.0
  local:
    jobs:
      hello:
        docker:
          - image: cimg/base:2026.11
        steps:
          - checkout
          - run: echo hello
executors:
  go:
    docker:
      - image: cimg/go:1.27
    resource_class: medium
commands:
  stash:
    steps:
      - persist_to_workspace:
          root: .
          paths: [out, bin]
jobs:
  test:
    executor: go
    steps:
      - checkout
      - run:
          name: Run the tests
          command: go test ./...
      - stash
  ship:
    docker:
      - image: cimg/base:2026.11
    steps:
      - checkout
      - attach_workspace:
          at: .
      - run: ./ship.sh
  build:
    machine:
      image: ubuntu-2204:2024.01.1
    steps:
      - checkout
  smoke:
    docker:
      - image: cimg/base:2026.11
    steps: [checkout]
  coordinator:
    type: no-op
  hold:
    type: approval
  release-svc:
    type: release
    plan_name: svc-release
job-groups:
  rollout:
    jobs:
      - smoke
      - build:
          name: verify
          requires: [smoke]
workflows:
  ci:
    jobs:
      - release-svc:
          requires: [ship]
      - ship:
          context: [prod, aws]
          serial-group: << pipeline.project.slug >>/ship
          requires:
            - test
            - coordinator: [success, failed]
          filters:
            branches:
              only: main
            tags:
              only: /^v.*/
      - hold:
          requires: [test]
      - coordinator:
          requires: [test]
      - test
      - build:
          name: build-all
          matrix:
            alias: builds
            parameters:
              os: [linux, mac]
              n: [1, 2]
      - node/test:
          requires: [test]
          filters: << pipeline.parameters.filters >>
      - local/hello
      - rollout:
          name: roll
          requires: [hold]
`

func buildRich(t *testing.T, m *impactmap.Map) *catalog.Catalog {
	t.Helper()
	cfg, err := config.Parse([]byte(rich))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Build(cfg, "ci", m)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func jobsByName(cat *catalog.Catalog) map[string]catalog.Job {
	out := map[string]catalog.Job{}
	for _, j := range cat.Jobs {
		out[j.Name] = j
	}
	return out
}

func TestJobsAreSortedAndHaveTheirKinds(t *testing.T) {
	cat := buildRich(t, nil)
	var names, kinds []string
	for _, j := range cat.Jobs {
		names = append(names, j.Name)
		kinds = append(kinds, j.Kind)
	}
	wantNames := []string{"build-all", "coordinator", "hold", "local/hello", "node/test", "release-svc", "roll", "ship", "test"}
	wantKinds := []string{"matrix", "no-op", "approval", "orb", "orb", "release", "job-group", "build", "build"}
	if !reflect.DeepEqual(names, wantNames) || !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("jobs:\n names %v\n kinds %v\nwant\n names %v\n kinds %v", names, kinds, wantNames, wantKinds)
	}
	if cat.Version != 1 || cat.Workflow != "ci" || cat.MapRevision != "" {
		t.Errorf("header = %d %q %q", cat.Version, cat.Workflow, cat.MapRevision)
	}
	if want := []catalog.Group{{Name: "rollout", Jobs: []string{"smoke", "verify"}}}; !reflect.DeepEqual(cat.JobGroups, want) {
		t.Errorf("job groups = %+v, want %+v", cat.JobGroups, want)
	}
}

func TestOneJobInFull(t *testing.T) {
	jobs := jobsByName(buildRich(t, nil))

	ship := jobs["ship"]
	wantShip := catalog.Job{
		Name: "ship", Ref: "ship", Kind: "build",
		Executor: catalog.Executor{Type: "docker", Image: "cimg/base:2026.11"},
		Contexts: []string{"aws", "prod"},
		Filters: catalog.Filters{
			Branches: &catalog.RefFilter{Only: []string{"main"}},
			Tags:     &catalog.RefFilter{Only: []string{"/^v.*/"}},
		},
		// Sorted by job, with the statuses as written.
		Requires:    []catalog.Require{{Job: "coordinator", Status: []string{"success", "failed"}}, {Job: "test", Status: []string{"success"}}},
		RequiredBy:  []string{"release-svc"},
		Workspace:   catalog.Workspace{Persists: []string{}, Attaches: boolp(true)},
		SerialGroup: "<< pipeline.project.slug >>/ship",
		Inputs:      catalog.Inputs{TopDirs: []string{}},
		Steps:       []string{"checkout", "attach_workspace", "run: ./ship.sh"},
	}
	if !reflect.DeepEqual(ship, wantShip) {
		t.Errorf("ship:\n got %+v\nwant %+v", ship, wantShip)
	}

	// A job that persists through a command, and runs on a named executor.
	test := jobs["test"]
	if want := (catalog.Executor{Type: "docker", Image: "cimg/go:1.27", ResourceClass: "medium"}); test.Executor != want {
		t.Errorf("test executor = %+v, want %+v", test.Executor, want)
	}
	if want := (catalog.Workspace{Persists: []string{"bin", "out"}, Attaches: boolp(false)}); !reflect.DeepEqual(test.Workspace, want) {
		t.Errorf("test workspace = %+v, want %+v", test.Workspace, want)
	}
	if want := []string{"checkout", "run: Run the tests", "stash"}; !reflect.DeepEqual(test.Steps, want) {
		t.Errorf("test steps = %v, want %v", test.Steps, want)
	}
	if want := []string{"coordinator", "hold", "node/test", "ship"}; !reflect.DeepEqual(test.RequiredBy, want) {
		t.Errorf("test required_by = %v, want %v", test.RequiredBy, want)
	}
}

// The steps that an entry brings, its pre-steps and post-steps, run in the job
// too, so what they do with workspaces is part of the entry.
func TestWorkspaceOfAnEntryIncludesItsOwnSteps(t *testing.T) {
	cfg, err := config.Parse([]byte(`
version: 2.1
jobs:
  compile:
    docker:
      - image: cimg/base:2026.11
    steps:
      - checkout
      - persist_to_workspace:
          root: .
          paths: [bin]
workflows:
  ci:
    jobs:
      - compile:
          pre-steps:
            - attach_workspace:
                at: .
          post-steps:
            - persist_to_workspace:
                root: .
                paths: [out, bin]
`))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Build(cfg, "ci", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := catalog.Workspace{Persists: []string{"bin", "out"}, Attaches: boolp(true)}
	if got := cat.Jobs[0].Workspace; !reflect.DeepEqual(got, want) {
		t.Errorf("workspace = %+v, want %+v", got, want)
	}
}

func TestEntriesThatRunNothingAndEntriesThatAreUnknown(t *testing.T) {
	jobs := jobsByName(buildRich(t, nil))
	noSteps := []string{"coordinator", "hold", "release-svc"}
	for _, name := range noSteps {
		j := jobs[name]
		if j.Executor.Type != "unknown" || len(j.Steps) != 0 || j.Steps == nil {
			t.Errorf("%s: executor %+v, steps %#v: an entry that runs nothing has no steps", name, j.Executor, j.Steps)
		}
		if j.Workspace.Attaches == nil || *j.Workspace.Attaches || len(j.Workspace.Persists) != 0 || j.Workspace.Persists == nil {
			t.Errorf("%s workspace = %+v, want persists [] and attaches false", name, j.Workspace)
		}
	}

	// A registry orb and a job group are unknown inside: nothing about them is claimed.
	for _, name := range []string{"node/test", "roll"} {
		j := jobs[name]
		if j.Executor != (catalog.Executor{Type: "unknown"}) || !reflect.DeepEqual(j.Steps, []string{"unknown"}) {
			t.Errorf("%s: executor %+v, steps %v, want unknown", name, j.Executor, j.Steps)
		}
		if j.Workspace.Persists != nil || j.Workspace.Attaches != nil {
			t.Errorf("%s workspace = %+v, want null and null", name, j.Workspace)
		}
	}
	// An inline orb is read.
	hello := jobs["local/hello"]
	if hello.Executor.Type != "docker" || !reflect.DeepEqual(hello.Steps, []string{"checkout", "run: echo hello"}) {
		t.Errorf("local/hello: executor %+v, steps %v", hello.Executor, hello.Steps)
	}
	if hello.Workspace.Attaches == nil || *hello.Workspace.Attaches {
		t.Errorf("local/hello workspace = %+v", hello.Workspace)
	}

	// Expression filters are said so, and not read.
	if got := jobs["node/test"].Filters; !got.Expression || got.Branches != nil || got.Tags != nil {
		t.Errorf("node/test filters = %+v", got)
	}
	// The group invocation keeps its own name, and requires the approval.
	if got := jobs["roll"]; got.Ref != "rollout" || !reflect.DeepEqual(got.Requires, []catalog.Require{{Job: "hold", Status: []string{"success"}}}) {
		t.Errorf("roll = %+v", got)
	}
}

func TestMatrixEntry(t *testing.T) {
	cat := buildRich(t, nil)
	jobs := jobsByName(cat)
	m := jobs["build-all"].Matrix
	want := &catalog.Matrix{Alias: "builds", Parameters: map[string][]any{"os": {"linux", "mac"}, "n": {1, 2}}}
	if m == nil || m.Alias != want.Alias || len(m.Parameters) != 2 || fmt.Sprint(m.Parameters["os"]) != "[linux mac]" || fmt.Sprint(m.Parameters["n"]) != "[1 2]" {
		t.Errorf("matrix = %+v, want %+v", m, want)
	}
	if jobs["build-all"].Ref != "build" || jobs["build-all"].Executor.Type != "machine" {
		t.Errorf("build-all = %+v: a matrix entry still shows the job it runs", jobs["build-all"])
	}
	if jobs["test"].Matrix != nil {
		t.Error("an entry with no matrix has one")
	}
	// The JSON says null, and not {}.
	out, err := cat.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Jobs []map[string]json.RawMessage `json:"jobs"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, j := range decoded.Jobs {
		if string(j["name"]) == `"test"` && string(j["matrix"]) != "null" {
			t.Errorf("matrix of test = %s, want null", j["matrix"])
		}
	}
}

func TestInputsComeFromTheImpactMap(t *testing.T) {
	m := &impactmap.Map{Version: 1, Revision: "abc123", Method: "static", Jobs: map[string]*impactmap.Job{
		"test": {Files: []string{"go.mod", "services/api/main.go", "services/api/x/y.go", "libs/shared/a.json", "README.md"}, Dirs: []string{".", "services", "services/api"}, Complete: true, Nodes: 1},
		"ship": {Files: []string{"ship.sh"}, Dirs: nil, Complete: false, Nodes: 1},
	}}
	cat := buildRich(t, m)
	if cat.MapRevision != "abc123" {
		t.Errorf("map_revision = %q", cat.MapRevision)
	}
	jobs := jobsByName(cat)
	want := catalog.Inputs{Mapped: true, Complete: true, Files: 5, Dirs: 3, TopDirs: []string{".", "libs/shared", "services/api"}}
	if got := jobs["test"].Inputs; !reflect.DeepEqual(got, want) {
		t.Errorf("test inputs = %+v, want %+v", got, want)
	}
	if got, want := jobs["ship"].Inputs, (catalog.Inputs{Mapped: true, Complete: false, Files: 1, TopDirs: []string{"."}}); !reflect.DeepEqual(got, want) {
		t.Errorf("ship inputs = %+v, want %+v", got, want)
	}
	// A job with no entry in the map, and an entry that runs nothing.
	for _, name := range []string{"build-all", "coordinator", "roll"} {
		if got, want := jobs[name].Inputs, (catalog.Inputs{TopDirs: []string{}}); !reflect.DeepEqual(got, want) {
			t.Errorf("%s inputs = %+v, want %+v", name, got, want)
		}
	}
}

func TestTopDirsAreCappedAtTheFinestDepthThatFits(t *testing.T) {
	var files, deep []string
	for i := 0; i < 15; i++ {
		files = append(files, fmt.Sprintf("area%02d/sub/file.go", i))
	}
	// 15 directories at depth 2 is too many; at depth 1 they are 15 as well, so the list is cut.
	deep = append(deep, files...)
	for i := 0; i < 8; i++ {
		deep = append(deep, fmt.Sprintf("tools/t%d/x/y.go", i), fmt.Sprintf("tools/t%d/z.go", i))
	}
	m := &impactmap.Map{Version: 1, Revision: "r", Method: "static", Jobs: map[string]*impactmap.Job{
		"test": {Files: files, Complete: true, Nodes: 1},
		"ship": {Files: deep[15:], Complete: true, Nodes: 1}, // 8 directories at depth 2 under tools/
	}}
	jobs := jobsByName(buildRich(t, m))
	if got := jobs["test"].Inputs.TopDirs; len(got) != 12 || got[0] != "area00" || got[11] != "area11" {
		t.Errorf("test top_dirs = %v, want the first 12 of area00...", got)
	}
	if got := jobs["ship"].Inputs.TopDirs; len(got) != 8 || got[0] != "tools/t0" {
		t.Errorf("ship top_dirs = %v, want 8 directories of depth 2", got)
	}

	// 13 directories of depth 2 under one parent collapse to that parent.
	var wide []string
	for i := 0; i < 13; i++ {
		wide = append(wide, fmt.Sprintf("services/s%02d/main.go", i))
	}
	m.Jobs["test"] = &impactmap.Job{Files: wide, Complete: true, Nodes: 1}
	if got := jobsByName(buildRich(t, m))["test"].Inputs.TopDirs; !reflect.DeepEqual(got, []string{"services"}) {
		t.Errorf("test top_dirs = %v, want [services]", got)
	}
}

// Two runs, and two orders of the same workflow, give the same bytes.
func TestOutputIsDeterministic(t *testing.T) {
	render := func(src string) string {
		cfg, err := config.Parse([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		cat, err := catalog.Build(cfg, "ci", nil)
		if err != nil {
			t.Fatal(err)
		}
		out, err := cat.JSON()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	a := render(rich)
	if b := render(rich); a != b {
		t.Fatal("two runs differ")
	}
	reversed := strings.Replace(rich, `      - release-svc:
          requires: [ship]
      - ship:`, `      - ship:`, 1)
	reversed = strings.Replace(reversed, `      - local/hello
`, `      - local/hello
      - release-svc:
          requires: [ship]
`, 1)
	if reversed == rich {
		t.Fatal("the test did not reorder the workflow")
	}
	if b := render(reversed); a != b {
		t.Errorf("a reordered workflow gives another catalog:\n%s", firstDiff(a, b))
	}
	if !strings.HasSuffix(a, "}\n") || strings.Contains(a, "\\u003c") {
		t.Errorf("the JSON must end with a newline and not escape < and >: %q", a[len(a)-20:])
	}
}

func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range al {
		if i >= len(bl) || al[i] != bl[i] {
			return fmt.Sprintf("line %d: %q vs %q", i+1, al[i], bl[min(i, len(bl)-1)])
		}
	}
	return "(prefix)"
}

func TestParseReadsWhatJSONWrites(t *testing.T) {
	cat := buildRich(t, nil)
	out, err := cat.JSON()
	if err != nil {
		t.Fatal(err)
	}
	back, err := catalog.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	again, err := back.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, again) {
		t.Error("Parse then JSON does not give the same bytes")
	}
	for _, bad := range []string{`{`, `{"version":2,"jobs":[]}`, `{"version":1}`, `[]`} {
		if _, err := catalog.Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) succeeded", bad)
		}
	}
}

func TestBuildErrors(t *testing.T) {
	cfg, err := config.Parse([]byte(rich))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Build(cfg, "nope", nil); err == nil {
		t.Error("an unknown workflow gives a catalog")
	}
}

func boolp(b bool) *bool { return &b }
