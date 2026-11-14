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
  build:
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
      - other:
          context: shared
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
      - explicit:
          requires:
            - plain: success
            - nothing: [success]
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
		// `success` is what a bare name means: it is not a status requirement.
		{ref: "explicit", name: "explicit", requires: []string{"plain", "nothing"}},
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
	const unchanged = `version: 2.1
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
	removing := func(w *config.Workflow, names ...string) map[int]bool {
		remove := map[int]bool{}
		for i, e := range w.Entries {
			for _, n := range names {
				if e.Name == n {
					remove[i] = true
				}
			}
		}
		return remove
	}
	tests := []struct {
		name   string
		remove []string
		inert  []string // the removed entries that do not run
		want   string
	}{
		{
			name:   "remove an entry that nothing requires: the others are not edited",
			remove: []string{"web"},
			want: `version: 2.1
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
			name:   "remove a chain: an entry and what requires it",
			remove: []string{"api", "web"},
			want: `version: 2.1
jobs:
  a:
    docker:
      - image: x
workflows:
  w:
    jobs:
      - lint
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
		{
			// CircleCI ignores a requirement on a job that does not run, so the
			// name goes from the entries that stay, and nothing else changes.
			name:   "a requirement on a removed job that does not run is dropped",
			remove: []string{"api"},
			inert:  []string{"api"},
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
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := mustParse(t, src)
			w := mustWorkflow(t, c, "w")
			if err := w.Prune(removing(w, tc.remove...), removing(w, tc.inert...)); err != nil {
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

	// Any other edit of a requires list could change what runs. Prune refuses,
	// and leaves the workflow alone.
	refusals := []struct {
		name          string
		remove, inert []string
		want          string
	}{
		{"a removed job that would run", []string{"api"}, nil, "would run"},
		{"a removed job that would run, whatever the others do", []string{"lint", "api"}, []string{"lint"}, "would run"},
		{"a job left with nothing to require", []string{"lint"}, []string{"lint"}, "no requirement"},
		{"a job whose every requirement is gone", []string{"lint", "api"}, []string{"lint", "api"}, "no requirement"},
	}
	for _, tc := range refusals {
		t.Run("refuses: "+tc.name, func(t *testing.T) {
			c := mustParse(t, src)
			w := mustWorkflow(t, c, "w")
			err := w.Prune(removing(w, tc.remove...), removing(w, tc.inert...))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want a refusal that says %q", err, tc.want)
			}
			if after := mustMarshal(t, c); after != unchanged {
				t.Errorf("a refused prune changed the config:\n%s", after)
			}
			if got := names(w); !reflect.DeepEqual(got, []string{"lint", "api", "web", "docs"}) {
				t.Errorf("entries after a refused prune = %v", got)
			}
		})
	}
}

// Prune edits nothing unless it can do all of it.
func TestPruneIsAllOrNothing(t *testing.T) {
	c := mustParse(t, `version: 2.1
workflows:
  w:
    jobs:
      - a
      - b:
          requires: [a, c]
      - c
      - d:
          requires: [a]
`)
	before := mustMarshal(t, c)
	w := mustWorkflow(t, c, "w")
	// b could lose a, but d would be left with nothing.
	if err := w.Prune(map[int]bool{0: true}, map[int]bool{0: true}); err == nil {
		t.Fatal("Prune left d with no requirement")
	}
	if after := mustMarshal(t, c); after != before {
		t.Errorf("a refused prune edited b:\n%s", after)
	}
}

// A requirement with a status goes like any other when its job does not run.
func TestPruneDropsAStatusRequirementOnAJobThatDoesNotRun(t *testing.T) {
	c := mustParse(t, `version: 2.1
workflows:
  w:
    jobs:
      - build
      - deploy
      - notify:
          requires:
            - build: failed
            - deploy: [success, canceled]
`)
	w := mustWorkflow(t, c, "w")
	if err := w.Prune(map[int]bool{1: true}, map[int]bool{1: true}); err != nil {
		t.Fatal(err)
	}
	want := `version: 2.1
workflows:
  w:
    jobs:
      - build
      - notify:
          requires:
            - build: failed
`
	if got := mustMarshal(t, c); got != want {
		t.Fatalf("--- want\n%s\n--- got\n%s", want, got)
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
	if err := w.Prune(map[int]bool{0: true}, nil); err != nil {
		t.Fatal(err)
	}
	if got := w.Entries[len(w.Entries)-1].Requires; !reflect.DeepEqual(got, []string{"build"}) {
		t.Fatalf("requires = %v: the name is still provided by the matrix entry", got)
	}
}

func TestPruneWithAnchorsAndMergeKeys(t *testing.T) {
	t.Run("an anchor defined in a removed entry: the entries that used it keep their own copy", func(t *testing.T) {
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
		if err := w.Prune(map[int]bool{0: true}, nil); err != nil {
			t.Fatal(err)
		}
		got := mustMarshal(t, c)
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

	t.Run("an entry in another workflow keeps its copy when the anchor's entry goes", func(t *testing.T) {
		c := mustParse(t, `version: 2.1
workflows:
  ci:
    jobs:
      - lint:
          filters: &feature-only
            branches:
              ignore: main
  nightly:
    jobs:
      - deploy:
          filters: *feature-only
`)
		if err := mustWorkflow(t, c, "ci").Prune(map[int]bool{0: true}, nil); err != nil {
			t.Fatal(err)
		}
		got := mustMarshal(t, c)
		want := `version: 2.1
workflows:
  ci:
    jobs: []
  nightly:
    jobs:
      - deploy:
          filters:
            branches:
              ignore: main
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
		if err := w.Prune(map[int]bool{0: true}, nil); err != nil {
			t.Fatal(err)
		}
		got := mustMarshal(t, c)
		if want := "version: 2.1\nworkflows:\n  w:\n    jobs:\n      - b:\n          context: x\n"; got != want {
			t.Fatalf("--- want\n%s\n--- got\n%s", want, got)
		}
	})

	t.Run("requires that come from a merge key are edited in the entry that has them", func(t *testing.T) {
		c := mustParse(t, `version: 2.1
shared: &shared
  requires: [lint, build]
workflows:
  w:
    jobs:
      - lint
      - build
      - a:
          <<: *shared
      - b:
          <<: *shared
          context: c
`)
		w := mustWorkflow(t, c, "w")
		for _, e := range w.Entries[2:] {
			if !reflect.DeepEqual(e.Requires, []string{"lint", "build"}) {
				t.Fatalf("%s: Requires = %v, want [lint build]", e.Name, e.Requires)
			}
		}
		// lint does not run: a and b each lose it, and the shared block, which
		// each took a copy of, is as it was.
		if err := w.Prune(map[int]bool{0: true}, map[int]bool{0: true}); err != nil {
			t.Fatal(err)
		}
		want := `version: 2.1
shared:
  requires: [lint, build]
workflows:
  w:
    jobs:
      - build
      - a:
          requires: [build]
      - b:
          requires: [build]
          context: c
`
		if got := mustMarshal(t, c); got != want {
			t.Fatalf("--- want\n%s\n--- got\n%s", want, got)
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
			name: "a key with no value is an empty list",
			jobs: "      - lint:\n          post-steps:\n          pre-steps: null",
			want: `      - lint:
          post-steps:
            - run: echo post
          pre-steps:
            - run: echo pre`,
		},
		{
			name: "a key that is ~ is an empty list too",
			jobs: "      - lint:\n          post-steps: ~",
			want: `      - lint:
          post-steps:
            - run: echo post
          pre-steps:
            - run: echo pre`,
		},
		{
			name:   "an argument mapping shared by an alias is its own copy: the anchor's entry is not edited",
			jobs:   "      - lint: &args\n          context: c\n      - docs: *args",
			target: 1,
			want: `      - lint:
          context: c
      - docs:
          context: c
          pre-steps:
            - run: echo pre
          post-steps:
            - run: echo post`,
		},
		{
			name:   "the anchor's entry may be edited too: the alias is not touched",
			jobs:   "      - lint: &args\n          context: c\n      - docs: *args",
			target: 0,
			want: `      - lint:
          context: c
          pre-steps:
            - run: echo pre
          post-steps:
            - run: echo post
      - docs:
          context: c`,
		},
		{
			name: "steps that come from a merge key are in the entry already",
			jobs: "      - lint:\n          <<: {post-steps: [{run: echo merged}]}",
			want: `      - lint:
          post-steps:
            - {run: echo merged}
            - run: echo post
          pre-steps:
            - run: echo pre`,
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

// An entry whose pre-steps or post-steps is not a list cannot take steps. It is
// left exactly as it was, and CheckSteps says so beforehand.
func TestAddStepsLeavesAnEntryItCannotEditAlone(t *testing.T) {
	step := func() *yaml.Node { return config.Map("run", "echo hi") }
	tests := []struct{ name, key, entry string }{
		{"post-steps is a string", "post-steps", "      - lint:\n          post-steps: echo hi"},
		{"post-steps is a mapping", "post-steps", "      - lint:\n          post-steps: {run: echo hi}"},
		{"pre-steps is a number", "pre-steps", "      - lint:\n          context: c\n          pre-steps: 3"},
		{"post-steps is an empty string", "post-steps", "      - lint:\n          post-steps: ''"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := "version: 2.1\nworkflows:\n  w:\n    jobs:\n" + tc.entry + "\n"
			c := mustParse(t, src)
			before := mustMarshal(t, c)
			w := mustWorkflow(t, c, "w")
			e := w.Entries[0]
			if err := e.CheckSteps(); err == nil || !strings.Contains(err.Error(), tc.key+` of "lint" is not a list`) {
				t.Errorf("CheckSteps = %v, want an error about %s", err, tc.key)
			}
			add := w.AddPostSteps
			if tc.key == "pre-steps" {
				add = w.AddPreSteps
			}
			if err := add(e, step()); err == nil {
				t.Error("adding steps: want an error")
			}
			if after := mustMarshal(t, c); after != before {
				t.Errorf("the entry changed:\n--- before\n%s\n--- after\n%s", before, after)
			}
		})
	}

	t.Run("an entry that can be edited passes the check", func(t *testing.T) {
		c := mustParse(t, "version: 2.1\nworkflows:\n  w:\n    jobs:\n      - bare\n      - empty:\n      - own:\n          pre-steps: [{run: a}]\n          post-steps:\n")
		for _, e := range mustWorkflow(t, c, "w").Entries {
			if err := e.CheckSteps(); err != nil {
				t.Errorf("%s: CheckSteps = %v", e.Name, err)
			}
		}
	})
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
			"  shared:\n    docker: [{image: x}]\n    steps:\n      - checkout\n      - run: reset\n  aliased:\n    docker: [{image: x}]\n    steps:\n      - checkout\n      - run: reset\n",
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

	// `aliased` took its steps from `shared` through an alias. Each job has its
	// own copy, so an edit to one is not an edit to the other.
	for _, job := range []string{"shared", "aliased"} {
		c := mustParse(t, src)
		if _, err := c.InsertAfterCheckout(job, reset()); err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(mustMarshal(t, c), "run: reset"); n != 1 {
			t.Errorf("a reset step in %s was written %d times, want 1", job, n)
		}
	}
}

// A job that gets its steps from a merge key has them in its own mapping, so
// the reset can follow its checkout like any other.
func TestInsertAfterCheckoutThroughAMergeKey(t *testing.T) {
	c := mustParse(t, `version: 2.1
defaults: &defaults
  docker: [{image: x}]
  steps:
    - checkout
    - run: build
jobs:
  merged:
    <<: *defaults
  other:
    <<: *defaults
workflows:
  w:
    jobs: [merged]
`)
	got, err := c.InsertAfterCheckout("merged", config.Map("run", "reset"))
	if err != nil || !got {
		t.Fatalf("InsertAfterCheckout = %v, %v", got, err)
	}
	out := mustMarshal(t, c)
	want := `jobs:
  merged:
    docker: [{image: x}]
    steps:
      - checkout
      - run: reset
      - run: build
  other:
    docker: [{image: x}]
    steps:
      - checkout
      - run: build
`
	if !strings.Contains(out, want) {
		t.Fatalf("output lacks:\n%s\nin:\n%s", want, out)
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

// Whatever the planner does not touch must keep its meaning. This decodes the
// config before and after a parse and emit round trip and compares the data.
func TestRoundTripKeepsTheMeaningOfAwkwardYAML(t *testing.T) {
	sources := map[string]string{
		"block scalars": `version: 2.1
jobs:
  a:
    docker: [{image: x}]
    steps:
      - run:
          name: strip
          command: |-
            echo one
            echo two
      - run:
          name: keep
          command: |+
            echo kept

      - run:
          name: folded
          command: >
            echo this is
            folded text
      - run:
          name: trailing spaces and tabs
          command: "echo a   \n\techo b\t\n"
workflows:
  w:
    jobs: [a]
`,
		"anchors and merge keys": `version: 2.1
defaults: &defaults
  docker: [{image: x}]
  environment: &env
    A: "1"
    B: two
jobs:
  a:
    <<: *defaults
    environment:
      <<: *env
      C: three
    steps: [checkout]
  b:
    <<: *defaults
    steps: [checkout]
workflows:
  w:
    jobs: [a, b]
`,
		"quoting, escapes and unicode": `version: 2.1
jobs:
  a:
    docker: [{image: x}]
    environment:
      PLAIN: value
      SINGLE: 'it''s'
      DOUBLE: "tab\there \"quoted\" \\ back"
      UNICODE: "café ☃ 日本語"
      EMPTY: ""
      NULLISH: ~
      BOOL: "true"
      NUMBER: "007"
      COLON: "a: b"
      HASH: "a #b"
    steps: [checkout]
workflows:
  w:
    jobs: [a]
`,
		"flow collections and long strings": `version: 2.1
jobs:
  a:
    docker: [{image: x, environment: {A: 1, B: [1, 2, {c: d}]}}]
    steps:
      - run: "` + strings.Repeat("a long sentence that goes on and on ", 12) + `"
workflows:
  w:
    jobs: [a]
`,
		"numbers and versions": `version: 2.1
jobs:
  a:
    docker: [{image: x}]
    parallelism: 4
    resource_class: small
    environment:
      FLOAT: 1.10
      INT: 0x1F
      OCT: 0o17
      EXP: 1e3
    steps: [checkout]
workflows:
  w:
    jobs: [a]
`,
	}
	for name, src := range sources {
		t.Run(name, func(t *testing.T) {
			var before, after any
			if err := yaml.Unmarshal([]byte(src), &before); err != nil {
				t.Fatalf("the source is not valid YAML: %v", err)
			}
			out := mustMarshal(t, mustParse(t, src))
			if err := yaml.Unmarshal([]byte(out), &after); err != nil {
				t.Fatalf("the output is not valid YAML: %v\n%s", err, out)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("the round trip changed the data\n--- in\n%s\n--- out\n%s", src, out)
			}
			// And once emitted, emitting again changes nothing.
			if again := mustMarshal(t, mustParse(t, out)); again != out {
				t.Fatalf("a second round trip changed the text:\n--- first\n%s\n--- second\n%s", out, again)
			}
		})
	}
}
