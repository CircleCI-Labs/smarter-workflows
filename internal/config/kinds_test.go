package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
)

const kindsConfig = `
version: 2.1
orbs:
  node: circleci/node@5.1.0
  local:
    executors:
      small:
        docker:
          - image: cimg/base:2026.11
        resource_class: small
    commands:
      greet:
        steps:
          - persist_to_workspace:
              root: .
              paths: [greeting]
    jobs:
      hello:
        executor: small
        steps:
          - checkout
          - greet
          - run: echo hello
      skip:
        type: no-op
jobs:
  build:
    docker:
      - image: cimg/go:1.27
    steps:
      - checkout
  tests:
    type: build
    docker:
      - image: cimg/go:1.27
    steps:
      - checkout
  coordinator:
    type: no-op
  hold:
    type: approval
  release-svc:
    type: release
    plan_name: svc-release
  odd:
    type: surprise
    docker:
      - image: cimg/go:1.27
    steps: [checkout]
job-groups:
  deploy-and-verify:
    jobs:
      - build
      - smoke:
          name: verify
          requires: [build]
workflows:
  w:
    jobs:
      - build
      - tests:
          requires: [build]
      - coordinator:
          requires: [tests]
      - hold:
          requires: [coordinator]
      - approve-by-entry:
          type: approval
          requires: [coordinator]
      - release-svc:
          requires: [hold]
      - deploy-and-verify:
          name: ship
          serial-group: << pipeline.project.slug >>/deploy
          requires: [coordinator]
      - local/hello
      - node/test:
          context: [one, two]
      - mystery
      - odd
`

func TestEntryKinds(t *testing.T) {
	c := mustParse(t, kindsConfig)
	w := mustWorkflow(t, c, "w")
	type brief struct {
		name     string
		kind     config.Kind
		approval bool
	}
	var got []brief
	for _, e := range w.Entries {
		got = append(got, brief{e.Name, e.Kind, e.Approval})
	}
	want := []brief{
		{"build", config.KindJob, false},
		{"tests", config.KindJob, false},
		// type: no-op is read from the definition, not from the entry.
		{"coordinator", config.KindNoOp, false},
		// An approval is `type: approval` in the job's definition, or on the entry.
		{"hold", config.KindApproval, true},
		{"approve-by-entry", config.KindApproval, true},
		{"release-svc", config.KindRelease, false},
		{"ship", config.KindJobGroup, false},
		// Jobs of an orb, inline or not, and names that nothing defines.
		{"local/hello", config.KindOrb, false},
		{"node/test", config.KindOrb, false},
		{"mystery", config.KindOrb, false},
		// A type that this package does not know is an ordinary job.
		{"odd", config.KindJob, false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds:\n got %+v\nwant %+v", got, want)
	}
}

func TestKindHasSteps(t *testing.T) {
	for kind, want := range map[config.Kind]bool{
		config.KindJob: true, config.KindOrb: true, config.KindJobGroup: true,
		config.KindApproval: false, config.KindNoOp: false, config.KindRelease: false,
	} {
		if got := kind.HasSteps(); got != want {
			t.Errorf("%s.HasSteps() = %v, want %v", kind, got, want)
		}
	}
}

func TestEntryContextsSerialGroupAndRequirements(t *testing.T) {
	c := mustParse(t, kindsConfig+`
  other:
    jobs:
      - build:
          context: solo
      - tests:
          context: [a, b]
          serial-group: plain/group
          requires:
            - build
            - coordinator: [success, failed]
            - hold: failed
`)
	byName := map[string]*config.Entry{}
	for _, e := range mustWorkflow(t, c, "w").Entries {
		byName[e.Name] = e
	}
	if got, want := byName["node/test"].Contexts, []string{"one", "two"}; !reflect.DeepEqual(got, want) {
		t.Errorf("contexts = %v, want %v", got, want)
	}
	if got := byName["build"].Contexts; got != nil {
		t.Errorf("an entry with no context has contexts %v", got)
	}
	// An expression is kept as written: the planner and the catalog do not evaluate it.
	if got, want := byName["ship"].SerialGroup, "<< pipeline.project.slug >>/deploy"; got != want {
		t.Errorf("serial group = %q, want %q", got, want)
	}
	if got := byName["build"].SerialGroup; got != "" {
		t.Errorf("an entry with no serial-group has %q", got)
	}

	other := mustWorkflow(t, c, "other").Entries
	if got := other[0].Contexts; !reflect.DeepEqual(got, []string{"solo"}) {
		t.Errorf("a context given as a string = %v", got)
	}
	tests := other[1]
	if tests.SerialGroup != "plain/group" || !reflect.DeepEqual(tests.Contexts, []string{"a", "b"}) {
		t.Errorf("tests = %+v", tests)
	}
	wantReqs := []config.Requirement{
		{Job: "build", Statuses: []string{"success"}},
		{Job: "coordinator", Statuses: []string{"success", "failed"}},
		{Job: "hold", Statuses: []string{"failed"}},
	}
	if !reflect.DeepEqual(tests.Requirements, wantReqs) {
		t.Errorf("requirements = %+v, want %+v", tests.Requirements, wantReqs)
	}
	if !reflect.DeepEqual(tests.Requires, []string{"build", "coordinator", "hold"}) || !tests.StatusRequires {
		t.Errorf("requires = %v, status = %v", tests.Requires, tests.StatusRequires)
	}
}

func TestJobGroups(t *testing.T) {
	c := mustParse(t, kindsConfig)
	got := c.JobGroups()
	want := []config.JobGroup{{Name: "deploy-and-verify", Jobs: []string{"build", "verify"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JobGroups = %+v, want %+v", got, want)
	}
	if g := c.JobGroupOf("verify"); g != "deploy-and-verify" {
		t.Errorf("JobGroupOf(verify) = %q", g)
	}
	if g := c.JobGroupOf("nowhere"); g != "" {
		t.Errorf("JobGroupOf(nowhere) = %q", g)
	}
	if got := mustParse(t, "version: 2.1\n").JobGroups(); len(got) != 0 {
		t.Errorf("a config with no job-groups has groups %+v", got)
	}
}

func TestJobDef(t *testing.T) {
	c := mustParse(t, `
version: 2.1
orbs:
  node: circleci/node@5.1.0
  local:
    executors:
      small:
        docker:
          - image: cimg/base:2026.11
        resource_class: small
    commands:
      greet:
        steps:
          - persist_to_workspace:
              root: .
              paths: [greeting]
    jobs:
      hello:
        executor: small
        steps:
          - checkout
          - greet
      skip:
        type: no-op
executors:
  fast:
    docker:
      - image: cimg/node:24
    resource_class: large
commands:
  save:
    steps:
      - persist_to_workspace:
          root: out
          paths: [b, a]
jobs:
  api:
    docker:
      - image: cimg/go:1.27
      - image: cimg/postgres:17
    resource_class: medium
    steps:
      - checkout
      - run:
          name: Test the API
          command: make test
      - run: |
          set -e

          make build
      - run:
          command: "  \n  echo from the command  \n"
      - save
      - persist_to_workspace:
          root: .
          paths: [dist, a]
      - store_artifacts:
          path: out
      - when:
          condition: true
          steps: [checkout]
  web:
    executor: fast
    steps:
      - checkout
      - attach_workspace:
          at: .
      - run: `+strings.Repeat("x", 100)+`
  tuned:
    executor:
      name: fast
    resource_class: xlarge
    steps: [checkout]
  vm:
    machine:
      image: ubuntu-2204:2024.01.1
    steps: [checkout]
  legacy:
    machine: true
    steps: [checkout]
  win:
    machine:
      image: windows-server-2022-gui:current
    steps: [checkout]
  mac:
    macos:
      xcode: 15.4.0
    resource_class: macos.m1.medium.gen1
    steps: [checkout]
  param:
    executor: << parameters.exec >>
    steps: [checkout]
  noop:
    type: no-op
    docker:
      - image: cimg/base:2026.11
  hold:
    type: approval
  rel:
    type: release
    plan_name: p
`)
	type brief struct {
		Known    bool
		Executor config.Executor
		Steps    []string
		Persists []string
		Attaches bool
	}
	tests := []struct {
		ref  string
		want brief
	}{
		{"api", brief{true, config.Executor{Type: "docker", Image: "cimg/go:1.27", ResourceClass: "medium"},
			[]string{"checkout", "run: Test the API", "run: set -e", "run: echo from the command", "save", "persist_to_workspace", "store_artifacts", "when"},
			[]string{"a", "b", "dist"}, false}},
		{"web", brief{true, config.Executor{Type: "docker", Image: "cimg/node:24", ResourceClass: "large"},
			[]string{"checkout", "attach_workspace", "run: " + strings.Repeat("x", 77) + "..."}, []string{}, true}},
		{"tuned", brief{true, config.Executor{Type: "docker", Image: "cimg/node:24", ResourceClass: "xlarge"}, []string{"checkout"}, []string{}, false}},
		{"vm", brief{true, config.Executor{Type: "machine", Image: "ubuntu-2204:2024.01.1"}, []string{"checkout"}, []string{}, false}},
		{"legacy", brief{true, config.Executor{Type: "machine"}, []string{"checkout"}, []string{}, false}},
		{"win", brief{true, config.Executor{Type: "windows", Image: "windows-server-2022-gui:current"}, []string{"checkout"}, []string{}, false}},
		{"mac", brief{true, config.Executor{Type: "macos", Image: "xcode:15.4.0", ResourceClass: "macos.m1.medium.gen1"}, []string{"checkout"}, []string{}, false}},
		{"param", brief{true, config.Executor{Type: "unknown"}, []string{"checkout"}, []string{}, false}},
		// An approval, a no-op and a release job have no executor and no steps,
		// even when a no-op job's definition carries one that CircleCI ignores.
		{"noop", brief{true, config.Executor{Type: "unknown"}, []string{}, []string{}, false}},
		{"hold", brief{true, config.Executor{Type: "unknown"}, []string{}, []string{}, false}},
		{"rel", brief{true, config.Executor{Type: "unknown"}, []string{}, []string{}, false}},
		// An inline orb's job is read, with the orb's own executors and commands.
		{"local/hello", brief{true, config.Executor{Type: "docker", Image: "cimg/base:2026.11", ResourceClass: "small"},
			[]string{"checkout", "greet"}, []string{"greeting"}, false}},
		{"local/skip", brief{true, config.Executor{Type: "unknown"}, []string{}, []string{}, false}},
		// A registry orb's job, an inline orb's missing job, and a name nothing defines.
		{"node/test", brief{false, config.Executor{Type: "unknown"}, nil, nil, false}},
		{"local/missing", brief{false, config.Executor{Type: "unknown"}, nil, nil, false}},
		{"mystery", brief{false, config.Executor{Type: "unknown"}, nil, nil, false}},
	}
	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			jd := c.JobDef(tc.ref)
			got := brief{jd.Known, jd.Executor, jd.Steps, jd.Persists, jd.Attaches}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("JobDef(%q):\n got %+v\nwant %+v", tc.ref, got, tc.want)
			}
		})
	}
}
