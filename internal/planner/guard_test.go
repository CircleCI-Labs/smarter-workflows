package planner_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

// fakeChecker stands in for the policy bundle.
type fakeChecker struct {
	check func(call int, jobs []string) (guard.Result, error)
	calls int
	seen  [][]string // the jobs of the workflow in each config it was shown
}

func (f *fakeChecker) Check(ctx context.Context, cfg []byte) (guard.Result, error) {
	f.calls++
	c, err := config.Parse(cfg)
	if err != nil {
		return guard.Result{}, err
	}
	var jobs []string
	for _, name := range c.Workflows() {
		if name != "ci" {
			continue
		}
		w, _ := c.Workflow(name)
		for _, e := range w.Entries {
			jobs = append(jobs, e.Name)
		}
	}
	f.seen = append(f.seen, jobs)
	return f.check(f.calls, jobs)
}

// requireJobs is the required-jobs rule of the guardrail bundle: every job in
// list must be in the workflow. It reports the first one missing, with the
// repair code.
func requireJobs(list ...string) func(int, []string) (guard.Result, error) {
	return func(_ int, jobs []string) (guard.Result, error) {
		for _, name := range list {
			if !slices.Contains(jobs, name) {
				return guard.Result{Status: guard.HardFail, Violations: []guard.Violation{{
					Policy: "org", Rule: "required_jobs", Hard: true,
					Message:     fmt.Sprintf("workflow ci lacks the job %s [smarter:require-job=%s]", name, name),
					RequireJobs: []string{name},
				}}}, nil
			}
		}
		return guard.Result{Status: guard.Pass}, nil
	}
}

func guardInput(c guard.Checker) planner.Input {
	in := input(&fakeRepo{changes: changes("M docs/index.md"), dirs: map[string]map[string]bool{"base1": sourceDirs(), "head1": sourceDirs()}})
	in.Checker = c
	return in
}

func TestGuardrailsPass(t *testing.T) {
	c := &fakeChecker{check: func(int, []string) (guard.Result, error) { return guard.Result{Status: guard.Pass}, nil }}
	o := opts()
	o.Mandatory = []string{"security-scan"}
	res := plan(t, guardInput(c), o)
	g := res.Report.Guardrails
	if g.Status != "PASS" || len(g.Repairs) != 0 || len(g.Violations) != 0 || res.GuardrailsFailed || c.calls != 1 {
		t.Fatalf("guardrails = %+v, failed = %v, calls = %d", g, res.GuardrailsFailed, c.calls)
	}
	// The checker saw the planned config, gate included.
	if !slices.Contains(c.seen[0], "smarter-gate") || slices.Contains(c.seen[0], "api-test") {
		t.Fatalf("the checker saw %v", c.seen[0])
	}
}

func TestGuardrailsAreSkippedWithoutABundle(t *testing.T) {
	res := plan(t, guardInput(nil), opts())
	if res.Report.Guardrails.Status != "SKIPPED" {
		t.Fatalf("guardrails = %+v", res.Report.Guardrails)
	}
}

func TestGuardrailsSoftViolationsAreWarnings(t *testing.T) {
	c := &fakeChecker{check: func(int, []string) (guard.Result, error) {
		return guard.Result{Status: guard.SoftFail, Violations: []guard.Violation{{Policy: "org", Rule: "resource_class", Message: "large\nis not allowed"}}}, nil
	}}
	res := plan(t, guardInput(c), opts())
	g := res.Report.Guardrails
	if g.Status != "SOFT_FAIL" || res.GuardrailsFailed || len(g.Violations) != 1 || g.Violations[0].Hard || g.Violations[0].Message != "large is not allowed" {
		t.Fatalf("guardrails = %+v", g)
	}
	if !slices.Contains(res.Report.Warnings, "guardrails (soft): resource_class: large is not allowed") {
		t.Fatalf("warnings = %v", res.Report.Warnings)
	}
}

func TestGuardrailsRepairAMissingMandatoryJob(t *testing.T) {
	c := &fakeChecker{check: requireJobs("security-scan")}
	res := plan(t, guardInput(c), opts()) // security-scan is not mandatory here, and nothing reads docs/index.md in it
	if res.Report.Full {
		t.Fatalf("the repair must not turn the plan into a full run: %s", res.Report.FullReason)
	}
	g := res.Report.Guardrails
	if g.Status != "PASS" || !slices.Equal(g.Repairs, []string{"require-job=security-scan"}) || res.GuardrailsFailed {
		t.Fatalf("guardrails = %+v", g)
	}
	if c.calls != 2 {
		t.Fatalf("the checker was called %d times, want 2 (before and after the repair)", c.calls)
	}
	if slices.Contains(c.seen[0], "security-scan") || !slices.Contains(c.seen[1], "security-scan") {
		t.Fatalf("seen = %v", c.seen)
	}
	if got := running(res); !sameSet(got, []string{"lint", "docs", "security-scan"}) {
		t.Fatalf("running = %v", got)
	}
	if got := reasons(t, res, "security-scan"); len(got) != 1 || got[0] != "required by the guardrails ([smarter:require-job=security-scan])" {
		t.Fatalf("reasons = %v", got)
	}
	if !slices.Contains(emittedJobs(t, res, "ci"), "security-scan") {
		t.Fatalf("the repaired config lacks the job")
	}
}

func TestGuardrailsRepairsTakeSeveralRounds(t *testing.T) {
	c := &fakeChecker{check: requireJobs("security-scan", "api-test", "web-test")}
	res := plan(t, guardInput(c), opts())
	g := res.Report.Guardrails
	if res.Report.Full || g.Status != "PASS" || !slices.Equal(g.Repairs, []string{"require-job=security-scan", "require-job=api-test", "require-job=web-test"}) || c.calls != 4 {
		t.Fatalf("full = %v, guardrails = %+v, calls = %d", res.Report.Full, g, c.calls)
	}
}

func TestGuardrailsGiveUpRepairingAfterThreeRounds(t *testing.T) {
	c := &fakeChecker{check: requireJobs("security-scan", "api-test", "web-test", "build", "e2e")}
	res := plan(t, guardInput(c), opts())
	g := res.Report.Guardrails
	// Three repairs, a fourth is asked for: the planner plans a full run, which
	// has every job, and checks it again.
	if !res.Report.Full || !strings.Contains(res.Report.FullReason, "cannot be repaired") {
		t.Fatalf("full = %v, reason = %q", res.Report.Full, res.Report.FullReason)
	}
	if g.Status != "PASS" || len(g.Repairs) != 3 || c.calls != 5 || res.GuardrailsFailed {
		t.Fatalf("guardrails = %+v, calls = %d", g, c.calls)
	}
	if !slices.Contains(c.seen[4], "build") || !slices.Contains(c.seen[4], "docs") {
		t.Fatalf("the last config checked is not the full plan: %v", c.seen[4])
	}
}

func TestGuardrailsUnrepairableViolationMeansAFullPlan(t *testing.T) {
	c := &fakeChecker{check: func(call int, jobs []string) (guard.Result, error) {
		// A rule the planner has no repair code for: it holds when every job is present.
		if !slices.Contains(jobs, "build") {
			return guard.Result{Status: guard.HardFail, Violations: []guard.Violation{{Policy: "org", Rule: "x", Message: "build must run", Hard: true}}}, nil
		}
		return guard.Result{Status: guard.Pass}, nil
	}}
	res := plan(t, guardInput(c), opts())
	if !res.Report.Full || !strings.Contains(res.Report.FullReason, "build must run") || res.Report.Guardrails.Status != "PASS" || res.GuardrailsFailed {
		t.Fatalf("full = %v, reason = %q, guardrails = %+v", res.Report.Full, res.Report.FullReason, res.Report.Guardrails)
	}
	if c.calls != 2 || len(res.Report.Guardrails.Repairs) != 0 {
		t.Fatalf("calls = %d, repairs = %v", c.calls, res.Report.Guardrails.Repairs)
	}
}

func TestGuardrailsRepairingAJobThatDoesNotExistGoesToAFullPlanAndFails(t *testing.T) {
	c := &fakeChecker{check: requireJobs("ghost")}
	res := plan(t, guardInput(c), opts())
	g := res.Report.Guardrails
	if !res.GuardrailsFailed || g.Status != "HARD_FAIL" || !res.Report.Full {
		t.Fatalf("failed = %v, full = %v, guardrails = %+v", res.GuardrailsFailed, res.Report.Full, g)
	}
	if len(g.Violations) != 1 || !g.Violations[0].Hard || g.Violations[0].Rule != "required_jobs" {
		t.Fatalf("violations = %+v", g.Violations)
	}
	// One repair was tried (the job is forced, but is nowhere to be found), then the full plan.
	if c.calls != 3 || !slices.Equal(g.Repairs, []string{"require-job=ghost"}) {
		t.Fatalf("calls = %d, repairs = %v", c.calls, g.Repairs)
	}
	// The report is written even though the pipeline must not go on.
	if len(res.Report.Jobs) == 0 || res.Config == nil {
		t.Fatal("the failed plan has no report")
	}
}

func TestGuardrailsAFullRunThatFailsIsNotRepaired(t *testing.T) {
	c := &fakeChecker{check: requireJobs("ghost")}
	o := opts()
	o.Branch = "main"
	res := plan(t, guardInput(c), o)
	if !res.GuardrailsFailed || c.calls != 1 || len(res.Report.Guardrails.Repairs) != 0 {
		t.Fatalf("failed = %v, calls = %d, repairs = %v", res.GuardrailsFailed, c.calls, res.Report.Guardrails.Repairs)
	}
}

func TestGuardrailsAHardStatusWithoutAHardViolationStillFails(t *testing.T) {
	c := &fakeChecker{check: func(int, []string) (guard.Result, error) { return guard.Result{Status: guard.HardFail}, nil }}
	o := opts()
	o.ForceFull = true
	res := plan(t, guardInput(c), o)
	if !res.GuardrailsFailed || res.Report.Guardrails.Status != "HARD_FAIL" {
		t.Fatalf("failed = %v, guardrails = %+v", res.GuardrailsFailed, res.Report.Guardrails)
	}
}

func TestGuardrailsACheckThatBreaksFallsBackToAFullPlan(t *testing.T) {
	for name, check := range map[string]func(int, []string) (guard.Result, error){
		"an error":        func(int, []string) (guard.Result, error) { return guard.Result{}, errBoom },
		"an ERROR status": func(int, []string) (guard.Result, error) { return guard.Result{Status: guard.Error}, nil },
	} {
		t.Run(name, func(t *testing.T) {
			c := &fakeChecker{check: check}
			res := plan(t, guardInput(c), opts())
			if !res.Report.Full || res.GuardrailsFailed || res.Report.Guardrails.Status != "ERROR" {
				t.Fatalf("full = %v, failed = %v, guardrails = %+v", res.Report.Full, res.GuardrailsFailed, res.Report.Guardrails)
			}
			if !strings.Contains(res.Report.FullReason, "could not be checked") || len(res.Report.Warnings) == 0 {
				t.Fatalf("reason = %q, warnings = %v", res.Report.FullReason, res.Report.Warnings)
			}
			if c.calls != 1 {
				t.Fatalf("calls = %d: a broken check is not retried", c.calls)
			}
		})
	}
}

func TestGuardrailsBundleThatCannotBeUsed(t *testing.T) {
	in := guardInput(nil)
	in.GuardrailsProblem = "policy support is not built into this binary"
	res := plan(t, in, opts())
	if !res.Report.Full || res.GuardrailsFailed || res.Report.Guardrails.Status != "ERROR" {
		t.Fatalf("full = %v, failed = %v, guardrails = %+v", res.Report.Full, res.GuardrailsFailed, res.Report.Guardrails)
	}
	if !strings.Contains(res.Report.FullReason, "policy support is not built into this binary") {
		t.Fatalf("reason = %q", res.Report.FullReason)
	}
	if !strings.Contains(strings.Join(res.Report.Warnings, "\n"), "planned a full run without checking it") {
		t.Fatalf("warnings = %v", res.Report.Warnings)
	}
}

func TestGuardrailsCheckTheTracedConfigToo(t *testing.T) {
	c := &fakeChecker{check: func(int, []string) (guard.Result, error) { return guard.Result{Status: guard.Pass}, nil }}
	in := guardInput(c)
	o := opts()
	o.Branch = "main"
	plan(t, in, o)
	if !slices.Contains(c.seen[0], "smarter-impact-map") {
		t.Fatalf("the checker saw %v: the map job must be checked", c.seen[0])
	}
}
