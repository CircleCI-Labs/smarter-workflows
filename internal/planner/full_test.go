package planner_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

func TestFullRunReasons(t *testing.T) {
	dirs := map[string]map[string]bool{"base1": sourceDirs(), "head1": sourceDirs()}
	tests := []struct {
		name string
		mod  func(o *planner.Options, in *planner.Input, repo *fakeRepo)
		want string
	}{
		{"forced", func(o *planner.Options, _ *planner.Input, _ *fakeRepo) { o.ForceFull = true }, "forced by --force-full"},
		{"default branch", func(o *planner.Options, _ *planner.Input, _ *fakeRepo) { o.Branch = "main" }, "default branch main"},
		{"tag", func(o *planner.Options, _ *planner.Input, _ *fakeRepo) { o.Branch, o.Tag = "", "v1.2.3" }, "tag v1.2.3"},
		{"schedule", func(o *planner.Options, _ *planner.Input, _ *fakeRepo) { o.TriggerType = "schedule" }, "scheduled pipeline"},
		{"scheduled_pipeline", func(o *planner.Options, _ *planner.Input, _ *fakeRepo) { o.TriggerType = "scheduled_pipeline" }, "scheduled pipeline"},
		{"commit tag", func(_ *planner.Options, in *planner.Input, _ *fakeRepo) { in.CommitMessage = "fix it [Smarter Full]" }, "the head commit asks for a full run"},
		{"commit trailer", func(_ *planner.Options, in *planner.Input, _ *fakeRepo) {
			in.CommitMessage = "fix it\n\nSmarter-Full: true"
		}, "the head commit asks for a full run"},
		{"no base", func(o *planner.Options, _ *planner.Input, _ *fakeRepo) {
			o.Base, o.NoBaseReason = "", "origin/main is missing"
		}, "no diff base: origin/main is missing"},
		{"no base and no reason", func(o *planner.Options, _ *planner.Input, _ *fakeRepo) { o.Base = "" }, "no diff base"},
		{"no map", func(_ *planner.Options, in *planner.Input, _ *fakeRepo) {
			in.Map, in.MapProblem = nil, "no impact map at /x"
		}, "no usable impact map: no impact map at /x"},
		{"diff fails", func(_ *planner.Options, _ *planner.Input, r *fakeRepo) { r.changesErr = errBoom }, "the changed files could not be listed: boom"},
		{"no head commit", func(o *planner.Options, _ *planner.Input, _ *fakeRepo) { o.Head = "" }, "the changed files could not be listed: there is no head commit"},
		{"pipeline definition changed", func(_ *planner.Options, _ *planner.Input, r *fakeRepo) {
			r.changes = changes("M .circleci/continue.yml")
		}, "changed path .circleci/continue.yml matches the full-run path .circleci/**"},
		{"a deeper pipeline file changed", func(_ *planner.Options, _ *planner.Input, r *fakeRepo) {
			r.changes = changes("A .circleci/scripts/x.sh")
		}, "matches the full-run path .circleci/**"},
		{"the old path of a rename counts", func(_ *planner.Options, _ *planner.Input, r *fakeRepo) {
			r.changes = changes("D .circleci/old.yml", "A scripts/old.yml")
		}, "changed path .circleci/old.yml"},
		{"an ignored path still counts for the full-run paths", func(o *planner.Options, _ *planner.Input, r *fakeRepo) {
			o.FullRunPaths, o.IgnorePaths = []string{"docs/**"}, []string{"docs/**"}
			r.changes = changes("M docs/index.md")
		}, "matches the full-run path docs/**"},
		{"custom full-run paths with braces", func(o *planner.Options, _ *planner.Input, r *fakeRepo) {
			o.FullRunPaths = planner.SplitList("**/*.{lock,sum},.circleci/**")
			r.changes = changes("M services/api/go.sum")
		}, "matches the full-run path **/*.{lock,sum}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{changes: changes("M docs/index.md"), dirs: dirs}
			in := input(repo)
			o := opts()
			tc.mod(&o, &in, repo)
			res := plan(t, in, o)
			if !res.Report.Full || !strings.Contains(res.Report.FullReason, tc.want) {
				t.Fatalf("full = %v, reason = %q, want a full run: %q", res.Report.Full, res.Report.FullReason, tc.want)
			}
			// Every entry stays, plus the gate.
			got := emittedJobs(t, res, "ci")
			want := []string{"lint", "security-scan", "api-test", "web-test", "build", "e2e", "docs", "hold", "deploy", "smarter-gate"}
			if !slices.Equal(got, want) {
				t.Errorf("emitted jobs = %v, want %v", got, want)
			}
			for _, j := range res.Report.Jobs {
				if j.Reasons[0] != "full run" && !strings.HasPrefix(j.Reasons[0], "does not run") {
					t.Errorf("%s: reasons %v", j.Name, j.Reasons)
				}
			}
			if res.GuardrailsFailed || res.Report.Guardrails.Status != "SKIPPED" {
				t.Errorf("guardrails = %+v", res.Report.Guardrails)
			}
		})
	}
}

func TestFullRunReportsWhatCannotRun(t *testing.T) {
	// On a feature branch, the deploy chain is filtered to main: a full run
	// does not run it, and says so.
	res := plan(t, input(&fakeRepo{}), func() planner.Options { o := opts(); o.ForceFull = true; return o }())
	if got := skipped(res); !sameSet(got, []string{"hold", "deploy"}) {
		t.Fatalf("skipped = %v", got)
	}
	if got := reasons(t, res, "deploy"); !strings.Contains(got[0], "its filters leave out branch feature") || got[1] != "full run" {
		t.Fatalf("reasons = %v", got)
	}
	// On main the same jobs run (behind the approval).
	o := opts()
	o.Branch = "main"
	res = plan(t, input(&fakeRepo{}), o)
	if got := skipped(res); len(got) != 0 {
		t.Fatalf("skipped on main = %v", got)
	}
}

func TestFullRunWorkflowConditions(t *testing.T) {
	cfg := func(when, name string) []byte {
		return []byte(`version: 2.1
parameters:
  go: {type: boolean, default: true}
jobs:
  a: {docker: [{image: x}], steps: [checkout]}
workflows:
  w:
` + when + `    jobs:
      - a` + name + `
`)
	}
	m := monorepoMap()
	o := opts()
	o.Workflow = "w"
	tests := []struct {
		name   string
		config []byte
		want   string
	}{
		{"parameter in when", cfg("    when: << pipeline.parameters.go >>\n", ""), "cannot be evaluated"},
		{"parameter in unless", cfg("    unless: << pipeline.parameters.skip >>\n", ""), "cannot be evaluated"},
		{"a job name that is an expression", cfg("", ":\n          name: a-<< pipeline.parameters.go >>"), `job "a-<< pipeline.parameters.go >>" has a name or requires that is an expression`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := plan(t, planner.Input{Config: tc.config, Map: m, Repo: &fakeRepo{changes: changes("M x")}}, o)
			if !res.Report.Full || !strings.Contains(res.Report.FullReason, tc.want) {
				t.Fatalf("full = %v, reason = %q, want %q", res.Report.Full, res.Report.FullReason, tc.want)
			}
		})
	}
	// A literal boolean is fine.
	res := plan(t, planner.Input{Config: cfg("    when: true\n", ""), Map: m, Repo: &fakeRepo{changes: changes("M x")}}, o)
	if res.Report.Full {
		t.Fatalf("a literal when must not force a full run: %s", res.Report.FullReason)
	}
	// A branch expression is enough without --parameters.
	res = plan(t, planner.Input{Config: cfg("    unless: pipeline.git.branch == \"x\"\n", ""), Map: m, Repo: &fakeRepo{changes: changes("M x")}}, o)
	if res.Report.Full {
		t.Fatalf("evaluated unless must not force a full run: %s", res.Report.FullReason)
	}
}

func TestPlanWorkflowWhenParameters(t *testing.T) {
	cfg := []byte(`version: 2.1
parameters:
  run: {type: boolean, default: true}
jobs:
  a: {docker: [{image: x}], steps: [checkout]}
  b: {docker: [{image: x}], steps: [checkout]}
workflows:
  w:
    when: << pipeline.parameters.run >>
    jobs:
      - a
      - b:
          requires: [a]
`)
	m := monorepoMap()
	o := opts()
	o.Workflow = "w"
	repo := &fakeRepo{changes: changes("M pkg/x.go")}
	mapForA := func(files ...string) *impactmap.Map {
		m2 := monorepoMap()
		m2.Jobs["a"] = &impactmap.Job{Files: files, Complete: true}
		return m2
	}
	m = mapForA("pkg/x.go")

	t.Run("unknown without parameters", func(t *testing.T) {
		res := plan(t, planner.Input{Config: cfg, Map: m, Repo: repo}, o)
		if !res.Report.Full {
			t.Fatal("want full run")
		}
	})

	t.Run("pruned with run true", func(t *testing.T) {
		o2 := o
		o2.Parameters = map[string]any{"run": true}
		res := plan(t, planner.Input{Config: cfg, Map: m, Repo: repo}, o2)
		if res.Report.Full || !res.Report.WorkflowRuns {
			t.Fatalf("full=%v runs=%v", res.Report.Full, res.Report.WorkflowRuns)
		}
		if got := running(res); !slices.Equal(got, []string{"a", "b"}) {
			t.Fatalf("running %v", got)
		}
	})

	t.Run("skipped with run false", func(t *testing.T) {
		o2 := o
		o2.Parameters = map[string]any{"run": false}
		res := plan(t, planner.Input{Config: cfg, Map: m, Repo: repo}, o2)
		if res.Report.Full || res.Report.WorkflowRuns {
			t.Fatalf("full=%v runs=%v reason=%q", res.Report.Full, res.Report.WorkflowRuns, res.Report.WorkflowSkipReason)
		}
		if res.Report.Gate != nil {
			t.Fatal("gate added when workflow skipped")
		}
		if string(res.Config) != string(cfg) {
			t.Fatal("config was modified")
		}
	})
}

func TestRunErrors(t *testing.T) {
	ctx := context.Background()
	in := input(&fakeRepo{})
	tests := []struct {
		name    string
		in      planner.Input
		mod     func(*planner.Options)
		wantErr string
		usage   bool
	}{
		{"unparseable config", planner.Input{Config: []byte("a: [")}, nil, "parse config", false},
		{"empty config", planner.Input{Config: nil}, nil, "empty", false},
		{"two workflows and none named", in, func(o *planner.Options) { o.Workflow = "" }, "2 workflows (ci, nightly)", true},
		{"an unknown workflow", in, func(o *planner.Options) { o.Workflow = "nope" }, `no workflow "nope" (it has: ci, nightly)`, true},
		{"no workflows", planner.Input{Config: []byte("version: 2.1\n")}, nil, "no workflow", true},
		{"a bad mode", in, func(o *planner.Options) { o.Mode = "fast" }, "mode must be", true},
		{"a bad glob", in, func(o *planner.Options) { o.FullRunPaths = []string{"["} }, `bad glob "["`, true},
		{"a bad ignore glob", in, func(o *planner.Options) { o.IgnorePaths = []string{"a/{b"} }, "ignore paths", true},
		{"a bad extra input glob", in, func(o *planner.Options) { o.ExtraInputs = map[string][]string{"x": {"["}} }, "extra inputs", true},
		{"a bad gate name", in, func(o *planner.Options) { o.GateJob = "has space" }, "gate job name", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := opts()
			if tc.mod != nil {
				tc.mod(&o)
			}
			_, err := planner.Run(ctx, tc.in, o)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
			var ue *planner.UsageError
			if got := errors.As(err, &ue); got != tc.usage {
				t.Fatalf("usage error = %v, want %v", got, tc.usage)
			}
		})
	}
}

func TestOneWorkflowNeedsNoName(t *testing.T) {
	cfg := `version: 2.1
jobs:
  a: {docker: [{image: x}], steps: [checkout]}
workflows:
  only:
    jobs: [a]
`
	o := opts()
	o.Workflow = ""
	res := plan(t, planner.Input{Config: []byte(cfg), Map: monorepoMap(), Repo: &fakeRepo{changes: changes("M x")}}, o)
	if res.Report.Workflow != "only" {
		t.Fatalf("workflow = %q", res.Report.Workflow)
	}
}

func TestSplitListAndExtraInputs(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{" , ,", nil},
		{"a,b", []string{"a", "b"}},
		{" a/** , b/** ", []string{"a/**", "b/**"}},
		{"**/*.{go,mod},docs/**", []string{"**/*.{go,mod}", "docs/**"}},
		{"{a,{b,c}},d", []string{"{a,{b,c}}", "d"}},
		{"a},b", []string{"a}", "b"}},
	}
	for _, tc := range tests {
		if got := planner.SplitList(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("SplitList(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	got, err := planner.ParseExtraInputs("api-test=infra/**|libs/**/*.{json,yaml}, web=web/**,api-test=extra/**")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got["api-test"], []string{"infra/**", "libs/**/*.{json,yaml}", "extra/**"}) || !slices.Equal(got["web"], []string{"web/**"}) || len(got) != 2 {
		t.Fatalf("ParseExtraInputs = %v", got)
	}
	if m, err := planner.ParseExtraInputs(""); err != nil || len(m) != 0 {
		t.Fatalf("empty: %v, %v", m, err)
	}
	for _, bad := range []string{"nojob", "=glob", "job=", "job= | "} {
		if _, err := planner.ParseExtraInputs(bad); err == nil {
			t.Errorf("ParseExtraInputs(%q): want an error", bad)
		}
	}
}
