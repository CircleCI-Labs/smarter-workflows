package planner_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

// sourceDirs is the directory tree of the fixture at base.
func sourceDirs() map[string]bool {
	return map[string]bool{
		".": true, "docs": true, "e2e": true, "libs": true, "libs/shared": true, "scripts": true,
		"services": true, "services/api": true, "services/web": true, "services/web/src": true,
	}
}

func TestSelection(t *testing.T) {
	scan := func(o *planner.Options) { o.Mandatory = []string{"security-scan"} }
	tests := []struct {
		name     string
		changes  []diff.Change
		headDirs map[string]bool // directories at head; nil means the same as base
		mod      func(*planner.Options)
		mapMod   func(*impactmap.Map)
		wantRun  []string
		// reasons maps a job to substrings that must appear in its reasons.
		reasons map[string][]string
	}{
		{
			name:    "docs only",
			changes: changes("M docs/index.md"),
			mod:     scan,
			wantRun: []string{"lint", "security-scan", "docs"},
			reasons: map[string][]string{
				"lint":          {"reads docs/index.md"},
				"docs":          {"reads docs/index.md"},
				"security-scan": {"mandatory job"},
				"api-test":      {"inputs unchanged"},
				"web-test":      {"inputs unchanged"},
				"build":         {"inputs unchanged"},
				"e2e":           {"inputs unchanged"},
				"hold":          {"does not run: its filters leave out branch feature"},
				"deploy":        {"does not run: its filters leave out branch feature"},
			},
		},
		{
			name:    "one service: the workspace consumer follows its producer",
			changes: changes("M services/api/main.go"),
			mod:     scan,
			wantRun: []string{"lint", "security-scan", "api-test", "build", "e2e"},
			reasons: map[string][]string{
				"api-test": {"reads services/api/main.go"},
				"build":    {"reads services/api/main.go"},
				"e2e":      {"consumes the workspace of build"},
				"web-test": {"inputs unchanged"},
				"docs":     {"inputs unchanged"},
			},
		},
		{
			name:    "a shared library reaches both services",
			changes: changes("M libs/shared/schema.json"),
			mod:     scan,
			wantRun: []string{"lint", "security-scan", "api-test", "web-test", "build", "e2e"},
			reasons: map[string][]string{"api-test": {"reads libs/shared/schema.json"}, "web-test": {"reads libs/shared/schema.json"}},
		},
		{
			name:    "a file nobody read selects only the mandatory job",
			changes: changes("M CHANGELOG.md"),
			mod:     scan,
			wantRun: []string{"security-scan"},
		},
		{
			name:    "nothing changed",
			changes: nil,
			mod:     scan,
			wantRun: []string{"security-scan"},
		},
		{
			name:    "a deleted file that a job read",
			changes: changes("D services/api/main.go"),
			mod:     scan,
			wantRun: []string{"lint", "security-scan", "api-test", "build", "e2e"},
		},
		{
			name:    "a file added to a directory a job listed",
			changes: changes("A services/web/src/new.test.ts"),
			mod:     scan,
			wantRun: []string{"lint", "security-scan", "web-test", "build", "e2e"},
			reasons: map[string][]string{
				"web-test": {"reads services/web/src/new.test.ts"},
				"api-test": {"inputs unchanged"},
			},
		},
		{
			name:    "a file deleted from a directory a job listed",
			changes: changes("D services/web/src/extra.ts"),
			mod:     scan,
			wantRun: []string{"lint", "security-scan", "web-test", "build", "e2e"},
			reasons: map[string][]string{"web-test": {"reads services/web/src/extra.ts"}},
		},
		{
			name:     "a new directory under a directory a job listed",
			changes:  changes("A services/billing/main.go"),
			headDirs: withDirs(sourceDirs(), "services/billing"),
			mod:      scan,
			wantRun:  []string{"lint", "security-scan", "build", "e2e"},
			reasons: map[string][]string{
				"lint":     {"reads services/billing/main.go"},
				"build":    {"reads services/billing/main.go"},
				"api-test": {"inputs unchanged"},
			},
		},
		{
			name:     "a new directory below one that a job listed",
			changes:  changes("A services/api/http/handler.go"),
			headDirs: withDirs(sourceDirs(), "services/api/http"),
			mod:      scan,
			// lint and build listed services/api, whose child http is new; security-scan listed only the root.
			wantRun: []string{"lint", "security-scan", "api-test", "build", "e2e"},
			reasons: map[string][]string{"api-test": {"reads services/api/http/handler.go"}},
		},
		{
			name:     "a directory that is gone",
			changes:  changes("D scripts/x.sh"),
			headDirs: without(sourceDirs(), "scripts"),
			mod:      scan,
			wantRun:  []string{"security-scan"},
			reasons: map[string][]string{
				"security-scan": {"mandatory job"},
			},
		},
		{
			name:    "a rename is a delete plus an add",
			changes: changes("D services/api/main.go", "A services/api/server.go"),
			mod:     scan,
			wantRun: []string{"lint", "security-scan", "api-test", "build", "e2e"},
			reasons: map[string][]string{"api-test": {"reads services/api/main.go", "reads services/api/server.go"}},
		},
		{
			name:    "ignored paths select nothing",
			changes: changes("M docs/index.md", "M README.md"),
			mod: func(o *planner.Options) {
				scan(o)
				o.IgnorePaths = []string{"docs/**", "*.md"}
			},
			wantRun: []string{"security-scan"},
		},
		{
			name:    "extra inputs select a job whose trace cannot see them, and its ancestors",
			changes: changes("M infra/terraform/main.tf"),
			mod: func(o *planner.Options) {
				scan(o)
				o.ExtraInputs = map[string][]string{"api-test": {"infra/**", "other/**"}}
			},
			wantRun: []string{"lint", "security-scan", "api-test"},
			reasons: map[string][]string{
				"api-test": {"extra input infra/** matches infra/terraform/main.tf"},
				"lint":     {"required by api-test"},
			},
		},
		{
			name:    "a job without a map entry always runs",
			changes: changes("M CHANGELOG.md"),
			mapMod:  func(m *impactmap.Map) { delete(m.Jobs, "docs") },
			wantRun: []string{"docs"},
			reasons: map[string][]string{"docs": {"no impact map entry"}},
		},
		{
			name:    "a job with an incomplete map entry always runs",
			changes: changes("M CHANGELOG.md"),
			mapMod:  func(m *impactmap.Map) { m.Jobs["web-test"].Complete = false },
			// web-test requires lint, so lint comes with it.
			wantRun: []string{"lint", "web-test"},
			reasons: map[string][]string{"web-test": {"the impact map entry is incomplete"}, "lint": {"required by web-test"}},
		},
		{
			name:    "many changed files are summarised",
			changes: changes("M services/api/main.go", "M services/web/src/index.ts", "M libs/shared/schema.json", "M Makefile", "M go.mod"),
			mod:     scan,
			wantRun: []string{"lint", "security-scan", "api-test", "web-test", "build", "e2e"},
			reasons: map[string][]string{"build": {"reads Makefile", "and 2 more changed files read"}},
		},
		{
			name:    "a new file deep under an input dir",
			changes: changes("A services/web/src/nested/pkg/footer.ts"),
			mod:     scan,
			wantRun: []string{"lint", "security-scan", "web-test", "build", "e2e"},
			reasons: map[string][]string{"web-test": {"reads services/web/src/nested/pkg/footer.ts"}},
		},
		{
			name:    "repository root among inputs covers any path",
			changes: changes("M CHANGELOG.md"),
			mapMod: func(m *impactmap.Map) {
				m.Jobs["api-test"].Dirs = append(m.Jobs["api-test"].Dirs, ".")
			},
			mod:     scan,
			wantRun: []string{"lint", "security-scan", "api-test"},
			reasons: map[string][]string{"api-test": {"reads CHANGELOG.md"}, "lint": {"required by api-test"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{changes: tc.changes, dirs: map[string]map[string]bool{"base1": sourceDirs(), "head1": tc.headDirs}}
			if tc.headDirs == nil {
				repo.dirs["head1"] = sourceDirs()
			}
			in := input(repo)
			if tc.mapMod != nil {
				tc.mapMod(in.Map)
			}
			o := opts()
			if tc.mod != nil {
				tc.mod(&o)
			}
			res := plan(t, in, o)
			if res.Report.Full {
				t.Fatalf("unexpected full run: %s", res.Report.FullReason)
			}
			if got := running(res); !sameSet(got, tc.wantRun) {
				t.Errorf("running = %v, want %v", got, tc.wantRun)
			}
			for job, subs := range tc.reasons {
				joined := strings.Join(reasons(t, res, job), " | ")
				for _, sub := range subs {
					if !strings.Contains(joined, sub) {
						t.Errorf("reasons of %s = %q, want it to contain %q", job, joined, sub)
					}
				}
			}
			// Select mode removes exactly the jobs that do not run, except what
			// cannot run anyway is gone too; the gate is added last.
			wantEmitted := append(slices.Clone(tc.wantRun), "smarter-gate")
			got := emittedJobs(t, res, "ci")
			if !sameSet(got, wantEmitted) {
				t.Errorf("emitted jobs = %v, want %v", got, wantEmitted)
			}
			if got[len(got)-1] != "smarter-gate" {
				t.Errorf("the gate is not last: %v", got)
			}
			// Order of the kept jobs is the workflow's own.
			order := []string{"lint", "security-scan", "api-test", "web-test", "build", "e2e", "docs", "hold", "deploy", "smarter-gate"}
			if !slices.IsSortedFunc(got, func(a, b string) int { return slices.Index(order, a) - slices.Index(order, b) }) {
				t.Errorf("emitted order = %v", got)
			}
			noNewExpressions(t, in.Config, res.Config)
		})
	}
}

func withDirs(base map[string]bool, add ...string) map[string]bool {
	out := map[string]bool{}
	for k, v := range base {
		out[k] = v
	}
	for _, d := range add {
		out[d] = true
	}
	return out
}

func without(base map[string]bool, drop ...string) map[string]bool {
	out := map[string]bool{}
	for k, v := range base {
		out[k] = v
	}
	for _, d := range drop {
		delete(out, d)
	}
	return out
}

func TestSelectionLeavesNoRequirementOnARemovedJob(t *testing.T) {
	repo := &fakeRepo{changes: changes("M docs/index.md"), dirs: map[string]map[string]bool{"base1": sourceDirs(), "head1": sourceDirs()}}
	res := plan(t, input(repo), opts())
	// Nothing in the plan may require a job that was removed. No job that stays
	// here requires one that does not run, so the planner edits no requirement.
	have := emittedJobs(t, res, "ci")
	for _, name := range have {
		got := emittedEntry(t, res, "ci", name).Requires
		for _, r := range got {
			if !slices.Contains(have, r) {
				t.Errorf("%s requires %s, which is gone", name, r)
			}
		}
		if name == "smarter-gate" {
			continue
		}
		if want := entryOf(t, monorepo, name).Requires; !slices.Equal(got, want) {
			t.Errorf("%s requires %v, want %v: the planner does not edit a requirement", name, got, want)
		}
	}
}

func TestSelectionLeavesEverythingElseUntouched(t *testing.T) {
	repo := &fakeRepo{changes: changes("M docs/index.md"), dirs: map[string]map[string]bool{"base1": sourceDirs(), "head1": sourceDirs()}}
	res := plan(t, input(repo), opts())
	out := string(res.Config)
	// The other workflow, the comments, the job definitions and their order.
	for _, want := range []string{
		"  # the pipeline under test\n  ci:\n",
		"  # untouched by the planner\n  nightly:\n    when: false\n    jobs:\n      - lint\n",
		"  deploy:\n    docker: [{image: \"cimg/base:2026.11\"}]\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// Job definitions are not pruned, even for removed entries.
	for _, job := range []string{"api-test", "web-test", "build", "e2e", "deploy"} {
		if !strings.Contains(out, "\n  "+job+":\n    docker:") {
			t.Errorf("the definition of %s is gone", job)
		}
	}
	if i, j := strings.Index(out, "  lint:\n"), strings.Index(out, "  security-scan:\n"); i < 0 || j < i {
		t.Error("job definitions changed order")
	}
}

func TestClosureOfApprovalsAndWorkspaces(t *testing.T) {
	const cfg = `version: 2.1
jobs:
  prep: {docker: [{image: x}], steps: [checkout, {persist_to_workspace: {root: ., paths: [p]}}]}
  mid: {docker: [{image: x}], steps: [checkout, {run: echo}]}
  use: {docker: [{image: x}], steps: [{attach_workspace: {at: .}}]}
  other: {docker: [{image: x}], steps: [checkout]}
  ship: {docker: [{image: x}], steps: [checkout]}
workflows:
  w:
    jobs:
      - prep
      - mid:
          requires: [prep]
      - use:
          requires: [mid]
      - other
      - gate-ok:
          type: approval
          requires: [use]
      - ship:
          requires: [gate-ok]
      - orb/job:
          requires: [prep]
`
	m := &impactmap.Map{Version: 1, Jobs: map[string]*impactmap.Job{
		"prep":    {Files: []string{"a"}, Complete: true},
		"mid":     {Files: []string{"b"}, Complete: true},
		"use":     {Files: []string{"c"}, Complete: true},
		"other":   {Files: []string{"d"}, Complete: true},
		"ship":    {Files: []string{"e"}, Complete: true},
		"orb/job": {Files: []string{"f"}, Complete: true},
	}}
	run := func(changed string) []string {
		in := planner.Input{Config: []byte(cfg), Map: m, Repo: &fakeRepo{changes: changes("M " + changed)}}
		o := opts()
		o.Workflow = "w"
		res := plan(t, in, o)
		return running(res)
	}
	tests := []struct {
		changed string
		want    []string
	}{
		// prep produces; use consumes (downstream through mid, which comes along
		// as an ancestor); the orb job counts as a consumer too.
		{"a", []string{"prep", "mid", "use", "orb/job"}},
		// use is selected: its ancestors mid and prep come with it. prep is a
		// producer, so the orb job (a consumer downstream of prep) follows.
		{"c", []string{"prep", "mid", "use", "orb/job"}},
		// mid is not a producer or consumer: its ancestor prep comes, and with
		// it everything that consumes prep's workspace.
		{"b", []string{"prep", "mid", "use", "orb/job"}},
		// ship is selected: the approval and everything up to prep come with it.
		{"e", []string{"prep", "mid", "use", "gate-ok", "ship", "orb/job"}},
		// other is independent.
		{"d", []string{"other"}},
	}
	for _, tc := range tests {
		t.Run("change "+tc.changed, func(t *testing.T) {
			if got := run(tc.changed); !sameSet(got, tc.want) {
				t.Fatalf("running = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatrixEntriesAlwaysRun(t *testing.T) {
	const cfg = `version: 2.1
jobs:
  t:
    parameters: {v: {type: string}}
    docker: [{image: x}]
    steps: [checkout]
  after:
    docker: [{image: x}]
    steps: [checkout]
workflows:
  w:
    jobs:
      - t:
          name: t-<< matrix.v >>
          matrix:
            alias: t
            parameters: {v: [a, b]}
      - after:
          requires: [t]
`
	m := &impactmap.Map{Version: 1, Jobs: map[string]*impactmap.Job{"after": {Files: []string{"x"}, Complete: true}}}
	res := plan(t, planner.Input{Config: []byte(cfg), Map: m, Repo: &fakeRepo{changes: changes("M unrelated")}}, func() planner.Options { o := opts(); o.Workflow = "w"; return o }())
	if res.Report.Full {
		t.Fatalf("full run: %s", res.Report.FullReason)
	}
	if got := running(res); !sameSet(got, []string{"t-<< matrix.v >>"}) {
		t.Fatalf("running = %v: the matrix entry runs and the job behind it is skipped", got)
	}
	if got := reasons(t, res, "t-<< matrix.v >>"); !slices.Contains(got, "matrix entry always runs") {
		t.Fatalf("reasons = %v", got)
	}
	// The matrix entry keeps its expression name in the output: it is a
	// literal copy of what the user wrote. The gate waits for it by its alias.
	if !strings.Contains(string(res.Config), "name: t-<< matrix.v >>") {
		t.Errorf("the matrix entry was not kept as is:\n%s", res.Config)
	}
	if got := res.Report.Gate.Requires; !slices.Equal(got, []string{"t"}) {
		t.Errorf("gate requires %v, want [t]", got)
	}
}
