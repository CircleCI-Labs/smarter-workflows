package config_test

import (
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
)

func mustParse(t *testing.T, src string) *config.Config {
	t.Helper()
	c, err := config.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

func mustMarshal(t *testing.T, c *config.Config) string {
	t.Helper()
	b, err := c.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Whatever we emit must be valid YAML with every alias resolvable.
	if _, err := config.Parse(b); err != nil {
		t.Fatalf("emitted config does not parse again: %v\n%s", err, b)
	}
	return string(b)
}

func mustWorkflow(t *testing.T, c *config.Config, name string) *config.Workflow {
	t.Helper()
	w, err := c.Workflow(name)
	if err != nil {
		t.Fatalf("Workflow(%q): %v", name, err)
	}
	return w
}

func names(w *config.Workflow) []string {
	var out []string
	for _, e := range w.Entries {
		out = append(out, e.Name)
	}
	return out
}

// normalised is already in the shape the encoder writes: no blank lines, one
// space before a line comment, sequences indented under their key.
const normalised = `# CI config for the example.
version: 2.1
orbs:
  node: circleci/node@5.1.0 # pinned
commands:
  setup:
    steps:
      - checkout # first
      - run: echo hi
jobs:
  # lint everything
  lint:
    docker:
      - image: cimg/base:2026.11
    steps:
      - checkout
      - run:
          name: "lint: all"
          command: |
            set -e
            echo "lint"
            cat <<EOF2
            x
            EOF2
  build: &build_defaults
    docker:
      - image: cimg/go:1.26
    steps:
      - checkout
workflows:
  ci:
    # the main jobs
    jobs:
      - lint
      # build comes after lint
      - build:
          requires:
            - lint # needs lint
          filters:
            branches:
              only: /^release\/.*/
      - hold:
          type: approval
      - other: *build_defaults
`

func TestRoundTripKeepsOrderCommentsAndStyle(t *testing.T) {
	c := mustParse(t, normalised)
	if got := mustMarshal(t, c); got != normalised {
		t.Fatalf("round trip changed the config:\n--- want\n%s\n--- got\n%s", normalised, got)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"empty", "", "empty"},
		{"only a comment", "# nothing\n", "empty"},
		{"two documents", "a: 1\n---\nb: 2\n", "more than one"},
		{"a list", "- a\n- b\n", "not a mapping"},
		{"a scalar", "hello\n", "not a mapping"},
		{"broken yaml", "a: [1, 2\n", "parse config"},
		{"unknown anchor", "a: *nope\n", "parse config"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Parse([]byte(tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestWorkflows(t *testing.T) {
	c := mustParse(t, `
version: 2.1
workflows:
  version: 2
  a:
    jobs: [x]
  b:
    when: false
    jobs:
      - y
  not-a-workflow: 3
  empty:
    jobs: []
`)
	if got, want := c.Workflows(), []string{"a", "b", "empty"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Workflows = %v, want %v", got, want)
	}
	if _, err := c.Workflow("nope"); err == nil {
		t.Fatal("want an error for an unknown workflow")
	}
	if _, err := mustParse(t, "version: 2.1\n").Workflow("a"); err == nil {
		t.Fatal("want an error for a config without workflows")
	}
}

func TestParseEntries(t *testing.T) {
	c := mustParse(t, `
version: 2.1
workflows:
  w:
    jobs:
      - plain
      - nothing:
      - renamed:
          name: other
          requires: [plain, nothing]
      - statuses:
          requires:
            - plain: failed
            - nothing: [success, canceled]
      - hold:
          type: approval
      - mat:
          matrix:
            alias: all-mat
            parameters:
              v: [a, b]
      - dyn:
          name: dyn-<< matrix.v >>
          matrix:
            parameters:
              v: [a, b]
      - orb/job:
          context: [c1]
`)
	w := mustWorkflow(t, c, "w")
	type brief struct {
		ref, name, alias string
		requires         []string
		status, approval bool
		matrix, dynamic  bool
	}
	var got []brief
	for _, e := range w.Entries {
		got = append(got, brief{e.Ref, e.Name, e.Alias, e.Requires, e.StatusRequires, e.Approval, e.Matrix, e.Dynamic})
	}
	want := []brief{
		{ref: "plain", name: "plain"},
		{ref: "nothing", name: "nothing"},
		{ref: "renamed", name: "other", requires: []string{"plain", "nothing"}},
		{ref: "statuses", name: "statuses", requires: []string{"plain", "nothing"}, status: true},
		{ref: "hold", name: "hold", approval: true},
		{ref: "mat", name: "mat", alias: "all-mat", matrix: true},
		{ref: "dyn", name: "dyn-<< matrix.v >>", matrix: true, dynamic: true},
		{ref: "orb/job", name: "orb/job"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries:\n got %+v\nwant %+v", got, want)
	}
	if got, want := w.Entries[2].Names(), []string{"other"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Names of a renamed entry = %v, want %v", got, want)
	}
	if got, want := w.Entries[5].Names(), []string{"mat", "all-mat"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Names of a matrix entry = %v, want %v", got, want)
	}
	if got, want := w.Entries[6].Names(), []string{"dyn"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Names of a dynamic matrix entry = %v, want %v", got, want)
	}
}

func TestParseEntriesAliasesAndMergeKeys(t *testing.T) {
	c := mustParse(t, `
version: 2.1
defaults: &defaults
  requires: [lint]
  filters:
    branches:
      only: main
workflows:
  w:
    jobs:
      - lint
      - merged:
          <<: *defaults
          name: m
      - aliased: *defaults
`)
	w := mustWorkflow(t, c, "w")
	if len(w.Entries) != 3 {
		t.Fatalf("entries = %v", names(w))
	}
	for _, e := range w.Entries[1:] {
		if !reflect.DeepEqual(e.Requires, []string{"lint"}) {
			t.Errorf("%s: Requires = %v", e.Name, e.Requires)
		}
		if b := e.Filters.Branches; b == nil || !reflect.DeepEqual(b.Only, []string{"main"}) {
			t.Errorf("%s: Filters = %+v", e.Name, e.Filters)
		}
	}
	if w.Entries[1].Name != "m" || w.Entries[2].Name != "aliased" {
		t.Errorf("names = %v: an explicit key must win over a merged one", names(w))
	}
}

func TestParseEntryErrors(t *testing.T) {
	tests := []struct{ name, jobs, want string }{
		{"two keys", "- a: {}\n        b: {}", "2 keys"},
		{"arguments are a list", "- a: [x]", "not a mapping"},
		{"name is a list", "- a:\n            name: [x]", "name of"},
		{"requires is a string", "- a:\n            requires: b", "not a list"},
		{"requires item is a list", "- a:\n            requires: [[x]]", "neither a name"},
		{"an entry that is a list", "- [a]", "neither a name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := mustParse(t, "version: 2.1\nworkflows:\n  w:\n    jobs:\n      "+tc.jobs+"\n")
			_, err := c.Workflow("w")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestFilters(t *testing.T) {
	c := mustParse(t, `
version: 2.1
workflows:
  w:
    jobs:
      - none
      - expr:
          filters: pipeline.git.branch == "main"
      - only-str:
          filters:
            branches:
              only: main
      - lists:
          filters:
            branches:
              only: [main, /^rel-.*/]
              ignore: wip
            tags:
              only: /^v.*/
      - param:
          filters:
            branches:
              only: << pipeline.parameters.b >>
      - odd:
          filters:
            other: x
`)
	w := mustWorkflow(t, c, "w")
	f := func(i int) config.Filters { return w.Entries[i].Filters }
	if f(0).Present {
		t.Error("none: Present")
	}
	if !f(1).Present || !f(1).Expr {
		t.Errorf("expr: %+v", f(1))
	}
	if b := f(2).Branches; b == nil || !b.HasOnly || !reflect.DeepEqual(b.Only, []string{"main"}) || b.HasIgnore || b.Dynamic || f(2).Tags != nil {
		t.Errorf("only-str: %+v / %+v", f(2), f(2).Branches)
	}
	b, tg := f(3).Branches, f(3).Tags
	if !reflect.DeepEqual(b.Only, []string{"main", "/^rel-.*/"}) || !reflect.DeepEqual(b.Ignore, []string{"wip"}) || !b.HasOnly || !b.HasIgnore {
		t.Errorf("lists branches: %+v", b)
	}
	if tg == nil || !reflect.DeepEqual(tg.Only, []string{"/^v.*/"}) {
		t.Errorf("lists tags: %+v", tg)
	}
	if b := f(4).Branches; b == nil || !b.Dynamic {
		t.Errorf("param: %+v", b)
	}
	if !f(5).Expr {
		t.Errorf("odd: %+v", f(5))
	}
}

func TestCondition(t *testing.T) {
	tests := []struct {
		body string
		want config.Condition
	}{
		{"", config.NoCondition},
		{"when: true", config.LiteralCondition},
		{"unless: false", config.LiteralCondition},
		{"when: true\n    unless: false", config.LiteralCondition},
		{"when: << pipeline.parameters.go >>", config.DynamicCondition},
		{"when: pipeline.git.branch == \"main\"", config.DynamicCondition},
		{"when:\n      and: [true, true]", config.DynamicCondition},
		{"when: \"true\"", config.DynamicCondition},
		{"when: yes", config.DynamicCondition},
		{"when: true\n    unless: << pipeline.parameters.x >>", config.DynamicCondition},
	}
	for _, tc := range tests {
		t.Run(tc.body, func(t *testing.T) {
			c := mustParse(t, "version: 2.1\nworkflows:\n  w:\n    "+tc.body+"\n    jobs: [a]\n")
			if got := mustWorkflow(t, c, "w").Condition(); got != tc.want {
				t.Fatalf("Condition = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPrune(t *testing.T) {
	const src = `version: 2.1
jobs:
  a:
    docker:
      - image: x
workflows:
  w:
    jobs:
      - lint
      - api: # the api
          requires:
            - lint
      - web:
          requires: [lint, api]
          filters:
            branches:
              only: main
      # docs sits last
      - docs:
          requires:
            - lint
  other:
    jobs:
      - lint
      - api
`
	tests := []struct {
		name   string
		remove []string
		want   string
	}{
		{
			name:   "remove a sibling: the requires of the others are scrubbed",
			remove: []string{"api"},
			want: `version: 2.1
jobs:
  a:
    docker:
      - image: x
workflows:
  w:
    jobs:
      - lint
      - web:
          requires: [lint]
          filters:
            branches:
              only: main
      # docs sits last
      - docs:
          requires:
            - lint
  other:
    jobs:
      - lint
      - api
`,
		},
		{
			name:   "an entry that loses all its requires keeps its other keys; one with only requires becomes a bare name",
			remove: []string{"lint", "web"},
			want: `version: 2.1
jobs:
  a:
    docker:
      - image: x
workflows:
  w:
    jobs:
      - api # the api
      # docs sits last
      - docs
  other:
    jobs:
      - lint
      - api
`,
		},
		{
			name:   "remove everything",
			remove: []string{"lint", "api", "web", "docs"},
			want: `version: 2.1
jobs:
  a:
    docker:
      - image: x
workflows:
  w:
    jobs: []
  other:
    jobs:
      - lint
      - api
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := mustParse(t, src)
			w := mustWorkflow(t, c, "w")
			remove := map[int]bool{}
			for i, e := range w.Entries {
				for _, n := range tc.remove {
					if e.Name == n {
						remove[i] = true
					}
				}
			}
			if err := w.Prune(remove); err != nil {
				t.Fatal(err)
			}
			if got := mustMarshal(t, c); got != tc.want {
				t.Fatalf("--- want\n%s\n--- got\n%s", tc.want, got)
			}
			for _, e := range w.Entries {
				for _, r := range e.Requires {
					for _, n := range tc.remove {
						if r == n {
							t.Errorf("entry %s still requires removed %s", e.Name, n)
						}
					}
				}
			}
		})
	}
}

func TestPruneKeepsANameThatAKeptEntryStillProvides(t *testing.T) {
	// The matrix entry "m" answers to "m" and, as a matrix, to its job "build".
	c := mustParse(t, `
version: 2.1
workflows:
  w:
    jobs:
      - build
      - build:
          name: m
          matrix:
            parameters:
              v: [a]
      - test:
          requires: [build]
`)
	w := mustWorkflow(t, c, "w")
	if err := w.Prune(map[int]bool{0: true}); err != nil {
		t.Fatal(err)
	}
	if got := w.Entries[len(w.Entries)-1].Requires; !reflect.DeepEqual(got, []string{"build"}) {
		t.Fatalf("requires = %v: the name is still provided by the matrix entry", got)
	}
}

func TestPruneWithAnchorsAndMergeKeys(t *testing.T) {
	t.Run("an anchor defined in a removed entry is inlined where it is still used", func(t *testing.T) {
		c := mustParse(t, `version: 2.1
workflows:
  w:
    jobs:
      - build:
          filters: &only_main
            branches:
              only: main
      - deploy:
          filters: *only_main
      - docs:
          filters: *only_main
`)
		w := mustWorkflow(t, c, "w")
		if err := w.Prune(map[int]bool{0: true}); err != nil {
			t.Fatal(err)
		}
		got := mustMarshal(t, c) // mustMarshal re-parses: a dangling alias would fail here
		want := `version: 2.1
workflows:
  w:
    jobs:
      - deploy:
          filters:
            branches:
              only: main
      - docs:
          filters:
            branches:
              only: main
`
		if got != want {
			t.Fatalf("--- want\n%s\n--- got\n%s", want, got)
		}
	})

	t.Run("a whole entry that is an alias of a removed entry", func(t *testing.T) {
		c := mustParse(t, `version: 2.1
workflows:
  w:
    jobs:
      - a: &entry
          context: x
      - b: *entry
`)
		w := mustWorkflow(t, c, "w")
		if err := w.Prune(map[int]bool{0: true}); err != nil {
			t.Fatal(err)
		}
		got := mustMarshal(t, c)
		if want := "version: 2.1\nworkflows:\n  w:\n    jobs:\n      - b:\n          context: x\n"; got != want {
			t.Fatalf("--- want\n%s\n--- got\n%s", want, got)
		}
	})

	t.Run("requires that come from a merge key are overridden, not edited in place", func(t *testing.T) {
		c := mustParse(t, `version: 2.1
shared: &shared
  requires: [lint]
workflows:
  w:
    jobs:
      - lint
      - a:
          <<: *shared
      - b:
          <<: *shared
          context: c
`)
		w := mustWorkflow(t, c, "w")
		if err := w.Prune(map[int]bool{0: true}); err != nil {
			t.Fatal(err)
		}
		got := mustMarshal(t, c)
		want := `version: 2.1
shared: &shared
  requires: [lint]
workflows:
  w:
    jobs:
      - a:
          <<: *shared
          requires: []
      - b:
          <<: *shared
          context: c
          requires: []
`
		if got != want {
			t.Fatalf("--- want\n%s\n--- got\n%s", want, got)
		}
		for _, e := range w.Entries {
			if len(e.Requires) != 0 {
				t.Errorf("%s: Requires = %v, want none", e.Name, e.Requires)
			}
		}
	})
}

func TestAppendEntryAndAddJob(t *testing.T) {
	t.Run("with a jobs section", func(t *testing.T) {
		c := mustParse(t, `version: 2.1
jobs:
  a:
    docker:
      - image: x
workflows:
  w:
    jobs: [a]
`)
		if err := c.AddJob("gate", config.Map("docker", config.Seq(config.Map("image", "cimg/base:2026.11")), "resource_class", "small")); err != nil {
			t.Fatal(err)
		}
		w := mustWorkflow(t, c, "w")
		if err := w.AppendEntry("gate", []string{"a", "b"}); err != nil {
			t.Fatal(err)
		}
		want := `version: 2.1
jobs:
  a:
    docker:
      - image: x
  gate:
    docker:
      - image: cimg/base:2026.11
    resource_class: small
workflows:
  w:
    jobs:
      - a
      - gate:
          requires:
            - a
            - b
`
		if got := mustMarshal(t, c); got != want {
			t.Fatalf("--- want\n%s\n--- got\n%s", want, got)
		}
	})

	t.Run("without a jobs section the section goes before the workflows", func(t *testing.T) {
		c := mustParse(t, `version: 2.1
orbs:
  o: x/y@1.2.3
workflows:
  w:
    jobs:
      - o/job
`)
		if err := c.AddJob("gate", config.Map("resource_class", "small")); err != nil {
			t.Fatal(err)
		}
		w := mustWorkflow(t, c, "w")
		if err := w.AppendEntry("gate", nil); err != nil {
			t.Fatal(err)
		}
		want := `version: 2.1
orbs:
  o: x/y@1.2.3
jobs:
  gate:
    resource_class: small
workflows:
  w:
    jobs:
      - o/job
      - gate
`
		if got := mustMarshal(t, c); got != want {
			t.Fatalf("--- want\n%s\n--- got\n%s", want, got)
		}
	})

	t.Run("name collisions are errors", func(t *testing.T) {
		c := mustParse(t, "version: 2.1\njobs:\n  gate: {}\nworkflows:\n  w:\n    jobs: [gate]\n")
		if err := c.AddJob("gate", config.Map()); err == nil {
			t.Error("AddJob: want an error for an existing job")
		}
		if err := mustWorkflow(t, c, "w").AppendEntry("gate", nil); err == nil {
			t.Error("AppendEntry: want an error for an existing entry")
		}
	})
}

func TestAddSteps(t *testing.T) {
	post := config.Map("run", "echo post")
	pre := config.Map("run", "echo pre")
	tests := []struct {
		name   string
		jobs   string
		target int
		want   string
	}{
		{
			name: "a bare name becomes a mapping and keeps its comment",
			jobs: "      - lint # lint it",
			want: `      - lint: # lint it
          pre-steps:
            - run: echo pre
          post-steps:
            - run: echo post`,
		},
		{
			name: "existing steps stay: ours go before the pre-steps and after the post-steps",
			jobs: `      - lint:
          pre-steps:
            - run: echo mine-pre
          post-steps:
            - run: echo mine-post`,
			want: `      - lint:
          pre-steps:
            - run: echo pre
            - run: echo mine-pre
          post-steps:
            - run: echo mine-post
            - run: echo post`,
		},
		{
			name: "flow style becomes block style",
			jobs: "      - lint: {requires: [a], post-steps: [{run: echo x}]}",
			want: `      - lint:
          requires: [a]
          post-steps:
            - {run: echo x}
            - run: echo post
          pre-steps:
            - run: echo pre`,
		},
		{
			name: "an empty argument list",
			jobs: "      - lint:",
			want: `      - lint:
          pre-steps:
            - run: echo pre
          post-steps:
            - run: echo post`,
		},
		{
			name:   "an argument mapping shared by an alias is copied, not edited",
			jobs:   "      - lint: &args\n          context: c\n      - docs: *args",
			target: 1,
			want: `      - lint: &args
          context: c
      - docs:
          context: c
          pre-steps:
            - run: echo pre
          post-steps:
            - run: echo post`,
		},
		{
			name: "steps that come from a merge key are copied into the entry",
			jobs: "      - lint:\n          <<: {post-steps: [{run: echo merged}]}",
			want: `      - lint:
          <<: {post-steps: [{run: echo merged}]}
          pre-steps:
            - run: echo pre
          post-steps:
            - {run: echo merged}
            - run: echo post`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := mustParse(t, "version: 2.1\nworkflows:\n  w:\n    jobs:\n"+tc.jobs+"\n")
			w := mustWorkflow(t, c, "w")
			target := w.Entries[tc.target]
			if err := w.AddPreSteps(target, cloneNode(pre)); err != nil {
				t.Fatal(err)
			}
			if err := w.AddPostSteps(target, cloneNode(post)); err != nil {
				t.Fatal(err)
			}
			want := "version: 2.1\nworkflows:\n  w:\n    jobs:\n" + tc.want + "\n"
			if got := mustMarshal(t, c); got != want {
				t.Fatalf("--- want\n%s\n--- got\n%s", want, got)
			}
		})
	}
}

func cloneNode(n *yaml.Node) *yaml.Node {
	b, _ := yaml.Marshal(n)
	var out yaml.Node
	if err := yaml.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out.Content[0]
}

func TestJobPlatform(t *testing.T) {
	c := mustParse(t, `
version: 2.1
orbs:
  o: x/y@1.2.3
executors:
  dock:
    docker:
      - image: x
  mac:
    macos:
      xcode: "16"
  vm:
    machine:
      image: ubuntu-2204:current
  chain:
    executor: dock
jobs:
  docker-job:
    docker: [{image: x}]
  machine-linux:
    machine:
      image: ubuntu-2404:2024.05.1
  machine-android:
    machine:
      image: android:2024.01.1
  machine-default:
    machine:
      image: default
  machine-windows:
    machine:
      image: windows-server-2022-gui:current
  machine-legacy:
    machine: true
  machine-param:
    machine:
      image: << parameters.img >>
  macos-job:
    macos:
      xcode: "16"
  by-name:
    executor: dock
  by-mapping:
    executor:
      name: dock
      extra: arg
  by-mac:
    executor: mac
  by-vm:
    executor: vm
  by-chain:
    executor: chain
  by-orb:
    executor: o/exec
  by-param:
    executor: << parameters.e >>
  no-executor:
    steps: []
workflows:
  w:
    jobs: [docker-job]
`)
	tests := map[string]config.Platform{
		"docker-job":      config.PlatformLinux,
		"machine-linux":   config.PlatformLinux,
		"machine-android": config.PlatformLinux,
		"machine-default": config.PlatformLinux,
		"machine-windows": config.PlatformOther,
		"machine-legacy":  config.PlatformUnknown,
		"machine-param":   config.PlatformUnknown,
		"macos-job":       config.PlatformOther,
		"by-name":         config.PlatformLinux,
		"by-mapping":      config.PlatformLinux,
		"by-mac":          config.PlatformOther,
		"by-vm":           config.PlatformLinux,
		"by-chain":        config.PlatformLinux,
		"by-orb":          config.PlatformUnknown,
		"by-param":        config.PlatformUnknown,
		"no-executor":     config.PlatformUnknown,
		"o/job":           config.PlatformUnknown,
		"missing":         config.PlatformUnknown,
	}
	for job, want := range tests {
		if got := c.JobPlatform(job); got != want {
			t.Errorf("JobPlatform(%q) = %v, want %v", job, got, want)
		}
	}
	if !c.HasJob("docker-job") || c.HasJob("o/job") {
		t.Error("HasJob is wrong")
	}
}

func TestWorkspace(t *testing.T) {
	c := mustParse(t, `
version: 2.1
orbs:
  o: x/y@1.2.3
commands:
  save:
    steps:
      - run: make
      - persist_to_workspace:
          root: .
          paths: [dist]
  load:
    steps:
      - when:
          condition: true
          steps:
            - attach_workspace:
                at: .
  nested:
    steps:
      - load
  loop-a:
    steps:
      - loop-b
  loop-b:
    steps:
      - loop-a
jobs:
  producer:
    docker: [{image: x}]
    steps:
      - checkout
      - persist_to_workspace:
          root: .
          paths: [out]
  consumer:
    docker: [{image: x}]
    steps:
      - attach_workspace:
          at: .
  via-command-persist:
    docker: [{image: x}]
    steps:
      - save
  via-command-attach:
    docker: [{image: x}]
    steps:
      - nested
  via-mapping-call:
    docker: [{image: x}]
    steps:
      - save:
          some: arg
  both:
    docker: [{image: x}]
    steps:
      - attach_workspace: {at: .}
      - persist_to_workspace: {root: ., paths: [x]}
  neither:
    docker: [{image: x}]
    steps:
      - checkout
      - o/orb-command
      - run: echo persist_to_workspace
  looping:
    docker: [{image: x}]
    steps:
      - loop-a
workflows:
  w:
    jobs: [producer]
`)
	type pa struct{ persists, attaches bool }
	tests := map[string]pa{
		"producer":            {true, false},
		"consumer":            {false, true},
		"via-command-persist": {true, false},
		"via-command-attach":  {false, true},
		"via-mapping-call":    {true, false},
		"both":                {true, true},
		"neither":             {false, false},
		"looping":             {false, false},
		"not-a-job":           {false, false},
	}
	for job, want := range tests {
		p, a := c.Workspace(job)
		if (pa{p, a}) != want {
			t.Errorf("Workspace(%q) = %v, %v; want %v, %v", job, p, a, want.persists, want.attaches)
		}
	}
}

func TestInsertAfterCheckout(t *testing.T) {
	const src = `version: 2.1
jobs:
  plain:
    docker: [{image: x}]
    steps:
      - run: first
      - checkout
      - run: second
  with-path:
    docker: [{image: x}]
    steps:
      - checkout:
          path: ~/src
      - run: after
  twice:
    docker: [{image: x}]
    steps:
      - checkout
      - run: middle
      - checkout
  none:
    docker: [{image: x}]
    steps:
      - run: nothing
  shared:
    docker: [{image: x}]
    steps: &steps
      - checkout
  aliased:
    docker: [{image: x}]
    steps: *steps
workflows:
  w:
    jobs: [plain]
`
	reset := func() *yaml.Node { return config.Map("run", "reset") }
	want := map[string]bool{"plain": true, "with-path": true, "twice": true, "none": false, "shared": true, "aliased": true}
	orders := [][]string{
		{"plain", "with-path", "twice", "none", "shared", "aliased"},
		{"aliased", "shared", "none", "twice", "with-path", "plain"},
	}
	for _, order := range orders {
		c := mustParse(t, src)
		for _, job := range order {
			got, err := c.InsertAfterCheckout(job, reset())
			if err != nil || got != want[job] {
				t.Errorf("InsertAfterCheckout(%q) = %v, %v; want %v", job, got, err, want[job])
			}
		}
		// A second call finds the step in place and adds nothing.
		for _, job := range order {
			if _, err := c.InsertAfterCheckout(job, reset()); err != nil {
				t.Fatal(err)
			}
		}
		out := mustMarshal(t, c)
		for _, w := range []string{
			"      - run: first\n      - checkout\n      - run: reset\n      - run: second\n",
			"      - checkout:\n          path: ~/src\n      - run: reset\n      - run: after\n",
			"      - checkout\n      - run: reset\n      - run: middle\n      - checkout\n",
			"  shared:\n    docker: [{image: x}]\n    steps: &steps\n      - checkout\n      - run: reset\n  aliased:\n    docker: [{image: x}]\n    steps:\n      - checkout\n      - run: reset\n",
		} {
			if !strings.Contains(out, w) {
				t.Errorf("order %v: output lacks:\n%s\nin:\n%s", order, w, out)
			}
		}
		if n := strings.Count(out, "run: reset"); n != 5 {
			t.Errorf("order %v: %d reset steps, want 5:\n%s", order, n, out)
		}
	}
	if _, err := mustParse(t, src).InsertAfterCheckout("missing", reset()); err == nil {
		t.Error("want an error for a job that is not defined")
	}
}

func TestBuildersEncode(t *testing.T) {
	c := mustParse(t, "version: 2.1\njobs: {}\nworkflows:\n  w:\n    jobs: [a]\n")
	step := config.RunStep("smarter: collect trace", "/bin/sh", "always", "echo one\necho \"two\"\n")
	if err := c.AddJob("j", config.Map("steps", config.Seq(step, config.Map("persist_to_workspace", config.Map("root", "/tmp/smarter", "paths", config.Strs("traces")))))); err != nil {
		t.Fatal(err)
	}
	want := `version: 2.1
jobs:
  j:
    steps:
      - run:
          name: 'smarter: collect trace'
          when: always
          shell: /bin/sh
          command: |
            echo one
            echo "two"
      - persist_to_workspace:
          root: /tmp/smarter
          paths:
            - traces
workflows:
  w:
    jobs: [a]
`
	if got := mustMarshal(t, c); got != want {
		t.Fatalf("--- want\n%s\n--- got\n%s", want, got)
	}
}

func TestBuildersPanicOnProgrammingErrors(t *testing.T) {
	for name, f := range map[string]func(){
		"odd args":   func() { config.Map("a") },
		"key type":   func() { config.Map(1, "a") },
		"value type": func() { config.Map("a", 1) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: want a panic", name)
				}
			}()
			f()
		}()
	}
}

func TestVersion(t *testing.T) {
	for src, want := range map[string]string{
		"version: 2.1\n":     "2.1",
		"version: 2\n":       "2",
		"version: \"2.1\"\n": "2.1",
		"jobs: {}\n":         "",
		"version: [2]\n":     "",
	} {
		if got := mustParse(t, src).Version(); got != want {
			t.Errorf("Version of %q = %q, want %q", src, got, want)
		}
	}
}
