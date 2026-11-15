package monorepo_test

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff/difftest"
	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
	"github.com/CircleCI-Labs/smarter-workflows/internal/report"
	"github.com/CircleCI-Labs/smarter-workflows/internal/tracescript"
)

var update = flag.Bool("update", false, "rewrite the golden files in scenarios/")

// allJobs are the entries of the example's `ci` workflow, in order.
var allJobs = []string{"lint", "security-scan", "api-test", "web-test", "build", "e2e", "docs", "hold-deploy", "deploy"}

// scenario is one kind of change, planned against the example and its map.
type scenario struct {
	name string
	// what says in a sentence what changes, for readers of the table.
	what string
	// change edits the checkout after the base commit; the head commit takes
	// the result. Scenarios on the default branch commit on main itself.
	change func(r *difftest.Repo)
	// branch is the branch of the head commit (default "feature").
	branch  string
	message string
	// noMap plans without an impact map.
	noMap bool
	// noMandatory leaves out --mandatory security-scan.
	noMandatory bool
	mode        planner.Mode
	checker     guard.Checker
	// wantFull is the start of the full-run reason ("" for a real plan).
	wantFull string
	// wantRun is the set of jobs the plan runs.
	wantRun []string
	// wantEmitted is the jobs of the emitted workflow, gate (and map job) last;
	// nil means wantRun, then the gate.
	wantEmitted []string
	// traced says whether the plan must carry the trace steps.
	traced bool
}

func scenarios() []scenario {
	return []scenario{
		{
			name:    "docs-only",
			what:    "a guide page changes",
			change:  func(r *difftest.Repo) { r.Write("docs/guide.md", "# Guide\n\nA new paragraph.\n") },
			wantRun: []string{"lint", "security-scan", "docs"},
		},
		{
			name: "one-service",
			what: "a file of the API service changes",
			change: func(r *difftest.Repo) {
				r.Write("services/api/handler.go", "package api\n\n// Handle answers requests, faster.\nfunc Handle() {}\n")
			},
			wantRun: []string{"lint", "security-scan", "api-test", "build", "e2e"},
		},
		{
			name:    "shared-library",
			what:    "the schema both services read changes",
			change:  func(r *difftest.Repo) { r.Write("libs/shared/schema.json", "{\"version\": 2}\n") },
			wantRun: []string{"lint", "security-scan", "api-test", "web-test", "build", "e2e"},
		},
		{
			name:    "added-file",
			what:    "a file is added to a directory that jobs listed",
			change:  func(r *difftest.Repo) { r.Write("services/web/src/footer.ts", "export const footer = 'bye';\n") },
			wantRun: []string{"lint", "security-scan", "web-test", "build", "e2e"},
		},
		{
			name:    "new-directory",
			what:    "a new service directory appears under services/",
			change:  func(r *difftest.Repo) { r.Write("services/billing/main.go", "package billing\n") },
			wantRun: []string{"lint", "security-scan", "build", "e2e"},
		},
		{
			name:    "deleted-directory",
			what:    "the only file of scripts/ is deleted, so the directory is gone",
			change:  func(r *difftest.Repo) { r.Remove("scripts") },
			wantRun: []string{"lint", "security-scan"},
		},
		{
			name:    "rename",
			what:    "a file of the API service is renamed",
			change:  func(r *difftest.Repo) { r.Git("mv", "services/api/handler.go", "services/api/routes.go") },
			wantRun: []string{"lint", "security-scan", "api-test", "build", "e2e"},
		},
		{
			name:     "circleci-change",
			what:     "the pipeline definition changes",
			change:   func(r *difftest.Repo) { r.Write(".circleci/continue.yml", "# touched\n"+exampleConfig()) },
			wantFull: "changed path .circleci/continue.yml matches the full-run path .circleci/**",
			// A full run keeps every entry; the deploy chain stays but cannot run on a branch.
			wantRun:     []string{"lint", "security-scan", "api-test", "web-test", "build", "e2e", "docs"},
			wantEmitted: append(slices.Clone(allJobs), "smarter-gate"),
		},
		{
			name:        "no-map",
			what:        "there is no impact map yet",
			change:      func(r *difftest.Repo) { r.Write("docs/guide.md", "# Guide\n\nA new paragraph.\n") },
			noMap:       true,
			wantFull:    "no usable impact map: there is no impact map at",
			wantRun:     []string{"lint", "security-scan", "api-test", "web-test", "build", "e2e", "docs"},
			wantEmitted: append(slices.Clone(allJobs), "smarter-gate"),
		},
		{
			name:        "mandatory-repaired",
			what:        "a guide page changes, security-scan is not declared mandatory, and the guardrails demand it",
			change:      func(r *difftest.Repo) { r.Write("docs/guide.md", "# Guide\n\nA new paragraph.\n") },
			noMandatory: true,
			checker:     &requireSecurityScan{},
			wantRun:     []string{"lint", "security-scan", "docs"},
		},
		{
			name:    "shadow-docs-only",
			what:    "a guide page changes, in shadow mode: the report says what select would do, every job stays",
			change:  func(r *difftest.Repo) { r.Write("docs/guide.md", "# Guide\n\nA new paragraph.\n") },
			mode:    planner.ModeShadow,
			wantRun: []string{"lint", "security-scan", "docs"},
			wantEmitted: []string{
				"lint", "security-scan", "api-test", "web-test", "build", "e2e", "docs", "hold-deploy", "deploy", "smarter-gate",
			},
		},
		{
			name:     "main-branch-trace",
			what:     "a merge to the default branch: a full run that traces every job and rebuilds the map",
			branch:   "main",
			change:   func(r *difftest.Repo) { r.Write("docs/guide.md", "# Guide\n\nA new paragraph.\n") },
			wantFull: "default branch main",
			wantRun:  allJobs,
			traced:   true,
			wantEmitted: []string{
				"lint", "security-scan", "api-test", "web-test", "build", "e2e", "docs", "hold-deploy", "deploy", "smarter-impact-map", "smarter-gate",
			},
		},
	}
}

// requireSecurityScan is the required-jobs rule of the guardrail bundle, in Go:
// every workflow must hold security-scan, and the violation carries the repair
// code the planner understands.
type requireSecurityScan struct{ calls int }

func (c *requireSecurityScan) Check(_ context.Context, cfg []byte) (guard.Result, error) {
	c.calls++
	parsed, err := config.Parse(cfg)
	if err != nil {
		return guard.Result{}, err
	}
	for _, name := range parsed.Workflows() {
		w, err := parsed.Workflow(name)
		if err != nil {
			return guard.Result{}, err
		}
		found := false
		for _, e := range w.Entries {
			found = found || e.Name == "security-scan"
		}
		if !found {
			return guard.Result{Status: guard.HardFail, Violations: []guard.Violation{{
				Policy: "org", Rule: "required_jobs", Hard: true,
				Message:     "workflow " + name + " must contain the job security-scan [smarter:require-job=security-scan]",
				RequireJobs: []string{"security-scan"},
			}}}, nil
		}
	}
	return guard.Result{Status: guard.Pass}, nil
}

func exampleConfig() string {
	b, err := os.ReadFile("continue.yml")
	if err != nil {
		panic(err)
	}
	return string(b)
}

// outcome is what one scenario produced.
type outcome struct {
	result   *planner.Result
	planJSON []byte
	planMD   string
	config   []byte
}

// stubScripts replaces the embedded trace scripts for the golden files, so
// that they do not change whenever the collector does.
func stubScripts(t *testing.T) {
	t.Helper()
	oldReset, oldScript := tracescript.Reset, tracescript.Script
	tracescript.Reset = "# stub: the post-checkout reset of internal/tracescript\ntrue\n"
	tracescript.Script = "# stub: the trace collector of internal/tracescript\ntrue\n"
	t.Cleanup(func() { tracescript.Reset, tracescript.Script = oldReset, oldScript })
}

// run plans one scenario in a fresh git repository.
func (sc scenario) run(t *testing.T) outcome {
	t.Helper()
	r := difftest.New(t)
	for path, content := range baseTree() {
		r.Write(path, content)
	}
	r.Write(".circleci/continue.yml", exampleConfig())
	baseSHA := r.Commit("base")

	branch := sc.branch
	if branch == "" {
		branch = "feature"
		r.Git("checkout", "-q", "-b", branch)
	}
	sc.change(r)
	message := sc.message
	if message == "" {
		message = "change"
	}
	headSHA := r.Commit(message)

	git := diff.Git{Dir: r.Dir}
	in := planner.Input{Config: []byte(exampleConfig()), Repo: git, CommitMessage: message}
	if sc.noMap {
		in.MapProblem = "there is no impact map at /tmp/smarter/impact-map.json.gz"
	} else {
		m, err := impactmap.Load("impact-map.json.gz")
		if err != nil {
			t.Fatal(err)
		}
		if branch != "main" {
			// The map was built from the commit this branch was cut from.
			m.Revision = baseSHA
		}
		in.Map = m
	}
	if sc.checker != nil {
		in.Checker = sc.checker
	}

	o := planner.DefaultOptions()
	o.Mode = planner.ModeSelect
	if sc.mode != "" {
		o.Mode = sc.mode
	}
	o.Branch = branch
	o.Head = headSHA
	if branch != "main" {
		o.Base = baseSHA // what `circleci-smarter base` finds for a branch cut from main
	} else {
		o.NoBaseReason = "default branch main"
	}
	if !sc.noMandatory {
		o.Mandatory = []string{"security-scan"}
	}
	o.Version = "dev-example"
	o.BinPath = "/usr/local/bin/circleci-smarter"

	res, err := planner.Run(context.Background(), in, o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The SHAs differ between machines only if git does; keep the goldens stable anyway.
	rep := *res.Report
	if rep.Base != "" {
		rep.Base = "<base>"
	}
	if rep.Head != "" {
		rep.Head = "<head>"
	}
	if rep.MapRevision == baseSHA {
		rep.MapRevision = "<base>"
	}
	js, err := rep.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return outcome{result: res, planJSON: js, planMD: rep.Markdown(), config: res.Config}
}

func TestScenarios(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			stubScripts(t)
			out := sc.run(t)
			rep := out.result.Report

			// The facts first, so that a golden file is never the only oracle.
			switch {
			case sc.wantFull == "" && rep.Full:
				t.Fatalf("a real plan was expected, got a full run: %s", rep.FullReason)
			case sc.wantFull != "" && (!rep.Full || !strings.HasPrefix(rep.FullReason, sc.wantFull)):
				t.Fatalf("full = %v, reason = %q, want a full run: %s", rep.Full, rep.FullReason, sc.wantFull)
			}
			if got := running(rep); !sameSet(got, sc.wantRun) {
				t.Errorf("running = %v, want %v", got, sc.wantRun)
			}
			wantEmitted := sc.wantEmitted
			if wantEmitted == nil {
				for _, j := range allJobs { // keep the workflow's own order
					if slices.Contains(sc.wantRun, j) {
						wantEmitted = append(wantEmitted, j)
					}
				}
				wantEmitted = append(wantEmitted, "smarter-gate")
			}
			if got := emittedJobs(t, out.config); !slices.Equal(got, wantEmitted) {
				t.Errorf("emitted jobs = %v, want %v", got, wantEmitted)
			}
			if (rep.Trace != nil) != sc.traced {
				t.Errorf("trace = %+v, want traced = %v", rep.Trace, sc.traced)
			}
			if rep.Gate == nil || rep.Gate.Name != "smarter-gate" {
				t.Errorf("gate = %+v", rep.Gate)
			}
			if rep.Guardrails.Status != "SKIPPED" && rep.Guardrails.Status != "PASS" {
				t.Errorf("guardrails = %+v", rep.Guardrails)
			}
			if sc.checker != nil && !slices.Equal(rep.Guardrails.Repairs, []string{"require-job=security-scan"}) {
				t.Errorf("repairs = %v", rep.Guardrails.Repairs)
			}
			// The planner adds no pipeline parameters and no expressions.
			if strings.Contains(string(out.config), "<<") && !strings.Contains(exampleConfig(), "<<") {
				t.Errorf("the plan added an expression:\n%s", out.config)
			}
			if _, err := config.Parse(out.config); err != nil {
				t.Fatalf("the emitted config does not parse: %v", err)
			}

			dir := filepath.Join("scenarios", sc.name)
			golden(t, filepath.Join(dir, "plan.json"), out.planJSON)
			golden(t, filepath.Join(dir, "plan.md"), []byte(out.planMD))
			golden(t, filepath.Join(dir, "continue.yml"), out.config)
		})
	}
}

// golden compares got with a golden file, or rewrites it under -update.
func golden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (create it with -update)", err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s differs from the golden file (refresh it with -update after checking the change):\n%s", path, firstDifference(string(want), string(got)))
	}
}

// firstDifference shows where two texts first differ.
func firstDifference(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(w), len(g)); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl || i >= len(w) || i >= len(g) {
			return fmt.Sprintf("line %d:\n  want: %q\n  got:  %q", i+1, wl, gl)
		}
	}
	return "(no difference found)"
}

func running(rep *report.Plan) []string {
	var out []string
	for _, j := range rep.Jobs {
		if j.Run {
			out = append(out, j.Name)
		}
	}
	return out
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func emittedJobs(t *testing.T, cfg []byte) []string {
	t.Helper()
	c, err := config.Parse(cfg)
	if err != nil {
		t.Fatal(err)
	}
	w, err := c.Workflow("ci")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range w.Entries {
		out = append(out, e.Name)
	}
	return out
}
