package planner_test

// Robustness cases: unusual CircleCI configs, planned against temp git
// repositories and checked by rbInvariants. A test that fails here is a bug for
// the planner to fix; its comment says what the planner should do. A test that
// passes is a regression test. The configs are in testdata/robustness.

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff/difftest"
	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
	"github.com/CircleCI-Labs/smarter-workflows/internal/policy"
)

// The steps the trace adds, as rbSteps names them.
const (
	rbCollect = "run:smarter: collect trace"
	rbPersist = "persist_to_workspace"
	rbReset   = "run:smarter: reset atime"
)

// ---- fixtures, repositories and plans ----

// rbFixture reads testdata/robustness/<name>.
func rbFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "robustness", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// rbEdit is a change that writes one file.
func rbEdit(path, content string) func(r *difftest.Repo) {
	return func(r *difftest.Repo) { r.Write(path, content) }
}

// rbRepo is a temp repository: base is the commit before the change, head the
// commit with it.
type rbRepo struct {
	git        diff.Git
	base, head string
}

// rbChange commits base, makes the change on branch and commits it. On "main"
// the change lands on the default branch itself.
func rbChange(t *testing.T, base map[string]string, branch string, change func(r *difftest.Repo)) rbRepo {
	t.Helper()
	r := difftest.New(t)
	for path, content := range base {
		r.Write(path, content)
	}
	baseSHA := r.Commit("base")
	if branch != "main" {
		r.Git("checkout", "-q", "-b", branch)
	}
	if change != nil {
		change(r)
	}
	return rbRepo{git: diff.Git{Dir: r.Dir}, base: baseSHA, head: r.Commit("change")}
}

// rbReads is what one job read in a traced run: the files it read and the
// directories it listed. incomplete marks a trace the planner cannot trust.
type rbReads struct {
	files, dirs []string
	incomplete  bool
}

// rbMap is the impact map of a traced run at rev. A job missing from jobs has
// no entry.
func rbMap(rev string, jobs map[string]rbReads) *impactmap.Map {
	m := &impactmap.Map{
		Version: 1, Revision: rev, CreatedAt: "2026-11-12T18:30:00Z", Method: "atime",
		Jobs: map[string]*impactmap.Job{},
	}
	for name, r := range jobs {
		m.Jobs[name] = &impactmap.Job{Files: r.files, Dirs: r.dirs, Complete: !r.incomplete, Nodes: 1}
	}
	return m
}

// rbOpts is select-mode planning of workflow wf on branch. The default branch
// gets a full run.
func rbOpts(wf, branch string, rr rbRepo) planner.Options {
	o := planner.DefaultOptions()
	o.Mode = planner.ModeSelect
	o.Workflow = wf
	o.Branch = branch
	o.Head = rr.head
	if branch == "main" {
		o.NoBaseReason = "default branch main"
	} else {
		o.Base = rr.base
	}
	o.Version = "dev-robust"
	o.BinPath = "/usr/local/bin/circleci-smarter"
	return o
}

// rbWant is the plan a case expects: whether it is a full run, the jobs the
// report runs, the jobs of the planned workflow in the output, and the gate's
// requires.
type rbWant struct {
	full            bool
	workflowSkipped bool
	running         []string
	emitted         []string
	gate            []string
}

// rbCase is one planned change.
type rbCase struct {
	cfg        []byte
	base       map[string]string
	branch     string // "feature" unless set; "main" is a full run
	change     func(r *difftest.Repo)
	reads      map[string]rbReads
	mandatory  []string
	parameters map[string]any
	traceOrb   bool          // --trace-orb-jobs
	checker    guard.Checker // the policy bundle, or nil
	wf         string        // "ci" unless set
	want       rbWant
}

// rbRun plans one case, checks the plan against want, and checks the
// invariants of the emitted config. It returns the result.
func rbRun(t *testing.T, c rbCase) *planner.Result {
	t.Helper()
	wf, branch := c.wf, c.branch
	if wf == "" {
		wf = "ci"
	}
	if branch == "" {
		branch = "feature"
	}
	rr := rbChange(t, c.base, branch, c.change)
	o := rbOpts(wf, branch, rr)
	o.Mandatory = c.mandatory
	o.Parameters = c.parameters
	o.TraceOrbJobs = c.traceOrb
	in := planner.Input{Config: c.cfg, Map: rbMap(rr.base, c.reads), Repo: rr.git, CommitMessage: "change"}
	in.Checker = c.checker
	res, err := planner.Run(context.Background(), in, o)
	if err != nil {
		t.Fatalf("planner.Run: %v", err)
	}
	rbCheckPlan(t, res, wf, c.want)
	if !c.want.workflowSkipped {
		rbInvariants(t, rbCheck{wf: wf, branch: branch, in: c.cfg, res: res, checker: c.checker})
	}
	return res
}

// rbCheckPlan compares the plan with what the case expects.
func rbCheckPlan(t *testing.T, res *planner.Result, wf string, want rbWant) {
	t.Helper()
	if res.Report.Full != want.full {
		t.Errorf("full run = %v (%s), want %v", res.Report.Full, res.Report.FullReason, want.full)
	}
	if want.workflowSkipped {
		if res.Report.WorkflowRuns {
			t.Errorf("workflow_runs = true, want skipped")
		}
		if res.Report.Gate != nil {
			t.Errorf("gate present when workflow is skipped")
		}
		return
	}
	if got := running(res); !sameSet(got, want.running) {
		t.Errorf("running = %v, want %v", got, want.running)
	}
	if got := emittedJobs(t, res, wf); !slices.Equal(got, want.emitted) {
		t.Errorf("emitted jobs = %v, want %v", got, want.emitted)
	}
	if res.Report.Gate == nil {
		t.Errorf("the report has no gate")
		return
	}
	if got := res.Report.Gate.Requires; !slices.Equal(got, want.gate) {
		t.Errorf("gate requires %v, want %v", got, want.gate)
	}
}

// rbWarned reports whether one warning of the report contains every part.
func rbWarned(res *planner.Result, parts ...string) bool {
	for _, w := range res.Report.Warnings {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(w, p)
		}
		if ok {
			return true
		}
	}
	return false
}

// rbBundle loads the guardrail bundle of this repository.
func rbBundle(t *testing.T) guard.Checker {
	t.Helper()
	b, err := policy.Load("../../policies", map[string]any{})
	if err != nil {
		t.Fatalf("policy bundle: %v", err)
	}
	return b
}

// ---- the invariants ----

// rbCheck is one emitted config to check.
type rbCheck struct {
	wf      string
	branch  string
	in      []byte
	res     *planner.Result
	checker guard.Checker // nil without a policy bundle
}

// rbInvariants checks what every emitted config must hold:
//
//	(a) no requires names a job the output does not define;
//	(b) a job the output keeps keeps every requirement that runs on the branch,
//	    and its requires list differs from the input's only by the names of jobs
//	    that do not run (CircleCI ignores a requirement on such a job), never by all.
//	    The one exception is a gatekeeper, a no-op job that nothing requires: a plan
//	    keeps it, and cuts its requires to the entries that stay, or to the gate;
//	(c) the gate requires only jobs the report says run;
//	safety: the output never runs a job the input does not run on the branch;
//	report: in select mode, the report's running set is what the output runs;
//	(d) circleci config validate passes (with REQUIRE_CIRCLECI_CLI=1);
//	(e) an input that passes the policy bundle gives an output that passes it;
//	(f) the layout check finds a no-op gate in the output that waits for no job
//	    status, and no structural problem (a requires that points nowhere, a
//	    cycle, a workspace attached before it is persisted) that the input did not have.
//
// The run semantics are CircleCI's as the docs give them: a job runs when its
// filters pass and at least one of its requirements runs.
func rbInvariants(t *testing.T, c rbCheck) {
	t.Helper()
	gate := planner.DefaultGateJob
	inEntries := rbEntries(t, c.in, c.wf)
	outEntries := rbEntries(t, c.res.Config, c.wf)
	inIdx, outIdx := rbIndex(inEntries), rbIndex(outEntries)
	infra := func(e *config.Entry) bool { return e.Name == gate || e.Name == planner.ImpactMapJob }

	// (a)
	for _, e := range outEntries {
		for _, r := range e.Requires {
			if _, ok := outIdx[r]; !ok {
				t.Errorf("(a) %s requires %s, which the output does not define", e.Name, r)
			}
		}
	}

	inRuns, outRuns := rbOracle(inEntries, c.branch), rbOracle(outEntries, c.branch)

	// (b)
	for _, e := range outEntries {
		if infra(e) {
			continue
		}
		i, ok := inIdx[e.Name]
		if !ok {
			t.Errorf("the output has job %s, which the input does not", e.Name)
			continue
		}
		if c.res.Report.Mode == "select" && !c.res.Report.Full && inRuns[i] != rbNo && rbGatekeeper(inEntries, inIdx, i) {
			// A gatekeeper stays, and waits only for the entries that stay. When
			// none of them does, it waits for the gate.
			var want []string
			for _, r := range inEntries[i].Requires {
				if _, kept := outIdx[r]; kept {
					want = append(want, r)
				}
			}
			if len(want) == 0 && len(inEntries[i].Requires) > 0 {
				want = []string{gate}
			}
			if !slices.Equal(e.Requires, want) {
				t.Errorf("(b) the gatekeeper %s requires %v, want %v", e.Name, e.Requires, want)
			}
			continue
		}
		for _, a := range rbLiveAncestors(inEntries, inIdx, inRuns, i) {
			if _, ok := outIdx[a.Name]; !ok {
				t.Errorf("(b) %s is kept, but its requirement %s, which runs on %s, is gone from the output", e.Name, a.Name, c.branch)
			}
		}
		var want []string
		for _, r := range inEntries[i].Requires {
			j, existed := inIdx[r]
			if _, kept := outIdx[r]; kept || !existed {
				want = append(want, r)
			} else if inRuns[j] != rbNo {
				t.Errorf("(b) %s lost its requirement %s, which may run on %s", e.Name, r, c.branch)
			}
		}
		if !slices.Equal(e.Requires, want) {
			t.Errorf("(b) %s requires %v, want the input's %v without the jobs that do not run", e.Name, e.Requires, want)
		}
		if len(inEntries[i].Requires) > 0 && len(e.Requires) == 0 {
			t.Errorf("(b) %s lost every requirement, so it would run when CircleCI would not run it", e.Name)
		}
	}

	// (c)
	var gateEntry *config.Entry
	for _, e := range outEntries {
		if e.Name == gate {
			gateEntry = e
		}
	}
	if gateEntry == nil {
		t.Errorf("(c) the output has no gate job %s", gate)
	} else {
		ran := running(c.res)
		for _, r := range gateEntry.Requires {
			if !slices.Contains(ran, r) {
				t.Errorf("(c) the gate requires %s, which the report does not run", r)
			}
			if v := outRuns[outIdx[r]]; v != rbYes {
				t.Errorf("(c) the gate requires %s, which CircleCI does not surely run on %s", r, c.branch)
			}
		}
	}

	// safety and report
	for i, e := range outEntries {
		if infra(e) || e.Approval {
			continue
		}
		if orig, ok := inIdx[e.Name]; ok && outRuns[i] == rbYes && inRuns[orig] == rbNo {
			t.Errorf("safety: the output runs %s on branch %s, and the input does not", e.Name, c.branch)
		}
		if c.res.Report.Mode == "select" && outRuns[i] != rbUnknown {
			reported := slices.Contains(running(c.res), e.Name)
			if reported != (outRuns[i] == rbYes) {
				t.Errorf("report: %s is reported running=%v, but the output runs it=%v", e.Name, reported, outRuns[i] == rbYes)
			}
		}
	}

	// (e): every run checks the bundle on the input and the output. An input that
	// passes must give an output that passes when the planner had the bundle. A run
	// without the bundle has no guardrail to hold to, so its result is only logged.
	bundle := rbBundle(t)
	inRes, err := bundle.Check(context.Background(), c.in)
	if err != nil {
		t.Fatalf("policy check of the input: %v", err)
	}
	if !rbHard(inRes) {
		outRes, err := bundle.Check(context.Background(), c.res.Config)
		if err != nil {
			t.Fatalf("policy check of the output: %v", err)
		}
		switch {
		case c.checker != nil && (c.res.GuardrailsFailed || rbHard(outRes)):
			t.Errorf("(e) the input passes the policy bundle, the planner's output does not: failed=%v violations=%+v", c.res.GuardrailsFailed, outRes.Violations)
		case c.checker == nil && rbHard(outRes):
			t.Logf("(e) no bundle was given to the planner, and its output fails the bundle: %+v", outRes.Violations)
		}
	}

	// (d)
	rbValidate(t, c.res.Config)

	// (f)
	rbLayout(t, c)
}

// rbLayout is invariant (f): the plan, read by the layout check, has a no-op gate
// and no structural problem that the input config did not have.
func rbLayout(t *testing.T, c rbCheck) {
	t.Helper()
	ctx := context.Background()
	in, err := planner.CheckLayout(ctx, c.in, planner.LayoutOptions{Workflow: c.wf})
	if err != nil {
		t.Fatalf("layout check of the input: %v", err)
	}
	out, err := planner.CheckLayout(ctx, c.res.Config, planner.LayoutOptions{Workflow: c.wf, GateJob: planner.DefaultGateJob})
	if err != nil {
		t.Fatalf("layout check of the output: %v", err)
	}
	had := map[string]bool{}
	for _, f := range in.Findings {
		had[f.Code+" "+f.Job] = true
	}
	for _, f := range out.Findings {
		switch f.Code {
		case planner.CodeGateMissing, planner.CodeGateNotNoOp, planner.CodeGateAwaitsStatusJob:
			t.Errorf("(f) the layout check finds %s in the plan: %s", f.Code, f.Message)
		case planner.CodeDanglingRequires, planner.CodeCycle, planner.CodeConsumerBeforeProducer, planner.CodeDuplicateName:
			if !had[f.Code+" "+f.Job] {
				t.Errorf("(f) the plan introduced a layout problem: %s: %s", f.Code, f.Message)
			}
		}
	}
}

// rbGatekeeper reports whether entry i of the input is a gatekeeper: a no-op job
// that no other entry requires and that waits for success only. The planner
// keeps it in select mode.
func rbGatekeeper(entries []*config.Entry, idx map[string]int, i int) bool {
	if entries[i].Kind != config.KindNoOp || entries[i].StatusRequires {
		return false
	}
	for j, e := range entries {
		for _, r := range e.Requires {
			if k, ok := idx[r]; ok && k == i && j != i {
				return false
			}
		}
	}
	return true
}

// rbEntries returns the entries of workflow wf in cfg.
func rbEntries(t *testing.T, cfg []byte, wf string) []*config.Entry {
	t.Helper()
	c, err := config.Parse(cfg)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	w, err := c.Workflow(wf)
	if err != nil {
		t.Fatalf("workflow %s: %v", wf, err)
	}
	return w.Entries
}

// rbIndex maps every name an entry answers to, to the entry's index.
func rbIndex(entries []*config.Entry) map[string]int {
	idx := map[string]int{}
	for i, e := range entries {
		for _, n := range e.Names() {
			idx[n] = i
		}
	}
	return idx
}

// rbLiveAncestors lists the entries that entry i requires, directly or not,
// through jobs that run or may run on the branch. A requirement on a job that
// does not run is ignored by CircleCI, and so is what that job requires.
func rbLiveAncestors(entries []*config.Entry, idx map[string]int, runs []rbVerdict, i int) []*config.Entry {
	seen := map[int]bool{i: true}
	var out []*config.Entry
	queue := []int{i}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, r := range entries[cur].Requires {
			j, ok := idx[r]
			if !ok || seen[j] || runs[j] == rbNo {
				continue
			}
			seen[j] = true
			out = append(out, entries[j])
			queue = append(queue, j)
		}
	}
	return out
}

// rbHard reports whether a policy result has a hard failure.
func rbHard(r guard.Result) bool {
	if r.Status == guard.HardFail || r.Status == guard.Error {
		return true
	}
	for _, v := range r.Violations {
		if v.Hard {
			return true
		}
	}
	return false
}

// rbVerdict is whether CircleCI runs a job on a branch.
type rbVerdict int8

const (
	rbNo rbVerdict = iota
	rbUnknown
	rbYes
)

// rbOracle says, for each entry, whether CircleCI runs it on branch: its branch
// filter passes and, if it has requires, at least one of them runs. Status
// requirements, expression filters and unresolved names are unknown.
func rbOracle(entries []*config.Entry, branch string) []rbVerdict {
	idx := rbIndex(entries)
	out := make([]rbVerdict, len(entries))
	state := make([]int8, len(entries)) // 0 new, 1 in progress, 2 done
	var eval func(i int) rbVerdict
	eval = func(i int) rbVerdict {
		switch state[i] {
		case 2:
			return out[i]
		case 1:
			return rbUnknown
		}
		state[i] = 1
		e := entries[i]
		v := rbBranch(e.Filters, branch)
		if v != rbNo && len(e.Requires) > 0 {
			best := rbNo
			for _, r := range e.Requires {
				j, ok := idx[r]
				if !ok || j == i || e.StatusRequires {
					best = max(best, rbUnknown)
					continue
				}
				best = max(best, eval(j))
			}
			v = min(v, best)
		}
		state[i], out[i] = 2, v
		return v
	}
	for i := range entries {
		eval(i)
	}
	return out
}

// rbBranch is CircleCI's branch filter as the docs give it: no branches key
// means every branch (a tags-only filter still runs on branches), only and
// ignore are full matches, and an expression is not judged.
func rbBranch(f config.Filters, branch string) rbVerdict {
	switch {
	case f.Expr:
		return rbUnknown
	case f.Branches == nil:
		return rbYes
	case f.Branches.Dynamic:
		return rbUnknown
	case !f.Branches.HasOnly && !f.Branches.HasIgnore:
		return rbUnknown // the docs disagree here; the cases do not depend on it
	case f.Branches.HasOnly && !rbMatches(f.Branches.Only, branch):
		return rbNo
	case f.Branches.HasIgnore && rbMatches(f.Branches.Ignore, branch):
		return rbNo
	}
	return rbYes
}

// rbMatches reports whether a filter list matches name: an exact name, or a
// /regex/ that must match the whole name.
func rbMatches(patterns []string, name string) bool {
	for _, p := range patterns {
		if len(p) >= 2 && strings.HasPrefix(p, "/") && strings.HasSuffix(p, "/") {
			if regexp.MustCompile(`^(?:` + p[1:len(p)-1] + `)$`).MatchString(name) {
				return true
			}
			continue
		}
		if p == name {
			return true
		}
	}
	return false
}

// rbValidate runs `circleci config validate` on cfg when REQUIRE_CIRCLECI_CLI=1.
// Under that setting a missing CLI, or an API that cannot be reached, fails the
// test. Without it the check is logged and skipped.
func rbValidate(t *testing.T, cfg []byte) {
	t.Helper()
	if os.Getenv("REQUIRE_CIRCLECI_CLI") != "1" {
		t.Log("circleci config validate not run: set REQUIRE_CIRCLECI_CLI=1 to run it")
		return
	}
	bin, err := exec.LookPath("circleci")
	if err != nil {
		t.Fatalf("REQUIRE_CIRCLECI_CLI=1, but the circleci CLI is not on the PATH: %v", err)
	}
	file := filepath.Join(t.TempDir(), "continue.yml")
	if err := os.WriteFile(file, cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "config", "validate", file)
	cmd.Env = append(os.Environ(), "CIRCLE_NO_UPDATE_CHECK=1", "CIRCLE_NO_TELEMETRY=1", "CIRCLE_NO_INTERACTIVE=1", "CI=true", "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	for _, s := range []string{"dial tcp", "no such host", "connection refused", "i/o timeout", "network is unreachable", "TLS handshake", "context deadline exceeded"} {
		if strings.Contains(string(out), s) {
			t.Fatalf("REQUIRE_CIRCLECI_CLI=1, but the CircleCI API cannot be reached: %s", out)
		}
	}
	t.Errorf("not valid CircleCI config (circleci config validate): %v\n%s", err, out)
}

// ---- reading the emitted YAML the way CircleCI reads it ----

// rbDoc parses cfg as generic YAML, with anchors and merge keys resolved.
func rbDoc(t *testing.T, cfg []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(cfg, &doc); err != nil {
		t.Fatalf("the output is not valid YAML: %v", err)
	}
	return doc
}

// rbWorkflow returns the named workflow of a parsed config.
func rbWorkflow(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	wfs, _ := doc["workflows"].(map[string]any)
	w, ok := wfs[name].(map[string]any)
	if !ok {
		t.Fatalf("no workflow %s", name)
	}
	return w
}

// rbEntryBody returns the arguments of the entry whose name in workflow wf is
// name (its name: override if it has one), empty for a bare name.
func rbEntryBody(t *testing.T, doc map[string]any, wf, name string) map[string]any {
	t.Helper()
	items, _ := rbWorkflow(t, doc, wf)["jobs"].([]any)
	for _, item := range items {
		switch item := item.(type) {
		case string:
			if item == name {
				return map[string]any{}
			}
		case map[string]any:
			for key, body := range item {
				m, _ := body.(map[string]any)
				if m == nil {
					m = map[string]any{}
				}
				effective := key
				if n, ok := m["name"].(string); ok {
					effective = n
				}
				if effective == name {
					return m
				}
			}
		}
	}
	t.Fatalf("no entry %s in workflow %s", name, wf)
	return nil
}

// rbJob returns the definition of job name.
func rbJob(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	jobs, _ := doc["jobs"].(map[string]any)
	j, ok := jobs[name].(map[string]any)
	if !ok {
		t.Fatalf("no job definition %s", name)
	}
	return j
}

// rbSteps lists steps as "key", or "key:name" when the step has a name.
func rbSteps(t *testing.T, v any) []string {
	t.Helper()
	if v == nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("steps are not a list: %#v", v)
	}
	var out []string
	for _, s := range list {
		switch s := s.(type) {
		case string:
			out = append(out, s)
		case map[string]any:
			for key, body := range s {
				name := key
				if m, ok := body.(map[string]any); ok && m["name"] != nil {
					name = key + ":" + fmt.Sprint(m["name"])
				}
				out = append(out, name)
			}
		default:
			t.Fatalf("unexpected step %#v", s)
		}
	}
	return out
}

// rbCount counts how often s is in list.
func rbCount(list []string, s string) int {
	n := 0
	for _, x := range list {
		if x == s {
			n++
		}
	}
	return n
}

// rbSameTail checks that the output holds the input's text from marker to the
// end, byte for byte.
func rbSameTail(t *testing.T, in, out []byte, marker string) {
	t.Helper()
	i, j := bytes.Index(in, []byte(marker)), bytes.Index(out, []byte(marker))
	if i < 0 || j < 0 {
		t.Fatalf("marker %q not found", marker)
	}
	if !bytes.Equal(in[i:], out[j:]) {
		t.Errorf("the text from %q changed:\n--- input\n%s\n--- output\n%s", marker, in[i:], out[j:])
	}
}

// rbSameMeaning checks that the output means the same as the input for what the
// planner does not touch: workflow wf, and every job definition the input has.
// Both are read as YAML, so formatting does not count.
func rbSameMeaning(t *testing.T, in, out []byte, wf string) {
	t.Helper()
	inDoc, outDoc := rbDoc(t, in), rbDoc(t, out)
	if want, got := rbWorkflow(t, inDoc, wf), rbWorkflow(t, outDoc, wf); !reflect.DeepEqual(want, got) {
		t.Errorf("workflow %s changed:\n--- input\n%v\n--- output\n%v", wf, want, got)
	}
	inJobs, _ := inDoc["jobs"].(map[string]any)
	outJobs, _ := outDoc["jobs"].(map[string]any)
	for name, want := range inJobs {
		if got := outJobs[name]; !reflect.DeepEqual(want, got) {
			t.Errorf("job %s changed:\n--- input\n%v\n--- output\n%v", name, want, got)
		}
	}
}

// ---- the cases ----

// Case 1: an approval in the middle of the DAG. Only build's inputs change, so
// the approval and what follows it are not selected and not awaited.
func TestRobustness_ApprovalInTheMiddle(t *testing.T) {
	rbRun(t, rbCase{
		cfg:    rbFixture(t, "approval_middle.yml"),
		base:   map[string]string{"app/main.go": "package app\n", "deploy/run.sh": "#!/bin/sh\n"},
		change: rbEdit("app/main.go", "package app\n\n// changed\n"),
		reads: map[string]rbReads{
			"build":  {files: []string{"app/main.go"}, dirs: []string{"app"}},
			"deploy": {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}},
		},
		want: rbWant{running: []string{"build"}, emitted: []string{"build", "smarter-gate"}, gate: []string{"build"}},
	})
}

// CircleCI's configuration reference (`requires`): "When jobs you list as
// dependencies do not execute (due to filters, for example), CircleCI ignores
// them as dependencies for other jobs. But if filters exclude all dependencies
// of a job, that job doesn't execute either." The planner reproduces that (spec
// step 5). So a job whose requirements are all filtered out does not run, even
// when it is mandatory: it is left out with a warning, and it is not forced.
// Dropping its requirements instead would run it, and for an approval that the
// branch filters out, run it without its approval.
func TestRobustness_JobWithOnlyFilteredRequirementsDoesNotRun(t *testing.T) {
	t.Run("a mandatory job whose only requirement is filtered out", func(t *testing.T) {
		res := rbRun(t, rbCase{
			cfg:    rbFixture(t, "mandatory_filtered.yml"),
			base:   map[string]string{"services/api/main.go": "package api\n", "release/notes.md": "# notes\n", "release/publish.sh": "#!/bin/sh\n"},
			change: rbEdit("services/api/main.go", "package api\n\n// changed\n"),
			reads: map[string]rbReads{
				"lint":         {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
				"release-prep": {files: []string{"release/notes.md"}, dirs: []string{"release"}},
				"publish":      {files: []string{"release/publish.sh"}, dirs: []string{"release"}},
			},
			mandatory: []string{"publish"},
			want:      rbWant{running: []string{"lint"}, emitted: []string{"lint", "smarter-gate"}, gate: []string{"lint"}},
		})
		if !rbWarned(res, "mandatory job publish", "requires release-prep, which does not run", "does not force it") {
			t.Errorf("warnings = %v, want one about the mandatory job publish", res.Report.Warnings)
		}
		if got := reasons(t, res, "publish"); len(got) != 1 || got[0] != "does not run: it requires release-prep, which does not run" {
			t.Errorf("publish reasons = %v", got)
		}
	})
	t.Run("a mandatory job whose only requirement is an approval that the branch filters out", func(t *testing.T) {
		res := rbRun(t, rbCase{
			cfg:    rbFixture(t, "mandatory_filtered_approval.yml"),
			base:   map[string]string{"services/api/main.go": "package api\n", "deploy/run.sh": "#!/bin/sh\n"},
			change: rbEdit("services/api/main.go", "package api\n\n// changed\n"),
			reads: map[string]rbReads{
				"lint":   {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
				"deploy": {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}},
			},
			mandatory: []string{"deploy"},
			want:      rbWant{running: []string{"lint"}, emitted: []string{"lint", "smarter-gate"}, gate: []string{"lint"}},
		})
		if !rbWarned(res, "mandatory job deploy", "requires hold, which does not run", "does not force it") {
			t.Errorf("warnings = %v, want one about the mandatory job deploy", res.Report.Warnings)
		}
	})
	t.Run("a mandatory job whose own filters leave out the branch", func(t *testing.T) {
		res := rbRun(t, rbCase{
			cfg:    rbFixture(t, "mandatory_filtered.yml"),
			base:   map[string]string{"services/api/main.go": "package api\n", "release/notes.md": "# notes\n", "release/publish.sh": "#!/bin/sh\n"},
			change: rbEdit("services/api/main.go", "package api\n\n// changed\n"),
			reads: map[string]rbReads{
				"lint":         {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
				"release-prep": {files: []string{"release/notes.md"}, dirs: []string{"release"}},
				"publish":      {files: []string{"release/publish.sh"}, dirs: []string{"release"}},
			},
			mandatory: []string{"release-prep"},
			want:      rbWant{running: []string{"lint"}, emitted: []string{"lint", "smarter-gate"}, gate: []string{"lint"}},
		})
		if !rbWarned(res, "mandatory job release-prep", "its filters leave out branch feature", "does not force it") {
			t.Errorf("warnings = %v, want one about the mandatory job release-prep", res.Report.Warnings)
		}
	})
}

// A requirement on a job that does not run is ignored while another requirement
// runs (CircleCI's reference, as above): ship requires [build-main, test],
// build-main is filtered out, and ship runs after test. The plan keeps ship, and
// emits it with `requires: [test]`, which means what the input meant.
func TestRobustness_FilteredRequirementIsDroppedWhileAnotherOneRuns(t *testing.T) {
	base := map[string]string{"app/main.go": "package app\n", "build/main.sh": "#!/bin/sh\n", "ship/run.sh": "#!/bin/sh\n"}
	reads := map[string]rbReads{
		"build-main": {files: []string{"build/main.sh"}, dirs: []string{"build"}},
		"test":       {files: []string{"app/main.go"}, dirs: []string{"app"}},
		"ship":       {files: []string{"ship/run.sh"}, dirs: []string{"ship"}},
	}
	want := rbWant{running: []string{"test", "ship"}, emitted: []string{"test", "ship", "smarter-gate"}, gate: []string{"test", "ship"}}
	t.Run("both read the change", func(t *testing.T) {
		reads := maps.Clone(reads)
		reads["ship"] = rbReads{files: []string{"app/main.go"}, dirs: []string{"app"}}
		res := rbRun(t, rbCase{cfg: rbFixture(t, "filtered_mixed.yml"), base: base, reads: reads, want: want,
			change: rbEdit("app/main.go", "package app\n\n// changed\n")})
		if got := emittedEntry(t, res, "ci", "ship").Requires; !slices.Equal(got, []string{"test"}) {
			t.Errorf("ship requires %v, want [test]", got)
		}
	})
	t.Run("only ship reads the change: the closure takes test, and skips build-main", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: rbFixture(t, "filtered_mixed.yml"), base: base, reads: reads, want: want,
			change: rbEdit("ship/run.sh", "#!/bin/sh\n# changed\n")})
		if got := emittedEntry(t, res, "ci", "ship").Requires; !slices.Equal(got, []string{"test"}) {
			t.Errorf("ship requires %v, want [test]", got)
		}
		if got := reasons(t, res, "test"); !slices.Equal(got, []string{"required by ship"}) {
			t.Errorf("test reasons = %v", got)
		}
	})
}

// CircleCI's reference, once more, for an approval: deploy requires
// [hold-deploy, test], and hold-deploy is filtered to main. On a branch the
// approval is ignored, and deploy runs after test, with no approval. That is
// what CircleCI does with the input, so the plan must not change it: deploy
// stays, its requirement on the approval goes with the approval, and the gate
// waits for it, since nothing blocks it. On main, the approval is there, and
// the requirement stays.
func TestRobustness_ApprovalFilteredOutIsIgnoredWhileAnotherRequirementRuns(t *testing.T) {
	cfg := rbFixture(t, "approval_filtered_partial.yml")
	base := map[string]string{"services/api/main.go": "package api\n", "deploy/run.sh": "#!/bin/sh\n"}
	reads := map[string]rbReads{
		"test":   {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
		"deploy": {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}},
	}
	t.Run("on a branch, deploy runs after test", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("deploy/run.sh", "#!/bin/sh\n# changed\n"),
			want: rbWant{running: []string{"test", "deploy"}, emitted: []string{"test", "deploy", "smarter-gate"}, gate: []string{"test", "deploy"}}})
		if got := emittedEntry(t, res, "ci", "deploy").Requires; !slices.Equal(got, []string{"test"}) {
			t.Errorf("deploy requires %v, want [test]", got)
		}
	})
	t.Run("on a branch, a change deploy does not read leaves it out with the approval", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("services/api/main.go", "package api\n\n// changed\n"),
			want: rbWant{running: []string{"test"}, emitted: []string{"test", "smarter-gate"}, gate: []string{"test"}}})
	})
	t.Run("on main, the approval is there and stays", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, branch: "main", change: rbEdit("deploy/run.sh", "#!/bin/sh\n# changed\n"),
			want: rbWant{full: true, running: []string{"test", "hold-deploy", "deploy"},
				emitted: []string{"test", "hold-deploy", "deploy", "smarter-impact-map", "smarter-gate"}, gate: []string{"test"}}})
		if got := emittedEntry(t, res, "ci", "deploy").Requires; !slices.Equal(got, []string{"hold-deploy", "test"}) {
			t.Errorf("deploy requires %v, want [hold-deploy test]", got)
		}
	})
}

// The rule is recursive: build-main is filtered out, package requires only it,
// and publish requires only package. Neither package nor publish runs, so both
// leave the plan, whatever changed, and a mandatory publish gets a warning.
func TestRobustness_ChainBehindAFilteredJobDoesNotRun(t *testing.T) {
	base := map[string]string{"services/api/main.go": "package api\n", "release/publish.sh": "#!/bin/sh\n", "pkg/x.txt": "x\n"}
	reads := map[string]rbReads{
		"lint":       {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
		"build-main": {files: []string{"pkg/x.txt"}, dirs: []string{"pkg"}},
		"package":    {files: []string{"pkg/x.txt"}, dirs: []string{"pkg"}},
		"publish":    {files: []string{"release/publish.sh"}, dirs: []string{"release"}},
	}
	want := rbWant{running: []string{"lint"}, emitted: []string{"lint", "smarter-gate"}, gate: []string{"lint"}}
	t.Run("the inputs of the whole chain change", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: rbFixture(t, "filtered_chain.yml"), base: base, reads: reads, want: want,
			change: func(r *difftest.Repo) {
				r.Write("services/api/main.go", "package api\n\n// changed\n")
				r.Write("pkg/x.txt", "changed\n")
				r.Write("release/publish.sh", "#!/bin/sh\n# changed\n")
			}})
		for job, why := range map[string]string{
			"build-main": "does not run: its filters leave out branch feature",
			"package":    "does not run: it requires build-main, which does not run",
			"publish":    "does not run: it requires package, which does not run",
		} {
			if got := reasons(t, res, job); len(got) != 1 || got[0] != why {
				t.Errorf("%s reasons = %v, want %q", job, got, why)
			}
		}
	})
	t.Run("the last job of the chain is mandatory", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: rbFixture(t, "filtered_chain.yml"), base: base, reads: reads, want: want, mandatory: []string{"publish"},
			change: rbEdit("services/api/main.go", "package api\n\n// changed\n")})
		if !rbWarned(res, "mandatory job publish", "requires package, which does not run", "does not force it") {
			t.Errorf("warnings = %v, want one about the mandatory job publish", res.Report.Warnings)
		}
	})
}

// Case 2, the removal case: a job the change does not reach, whose only
// requirement is filtered out, is removed with that requirement.
func TestRobustness_RequirementFilteredOutRemovesDependent(t *testing.T) {
	rbRun(t, rbCase{
		cfg: rbFixture(t, "filtered_dependents.yml"),
		base: map[string]string{
			"services/api/main.go": "package api\n", "build/main.sh": "#!/bin/sh\n", "deploy/run.sh": "#!/bin/sh\n",
		},
		change: rbEdit("services/api/main.go", "package api\n\n// changed\n"),
		reads: map[string]rbReads{
			"lint":       {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
			"build-main": {files: []string{"build/main.sh"}, dirs: []string{"build"}},
			"ship":       {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}},
		},
		want: rbWant{running: []string{"lint"}, emitted: []string{"lint", "smarter-gate"}, gate: []string{"lint"}},
	})
}

// A job that requires a job by a status (`deploy: failed`), where the job is
// filtered out on the branch: the planner cannot tell whether CircleCI runs it
// (the status may be met by a job that did not run), so it is unknown, and the
// gate does not wait for it. Left out, its requirement could not be dropped
// without making it run unconditionally, so the plan is a full run.
func TestRobustness_StatusRequirementOnAFilteredJobGivesAFullRun(t *testing.T) {
	res := rbRun(t, rbCase{
		cfg:       rbFixture(t, "status_filtered.yml"),
		base:      map[string]string{"services/api/main.go": "package api\n", "deploy/run.sh": "#!/bin/sh\n", "notify/run.sh": "#!/bin/sh\n"},
		change:    rbEdit("services/api/main.go", "package api\n\n// changed\n"),
		reads:     map[string]rbReads{"lint": {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}}, "deploy": {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}}},
		mandatory: []string{"notify"},
		want: rbWant{full: true, running: []string{"lint", "notify"}, emitted: []string{"lint", "deploy", "notify", "smarter-gate"},
			gate: []string{"lint"}},
	})
	if !strings.Contains(res.Report.FullReason, "job notify requires only jobs that do not run here") {
		t.Errorf("full-run reason = %q", res.Report.FullReason)
	}
	if got := emittedEntry(t, res, "ci", "notify").Requires; !slices.Equal(got, []string{"deploy"}) {
		t.Errorf("notify requires %v: the plan must not edit it", got)
	}
	if !rbWarned(res, "smarter-gate does not wait for notify") {
		t.Errorf("warnings = %v", res.Report.Warnings)
	}
}

// Case 3: two workflows. Only the planned one is pruned, and the other keeps its
// bytes (the text from its key to the end of the file).
func TestRobustness_TwoWorkflowsOnlyPlannedIsPruned(t *testing.T) {
	cfg := rbFixture(t, "two_workflows.yml")
	res := rbRun(t, rbCase{
		cfg:    cfg,
		base:   map[string]string{"services/api/main.go": "package api\n", "docs/guide.md": "# guide\n"},
		change: rbEdit("docs/guide.md", "# guide\n\nA new paragraph.\n"),
		reads: map[string]rbReads{
			"lint": {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
			"test": {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
			"docs": {files: []string{"docs/guide.md"}, dirs: []string{"docs"}},
		},
		want: rbWant{running: []string{"docs"}, emitted: []string{"docs", "smarter-gate"}, gate: []string{"docs"}},
	})
	rbSameTail(t, cfg, res.Config, "  nightly:\n")
	if _, err := planner.Run(context.Background(), planner.Input{Config: cfg}, planner.DefaultOptions()); err == nil {
		t.Error("two workflows and no --workflow: want a usage error")
	}
}

// The other workflow means the same in the output, even when its job lists are
// written without indentation: the text may change, the meaning may not.
func TestRobustness_OtherWorkflowKeepsItsMeaning(t *testing.T) {
	cfg := rbFixture(t, "two_workflows_unindented.yml")
	res := rbRun(t, rbCase{
		cfg:    cfg,
		base:   map[string]string{"services/api/main.go": "package api\n", "docs/guide.md": "# guide\n"},
		change: rbEdit("docs/guide.md", "# guide\n\nA new paragraph.\n"),
		reads: map[string]rbReads{
			"lint": {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
			"test": {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
			"docs": {files: []string{"docs/guide.md"}, dirs: []string{"docs"}},
		},
		want: rbWant{running: []string{"docs"}, emitted: []string{"docs", "smarter-gate"}, gate: []string{"docs"}},
	})
	rbSameMeaning(t, cfg, res.Config, "nightly")
}

// Case 4: a matrix job required by a later job by its base name. The output
// validates, and a mandatory job after the matrix keeps the base-name requires.
func TestRobustness_MatrixJobRequiredByBaseName(t *testing.T) {
	cfg := rbFixture(t, "matrix_requires.yml")
	base := map[string]string{"services/api/main.go": "package api\n", "deploy/run.sh": "#!/bin/sh\n"}
	reads := map[string]rbReads{
		"lint":   {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
		"deploy": {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}},
	}
	change := rbEdit("services/api/main.go", "package api\n\n// changed\n")
	t.Run("the job after the matrix is pruned", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, change: change, reads: reads,
			want: rbWant{running: []string{"lint", "build"}, emitted: []string{"lint", "build", "smarter-gate"}, gate: []string{"lint", "build"}}})
	})
	t.Run("the job after the matrix is mandatory", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, change: change, reads: reads, mandatory: []string{"deploy"},
			want: rbWant{running: []string{"lint", "build", "deploy"}, emitted: []string{"lint", "build", "deploy", "smarter-gate"}, gate: []string{"lint", "build", "deploy"}}})
	})
}

// Case 5: orb jobs with name overrides. They are planned by their names (no
// impact map entry, so they run), and requires can name the override.
func TestRobustness_OrbJobNameOverrides(t *testing.T) {
	cfg := rbFixture(t, "orb_overrides.yml")
	base := map[string]string{"services/api/main.go": "package api\n", "deploy/run.sh": "#!/bin/sh\n"}
	reads := map[string]rbReads{"deploy": {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}}}
	change := rbEdit("services/api/main.go", "package api\n\n// changed\n")
	t.Run("the deploy is pruned", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, change: change, reads: reads,
			want: rbWant{running: []string{"unit-tests", "integration-tests"}, emitted: []string{"unit-tests", "integration-tests", "smarter-gate"}, gate: []string{"unit-tests", "integration-tests"}}})
	})
	t.Run("the deploy is mandatory and requires the override", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, change: change, reads: reads, mandatory: []string{"deploy"},
			want: rbWant{running: []string{"unit-tests", "integration-tests", "deploy"}, emitted: []string{"unit-tests", "integration-tests", "deploy", "smarter-gate"}, gate: []string{"unit-tests", "integration-tests", "deploy"}}})
	})
}

// Case 6: requires as a block list and as a flow list. A job-status key is
// present in the same config and is not selected here.
func TestRobustness_RequiresLongAndFlowForms(t *testing.T) {
	cfg := rbFixture(t, "requires_forms.yml")
	base := map[string]string{"services/api/main.go": "package api\n", "deploy/run.sh": "#!/bin/sh\n", "notify/run.sh": "#!/bin/sh\n"}
	reads := map[string]rbReads{
		"lint":            {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
		"build":           {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
		"test":            {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
		"deploy":          {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}},
		"notify-canceled": {files: []string{"notify/run.sh"}, dirs: []string{"notify"}},
	}
	change := rbEdit("services/api/main.go", "package api\n\n// changed\n")
	t.Run("the planned jobs", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, change: change, reads: reads,
			want: rbWant{running: []string{"lint", "build", "test"}, emitted: []string{"lint", "build", "test", "smarter-gate"}, gate: []string{"lint", "build", "test"}}})
	})
	t.Run("the flow-form job is mandatory", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, change: change, reads: reads, mandatory: []string{"deploy"},
			want: rbWant{running: []string{"lint", "build", "test", "deploy"}, emitted: []string{"lint", "build", "test", "deploy", "smarter-gate"}, gate: []string{"lint", "build", "test", "deploy"}}})
	})
}

// The gate does not wait for a job that requires a job status other than
// success: a job that waits for a failure may never run, and the gate would wait
// for it for ever. The plan says so in a warning, and still runs the job.
func TestRobustness_StatusRequiresJobIsNotAwaitedByGate(t *testing.T) {
	res := rbRun(t, rbCase{
		cfg:    rbFixture(t, "status_requires.yml"),
		base:   map[string]string{"services/api/main.go": "package api\n", "cleanup/run.sh": "#!/bin/sh\n"},
		change: rbEdit("services/api/main.go", "package api\n\n// changed\n"),
		reads: map[string]rbReads{
			"lint":    {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
			"build":   {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
			"cleanup": {files: []string{"cleanup/run.sh"}, dirs: []string{"cleanup"}},
		},
		mandatory: []string{"cleanup"},
		want:      rbWant{running: []string{"lint", "build", "cleanup"}, emitted: []string{"lint", "build", "cleanup", "smarter-gate"}, gate: []string{"lint", "build"}},
	})
	if !rbWarned(res, "smarter-gate does not wait for cleanup: it requires a job status other than success") {
		t.Errorf("warnings = %v, want one that the gate does not wait for cleanup", res.Report.Warnings)
	}
	if rbWarned(res, "does not wait for lint") || rbWarned(res, "does not wait for build") {
		t.Errorf("warnings = %v: only cleanup requires a status", res.Report.Warnings)
	}
}

// Case 7: a workflow gated on a pipeline parameter plans when the value is known.
func TestRobustness_WhenParameter(t *testing.T) {
	cfg := rbFixture(t, "when_parameter.yml")
	base := map[string]string{"services/api/main.go": "package api\n", "app/x.go": "package app\n"}
	change := rbEdit("services/api/main.go", "package api\n\n// changed\n")
	reads := map[string]rbReads{
		"lint": {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
		"test": {files: []string{"app/x.go"}, dirs: []string{"app"}},
	}
	t.Run("without parameters", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, change: change, reads: reads,
			want: rbWant{full: true, running: []string{"lint", "test"}, emitted: []string{"lint", "test", "smarter-gate"}, gate: []string{"lint", "test"}}})
		if !strings.Contains(res.Report.FullReason, "cannot be evaluated") {
			t.Errorf("full-run reason = %q", res.Report.FullReason)
		}
		noNewExpressions(t, cfg, res.Config)
	})
	t.Run("run-ci true prunes", func(t *testing.T) {
		res := rbRun(t, rbCase{
			cfg: cfg, base: base, change: change, reads: reads,
			parameters: map[string]any{"run-ci": true},
			want:       rbWant{running: []string{"lint"}, emitted: []string{"lint", "smarter-gate"}, gate: []string{"lint"}},
		})
		if res.Report.Full {
			t.Fatalf("full run: %s", res.Report.FullReason)
		}
	})
	t.Run("run-ci false skips the workflow", func(t *testing.T) {
		res := rbRun(t, rbCase{
			cfg: cfg, base: base, change: change, reads: reads,
			parameters: map[string]any{"run-ci": false},
			want:       rbWant{workflowSkipped: true, running: []string{}, emitted: []string{"lint", "test"}, gate: nil},
		})
		if string(res.Config) != string(cfg) {
			t.Error("config was modified when workflow skipped")
		}
		if got := rbWorkflow(t, rbDoc(t, res.Config), "ci")["when"]; got != "<< pipeline.parameters.run-ci >>" {
			t.Errorf("when = %v", got)
		}
	})
}

// Case 8: expression filters (a string) and regex filters. An expression is
// unknown, so its job and what depends on it stay out of the gate, with a
// warning. The regex decides whether a branch runs the job.
func TestRobustness_ExpressionAndRegexFilters(t *testing.T) {
	cfg := rbFixture(t, "filters_expr_regex.yml")
	base := map[string]string{"services/web/app.ts": "export {};\n"}
	reads := map[string]rbReads{
		"lint":          {files: []string{"services/web/app.ts"}, dirs: []string{"services/web"}},
		"feature-build": {files: []string{"services/web/app.ts"}, dirs: []string{"services/web"}},
		"expr-job":      {files: []string{"services/web/app.ts"}, dirs: []string{"services/web"}},
		"dependent":     {files: []string{"services/web/app.ts"}, dirs: []string{"services/web"}},
	}
	change := rbEdit("services/web/app.ts", "export const app = 1;\n")
	t.Run("a branch the regex matches", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, branch: "feature/login", change: change, reads: reads, mandatory: []string{"dependent"},
			want: rbWant{running: []string{"lint", "feature-build", "expr-job", "dependent"}, emitted: []string{"lint", "feature-build", "expr-job", "dependent", "smarter-gate"}, gate: []string{"lint", "feature-build"}}})
		if !rbWarned(res, "job expr-job", "left out of the gate") {
			t.Errorf("warnings = %v, want one about expr-job", res.Report.Warnings)
		}
	})
	t.Run("a branch the regex does not match", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, branch: "bugfix/x", change: change, reads: reads, mandatory: []string{"dependent"},
			want: rbWant{running: []string{"lint", "expr-job", "dependent"}, emitted: []string{"lint", "expr-job", "dependent", "smarter-gate"}, gate: []string{"lint"}}})
		if !rbWarned(res, "job expr-job", "left out of the gate") {
			t.Errorf("warnings = %v, want one about expr-job", res.Report.Warnings)
		}
	})
}

// Case 9: a job whose map entry is incomplete always runs, and a job missing
// from the map always runs. A closure still picks up the requirements of both.
func TestRobustness_IncompleteAndMissingMapEntries(t *testing.T) {
	rbRun(t, rbCase{
		cfg: rbFixture(t, "map_gaps.yml"),
		base: map[string]string{
			"services/a/x.go": "package a\n", "services/d/x.go": "package d\n", "docs/readme.md": "# readme\n",
		},
		change: rbEdit("docs/readme.md", "# readme\n\nMore.\n"),
		reads: map[string]rbReads{
			"alpha": {files: []string{"services/a/x.go"}, dirs: []string{"services/a"}},
			"beta":  {incomplete: true},
			"delta": {files: []string{"services/d/x.go"}, dirs: []string{"services/d"}},
		},
		want: rbWant{running: []string{"alpha", "beta", "gamma"}, emitted: []string{"alpha", "beta", "gamma", "smarter-gate"}, gate: []string{"alpha", "beta", "gamma"}},
	})
}

// Case 10: renames, deletes, and paths with spaces or unicode. Each change must
// select the job that read the path, through the file or through the directory.
func TestRobustness_RenamesDeletesAndUnicodePaths(t *testing.T) {
	cfg := rbFixture(t, "paths.yml")
	base := map[string]string{
		"web/src/My Components/Button.tsx": "export {};\n",
		"web/src/other.ts":                 "export {};\n",
		"api/handler.go":                   "package api\n",
		"docs/café.md":                     "# café\n",
	}
	reads := map[string]rbReads{
		"web-build": {files: []string{"web/src/My Components/Button.tsx"}, dirs: []string{"web/src"}},
		"api-test":  {files: []string{"api/handler.go"}, dirs: []string{"api"}},
		"docs-site": {files: []string{"docs/café.md"}, dirs: []string{"docs"}},
	}
	t.Run("a file is renamed", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, reads: reads,
			change: func(r *difftest.Repo) { r.Git("mv", "api/handler.go", "api/routes.go") },
			want:   rbWant{running: []string{"api-test"}, emitted: []string{"api-test", "smarter-gate"}, gate: []string{"api-test"}}})
	})
	t.Run("a file with a space in its path is deleted", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, reads: reads,
			change: func(r *difftest.Repo) { r.Remove("web/src/My Components/Button.tsx") },
			want:   rbWant{running: []string{"web-build"}, emitted: []string{"web-build", "smarter-gate"}, gate: []string{"web-build"}}})
	})
	t.Run("a file with a unicode name is edited", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, reads: reads,
			change: rbEdit("docs/café.md", "# café\n\nchanged\n"),
			want:   rbWant{running: []string{"docs-site"}, emitted: []string{"docs-site", "smarter-gate"}, gate: []string{"docs-site"}}})
	})
	t.Run("a new directory with a unicode name appears under a listed one", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, reads: reads,
			change: rbEdit("web/src/ünïcode/index.ts", "export {};\n"),
			want:   rbWant{running: []string{"web-build"}, emitted: []string{"web-build", "smarter-gate"}, gate: []string{"web-build"}}})
	})
}

// Case 11: nothing is selected, and the mandatory job is not in the config.
// Without a policy bundle the plan is the gate alone, with a warning. With the
// bundle the config fails required_jobs, which cannot be repaired, so planning
// fails the guardrails (exit 3).
func TestRobustness_NothingSelectedMandatoryMissing(t *testing.T) {
	cfg := rbFixture(t, "no_security_scan.yml")
	base := map[string]string{"services/api/main.go": "package api\n", "site/config.yml": "a: 1\n", "CHANGELOG.md": "# changes\n"}
	reads := map[string]rbReads{
		"lint":       {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
		"docs-build": {files: []string{"site/config.yml"}, dirs: []string{"site"}},
	}
	change := rbEdit("CHANGELOG.md", "# changes\n\n- one\n")
	t.Run("without a policy bundle", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: change, mandatory: []string{"security-scan"},
			want: rbWant{emitted: []string{"smarter-gate"}}})
		if res.GuardrailsFailed {
			t.Error("no bundle: the guardrails cannot fail")
		}
		if !rbWarned(res, "mandatory job security-scan is not in workflow ci") {
			t.Errorf("warnings = %v, want one about the missing mandatory job", res.Report.Warnings)
		}
	})
	t.Run("with the policy bundle", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: change, mandatory: []string{"security-scan"}, checker: rbBundle(t),
			want: rbWant{full: true, running: []string{"lint", "docs-build"}, emitted: []string{"lint", "docs-build", "smarter-gate"}, gate: []string{"lint", "docs-build"}}})
		if !res.GuardrailsFailed {
			t.Error("the bundle requires security-scan, which the config lacks: want the guardrails to fail (exit 3)")
		}
	})
}

// Case 12: the diamond a→b, a→c, b,c→d, where only c's inputs change. Without a
// workspace, the gate waits for a and c only. A persisted workspace selects its
// consumers too (spec step 4).
func TestRobustness_DiamondOnlyOneBranchChanges(t *testing.T) {
	base := map[string]string{
		"shared/config.json": "{}\n", "b/main.go": "package b\n", "c/main.go": "package c\n", "d/main.go": "package d\n",
	}
	change := rbEdit("c/main.go", "package c\n\n// changed\n")
	reads := map[string]rbReads{
		"a": {files: []string{"shared/config.json"}, dirs: []string{"shared"}},
		"b": {files: []string{"b/main.go"}, dirs: []string{"b"}},
		"c": {files: []string{"c/main.go"}, dirs: []string{"c"}},
		"d": {files: []string{"d/main.go"}, dirs: []string{"d"}},
	}
	t.Run("no workspace", func(t *testing.T) {
		rbRun(t, rbCase{cfg: rbFixture(t, "diamond.yml"), base: base, change: change, reads: reads,
			want: rbWant{running: []string{"a", "c"}, emitted: []string{"a", "c", "smarter-gate"}, gate: []string{"a", "c"}}})
	})
	t.Run("a persists a workspace the others attach", func(t *testing.T) {
		rbRun(t, rbCase{cfg: rbFixture(t, "diamond_workspace.yml"), base: base, change: change, reads: reads,
			want: rbWant{running: []string{"a", "b", "c", "d"}, emitted: []string{"a", "b", "c", "d", "smarter-gate"}, gate: []string{"a", "b", "c", "d"}}})
	})
}

// Case 13: on the default branch, the trace steps are appended to the post-steps
// a job already has, and nothing of the job's own steps is lost.
func TestRobustness_StepsAreAppendedNotReplaced(t *testing.T) {
	base := map[string]string{"services/api/main.go": "package api\n"}
	change := rbEdit("services/api/main.go", "package api\n\n// changed\n")
	t.Run("entries with their own steps", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: rbFixture(t, "steps_existing.yml"), base: base, branch: "main", change: change,
			want: rbWant{full: true, running: []string{"lint", "test"}, emitted: []string{"lint", "test", "smarter-impact-map", "smarter-gate"}, gate: []string{"lint", "test"}}})
		doc := rbDoc(t, res.Config)
		if got, want := rbSteps(t, rbEntryBody(t, doc, "ci", "lint")["post-steps"]), []string{"run", rbCollect, rbPersist}; !slices.Equal(got, want) {
			t.Errorf("lint post-steps = %v, want %v", got, want)
		}
		if got, want := rbSteps(t, rbEntryBody(t, doc, "ci", "test")["post-steps"]), []string{"store_test_results", rbCollect, rbPersist}; !slices.Equal(got, want) {
			t.Errorf("test post-steps = %v, want %v", got, want)
		}
		if got, want := rbSteps(t, rbEntryBody(t, doc, "ci", "lint")["pre-steps"]), []string{"run"}; !slices.Equal(got, want) {
			t.Errorf("lint pre-steps = %v, want %v", got, want)
		}
		if got, want := rbSteps(t, rbJob(t, doc, "lint")["steps"]), []string{"checkout", rbReset, "run"}; !slices.Equal(got, want) {
			t.Errorf("lint steps = %v, want %v (the reset follows checkout)", got, want)
		}
	})
	t.Run("an empty post-steps list", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: rbFixture(t, "steps_empty.yml"), base: base, branch: "main", change: change,
			want: rbWant{full: true, running: []string{"lint", "test"}, emitted: []string{"lint", "test", "smarter-impact-map", "smarter-gate"}, gate: []string{"lint", "test"}}})
		if got, want := rbSteps(t, rbEntryBody(t, rbDoc(t, res.Config), "ci", "lint")["post-steps"]), []string{rbCollect, rbPersist}; !slices.Equal(got, want) {
			t.Errorf("lint post-steps = %v, want %v", got, want)
		}
	})
}

// Expected: a post-steps key with no value is an empty list, so the trace steps are added.
func TestRobustness_EmptyPostStepsKeyIsTraced(t *testing.T) {
	res := rbRun(t, rbCase{
		cfg:    rbFixture(t, "steps_null.yml"),
		base:   map[string]string{"services/api/main.go": "package api\n"},
		branch: "main",
		change: rbEdit("services/api/main.go", "package api\n\n// changed\n"),
		want: rbWant{full: true, running: []string{"lint", "test"},
			emitted: []string{"lint", "test", "smarter-impact-map", "smarter-gate"}, gate: []string{"lint", "test"}},
	})
	if rbWarned(res, "internal error") {
		t.Errorf("warnings = %v, want no internal error", res.Report.Warnings)
	}
	if got, want := rbSteps(t, rbEntryBody(t, rbDoc(t, res.Config), "ci", "lint")["post-steps"]), []string{rbCollect, rbPersist}; !slices.Equal(got, want) {
		t.Errorf("lint post-steps = %v, want %v", got, want)
	}
}

// With --trace-orb-jobs an orb entry gets the pre-steps [checkout, reset]: the
// planner cannot reach the orb job's own checkout, so the entry checks out first
// and resets the access times after it (spec step 8). Without the flag the orb
// entries are not traced at all, and get no pre-steps and no post-steps.
func TestRobustness_OrbJobTraceAddsCheckoutAndResetPreSteps(t *testing.T) {
	cfg := rbFixture(t, "orb_overrides.yml")
	orbJobs := []string{"unit-tests", "integration-tests"}
	run := func(t *testing.T, traceOrb bool) map[string]any {
		res := rbRun(t, rbCase{
			cfg:      cfg,
			base:     map[string]string{"services/api/main.go": "package api\n", "deploy/run.sh": "#!/bin/sh\n"},
			branch:   "main",
			change:   rbEdit("services/api/main.go", "package api\n\n// changed\n"),
			traceOrb: traceOrb,
			want: rbWant{full: true, running: []string{"unit-tests", "integration-tests", "deploy"},
				emitted: []string{"unit-tests", "integration-tests", "deploy", "smarter-impact-map", "smarter-gate"},
				gate:    []string{"unit-tests", "integration-tests", "deploy"}},
		})
		wantTraced := []string{"deploy"}
		if traceOrb {
			wantTraced = []string{"unit-tests", "integration-tests", "deploy"}
		}
		if got := res.Report.Trace.Jobs; !slices.Equal(got, wantTraced) {
			t.Errorf("traced jobs = %v, want %v", got, wantTraced)
		}
		if got := emittedEntry(t, res, "ci", planner.ImpactMapJob).Requires; !slices.Equal(got, wantTraced) {
			t.Errorf("the map job requires %v, want %v", got, wantTraced)
		}
		return rbDoc(t, res.Config)
	}
	t.Run("with --trace-orb-jobs", func(t *testing.T) {
		doc := run(t, true)
		for _, name := range orbJobs {
			body := rbEntryBody(t, doc, "ci", name)
			if got, want := rbSteps(t, body["pre-steps"]), []string{"checkout", rbReset}; !slices.Equal(got, want) {
				t.Errorf("%s pre-steps = %v, want %v", name, got, want)
			}
			if got, want := rbSteps(t, body["post-steps"]), []string{rbCollect, rbPersist}; !slices.Equal(got, want) {
				t.Errorf("%s post-steps = %v, want %v", name, got, want)
			}
		}
		// The local job has its reset in its definition, after its own checkout.
		if pre := rbEntryBody(t, doc, "ci", "deploy")["pre-steps"]; pre != nil {
			t.Errorf("deploy has pre-steps %v, want none", rbSteps(t, pre))
		}
		if got, want := rbSteps(t, rbJob(t, doc, "deploy")["steps"]), []string{"checkout", rbReset, "run"}; !slices.Equal(got, want) {
			t.Errorf("deploy steps = %v, want %v", got, want)
		}
	})
	t.Run("without --trace-orb-jobs", func(t *testing.T) {
		doc := run(t, false)
		for _, name := range orbJobs {
			body := rbEntryBody(t, doc, "ci", name)
			if body["pre-steps"] != nil || body["post-steps"] != nil {
				t.Errorf("%s is not traced, but has pre-steps %v and post-steps %v", name, body["pre-steps"], body["post-steps"])
			}
		}
	})
}

// Case 14: a job with `checkout: {path: x}` gets the reset after that checkout,
// and a job with no checkout gets no reset. Both are traced and validate.
func TestRobustness_CheckoutPathAndNoCheckout(t *testing.T) {
	res := rbRun(t, rbCase{
		cfg:    rbFixture(t, "checkout_forms.yml"),
		base:   map[string]string{"src/main.go": "package src\n"},
		branch: "main",
		change: rbEdit("src/main.go", "package src\n\n// changed\n"),
		want: rbWant{full: true, running: []string{"custom-path", "no-checkout"},
			emitted: []string{"custom-path", "no-checkout", "smarter-impact-map", "smarter-gate"}, gate: []string{"custom-path", "no-checkout"}},
	})
	doc := rbDoc(t, res.Config)
	if got, want := rbSteps(t, rbJob(t, doc, "custom-path")["steps"]), []string{"checkout", rbReset, "run"}; !slices.Equal(got, want) {
		t.Errorf("custom-path steps = %v, want %v", got, want)
	}
	if got, want := rbSteps(t, rbJob(t, doc, "no-checkout")["steps"]), []string{"run"}; !slices.Equal(got, want) {
		t.Errorf("no-checkout steps = %v, want %v", got, want)
	}
	for _, name := range []string{"custom-path", "no-checkout"} {
		if rbCount(rbSteps(t, rbEntryBody(t, doc, "ci", name)["post-steps"]), rbCollect) != 1 {
			t.Errorf("%s has no single collect step", name)
		}
	}
	if got := emittedEntry(t, res, "ci", planner.ImpactMapJob).Requires; !slices.Equal(got, []string{"custom-path", "no-checkout"}) {
		t.Errorf("the map job requires %v", got)
	}
}

// Case 15: YAML anchors and aliases survive the emit. An anchored filter of a
// pruned entry is inlined where a kept entry uses the alias, and the anchored
// docker image of a kept job is still there.
func TestRobustness_AnchorsSurviveEmit(t *testing.T) {
	cfg := rbFixture(t, "anchors.yml")
	res := rbRun(t, rbCase{
		cfg:       cfg,
		base:      map[string]string{"services/api/main.go": "package api\n", "services/lint/x.go": "package lint\n", "docs/x.md": "# x\n"},
		change:    rbEdit("services/api/main.go", "package api\n\n// changed\n"),
		reads:     map[string]rbReads{"lint": {files: []string{"services/lint/x.go"}, dirs: []string{"services/lint"}}, "test": {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}}, "docs": {files: []string{"docs/x.md"}, dirs: []string{"docs"}}},
		mandatory: []string{"lint"},
		want:      rbWant{running: []string{"lint", "test"}, emitted: []string{"lint", "test", "smarter-gate"}, gate: []string{"lint", "test"}},
	})
	doc := rbDoc(t, res.Config)
	if got := rbEntryBody(t, doc, "ci", "lint")["filters"]; !reflect.DeepEqual(got, map[string]any{"branches": map[string]any{"ignore": "main"}}) {
		t.Errorf("lint filters = %v, want the anchored filter", got)
	}
	if got := rbJob(t, doc, "test")["docker"]; !reflect.DeepEqual(got, []any{map[string]any{"image": "cimg/base:2026.11"}}) {
		t.Errorf("test docker = %v, want the anchored image", got)
	}
}

// Expected: the planned workflow still prunes, and another workflow's alias to a pruned anchor still resolves.
func TestRobustness_AnchorUsedByAnotherWorkflowSurvivesPrune(t *testing.T) {
	cfg := rbFixture(t, "anchor_across_workflows.yml")
	res := rbRun(t, rbCase{
		cfg:    cfg,
		base:   map[string]string{"services/api/main.go": "package api\n", "docs/x.md": "# x\n", "deploy/run.sh": "#!/bin/sh\n"},
		change: rbEdit("services/api/main.go", "package api\n\n// changed\n"),
		reads: map[string]rbReads{
			"lint":   {files: []string{"docs/x.md"}, dirs: []string{"docs"}},
			"deploy": {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}},
		},
		want: rbWant{running: nil, emitted: []string{"smarter-gate"}, gate: nil},
	})
	if got := rbEntryBody(t, rbDoc(t, res.Config), "nightly", "deploy")["filters"]; !reflect.DeepEqual(got, map[string]any{"branches": map[string]any{"ignore": "main"}}) {
		t.Errorf("nightly deploy filters = %v, want the anchored filter", got)
	}
}

// Expected: each traced entry carries exactly one collect step, also when it inherits another entry's arguments.
func TestRobustness_TraceDoesNotDuplicateSharedArgs(t *testing.T) {
	cfg := rbFixture(t, "merge_traced.yml")
	res := rbRun(t, rbCase{
		cfg:    cfg,
		base:   map[string]string{"services/api/main.go": "package api\n"},
		branch: "main",
		change: rbEdit("services/api/main.go", "package api\n\n// changed\n"),
		want: rbWant{full: true, running: []string{"lint", "test"},
			emitted: []string{"lint", "test", "smarter-impact-map", "smarter-gate"}, gate: []string{"lint", "test"}},
	})
	doc := rbDoc(t, res.Config)
	for _, name := range []string{"lint", "test"} {
		if n := rbCount(rbSteps(t, rbEntryBody(t, doc, "ci", name)["post-steps"]), rbCollect); n != 1 {
			t.Errorf("%s has %d collect steps, want 1", name, n)
		}
	}
	if got := rbEntryBody(t, doc, "ci", "test")["context"]; got != "shared-ctx" {
		t.Errorf("test context = %v, want the inherited shared-ctx", got)
	}
}

// Expected: a job outside the traced workflow keeps its definition, even when it shares steps with a traced job.
func TestRobustness_TraceLeavesUntracedJobsAlone(t *testing.T) {
	cfg := rbFixture(t, "shared_steps.yml")
	res := rbRun(t, rbCase{
		cfg:    cfg,
		base:   map[string]string{"services/api/main.go": "package api\n"},
		branch: "main",
		change: rbEdit("services/api/main.go", "package api\n\n// changed\n"),
		want: rbWant{full: true, running: []string{"lint"},
			emitted: []string{"lint", "smarter-impact-map", "smarter-gate"}, gate: []string{"lint"}},
	})
	if got, want := rbSteps(t, rbJob(t, rbDoc(t, res.Config), "docs")["steps"]), []string{"checkout", "run"}; !slices.Equal(got, want) {
		t.Errorf("docs (not in the traced workflow) has steps %v, want %v", got, want)
	}
}

// Case 15 on the default branch: the map job waits for the traced jobs that run
// before any approval, and not for the ones behind it.
func TestRobustness_TraceAfterApprovalIsNotAwaited(t *testing.T) {
	cfg := rbFixture(t, "approval_middle.yml")
	res := rbRun(t, rbCase{
		cfg:    cfg,
		base:   map[string]string{"app/main.go": "package app\n", "deploy/run.sh": "#!/bin/sh\n"},
		branch: "main",
		change: rbEdit("app/main.go", "package app\n\n// changed\n"),
		want: rbWant{full: true, running: []string{"build", "hold", "deploy"},
			emitted: []string{"build", "hold", "deploy", "smarter-impact-map", "smarter-gate"}, gate: []string{"build"}},
	})
	if got := emittedEntry(t, res, "ci", planner.ImpactMapJob).Requires; !slices.Equal(got, []string{"build"}) {
		t.Errorf("the map job requires %v, want [build]", got)
	}
}

// Policy: the guardrail bundle still passes after pruning, repair, and a full run.
func TestRobustness_PolicyBundleStillPasses(t *testing.T) {
	cfg := rbFixture(t, "policy_pass.yml")
	base := map[string]string{
		"services/api/main.go": "package api\n", "go.mod": "module x\n", "app/x.go": "package app\n", "deploy/run.sh": "#!/bin/sh\n",
	}
	change := rbEdit("services/api/main.go", "package api\n\n// changed\n")
	reads := map[string]rbReads{
		"security-scan": {files: []string{"go.mod"}, dirs: []string{"."}},
		"lint":          {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
		"build":         {files: []string{"app/x.go"}, dirs: []string{"app"}},
		"deploy":        {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}},
	}
	t.Run("the scan is mandatory", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, change: change, reads: reads, mandatory: []string{"security-scan"}, checker: rbBundle(t),
			want: rbWant{running: []string{"security-scan", "lint"}, emitted: []string{"security-scan", "lint", "smarter-gate"}, gate: []string{"security-scan", "lint"}}})
	})
	t.Run("the planner repairs a scan that is not selected", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, change: change, reads: reads, checker: rbBundle(t),
			want: rbWant{running: []string{"security-scan", "lint"}, emitted: []string{"security-scan", "lint", "smarter-gate"}, gate: []string{"security-scan", "lint"}}})
		if got := res.Report.Guardrails.Repairs; !slices.Equal(got, []string{"require-job=security-scan"}) {
			t.Errorf("repairs = %v, want the scan forced once", got)
		}
	})
	t.Run("the default branch is a full run", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, change: change, reads: reads, branch: "main", checker: rbBundle(t),
			want: rbWant{full: true, running: []string{"security-scan", "lint", "build", "hold", "deploy"},
				emitted: []string{"security-scan", "lint", "build", "hold", "deploy", "smarter-impact-map", "smarter-gate"},
				gate:    []string{"security-scan", "lint", "build"}}})
	})
}

// Policy: the bundle requires a job that the project's own filters keep off the
// branch. The planner cannot make it run (it does not force a job), so the plan
// without it breaks the bundle, the repair does not take, and the planner falls
// back to a full run, which keeps every entry and so passes.
func TestRobustness_PolicyRequiredJobThatCannotRunGivesAFullRun(t *testing.T) {
	res := rbRun(t, rbCase{
		cfg:       rbFixture(t, "policy_scan_filtered.yml"),
		base:      map[string]string{"services/api/main.go": "package api\n"},
		change:    rbEdit("services/api/main.go", "package api\n\n// changed\n"),
		reads:     map[string]rbReads{"security-scan": {files: []string{"go.mod"}}, "lint": {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}}},
		mandatory: []string{"security-scan"},
		checker:   rbBundle(t),
		want:      rbWant{full: true, running: []string{"lint"}, emitted: []string{"security-scan", "lint", "smarter-gate"}, gate: []string{"lint"}},
	})
	if !strings.Contains(res.Report.FullReason, "the guardrails reject the plan and it cannot be repaired") {
		t.Errorf("full-run reason = %q", res.Report.FullReason)
	}
	if res.GuardrailsFailed || res.Report.Guardrails.Status != "PASS" {
		t.Errorf("guardrails failed = %v, status = %s: the full run keeps the job, so it passes", res.GuardrailsFailed, res.Report.Guardrails.Status)
	}
	if got := reasons(t, res, "security-scan"); len(got) != 2 || got[0] != "does not run: its filters leave out branch feature" {
		t.Errorf("security-scan reasons = %v: the report says why it does not run", got)
	}
}

// Every fixture is valid CircleCI config, so a failing case is the planner's doing.
func TestRobustness_FixturesAreValidCircleCI(t *testing.T) {
	names, err := filepath.Glob(filepath.Join("testdata", "robustness", "*.yml"))
	if err != nil || len(names) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, name := range names {
		t.Run(filepath.Base(name), func(t *testing.T) {
			rbValidate(t, rbFixture(t, filepath.Base(name)))
		})
	}
}

// ---- job types and job groups ----
//
// A no-op job, an approval and a release job run no steps, so they have no
// trace and no inputs of their own. The jobs around them decide: a no-op job
// or an approval is kept when a selected job requires it, a release job when a
// job it requires is selected. A job group is one unit that always runs.

// rbEntryHas reports whether the entry of workflow wf has the key.
func rbEntryHas(t *testing.T, cfg []byte, wf, name, key string) bool {
	t.Helper()
	_, ok := rbEntryBody(t, rbDoc(t, cfg), wf, name)[key]
	return ok
}

// A no-op job between the tests and the deploy is a structural job: the
// closure keeps it when the deploy is selected, and drops it when nothing that
// requires it is. It is not traced, and the gate does not wait for it, since it
// can only fail with the jobs it requires.
func TestRobustness_NoOpCoordinatorIsDecidedByTheClosure(t *testing.T) {
	cfg := rbFixture(t, "noop_coordinator.yml")
	base := map[string]string{"unit/x.go": "package unit\n", "integration/x.go": "package integration\n", "deploy/run.sh": "#!/bin/sh\n", "notes/readme.md": "# notes\n"}
	reads := map[string]rbReads{
		"test-unit":        {files: []string{"unit/x.go"}, dirs: []string{"unit"}},
		"test-integration": {files: []string{"integration/x.go"}, dirs: []string{"integration"}},
		"deploy":           {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}},
	}
	t.Run("the change reaches the job after the no-op job", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("deploy/run.sh", "#!/bin/sh\n# changed\n"),
			want: rbWant{running: []string{"test-unit", "test-integration", "tests-done", "deploy"},
				emitted: []string{"test-unit", "test-integration", "tests-done", "deploy", "smarter-gate"},
				gate:    []string{"test-unit", "test-integration", "deploy"}}})
		if got := reasons(t, res, "tests-done"); !slices.Equal(got, []string{"required by deploy"}) {
			t.Errorf("tests-done reasons = %v", got)
		}
		if got := emittedEntry(t, res, "ci", "tests-done").Requires; !slices.Equal(got, []string{"test-unit", "test-integration"}) {
			t.Errorf("tests-done requires %v: the plan must not edit it", got)
		}
	})
	t.Run("the change reaches a job before it, and nothing that requires it", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("unit/x.go", "package unit\n\n// changed\n"),
			want: rbWant{running: []string{"test-unit"}, emitted: []string{"test-unit", "smarter-gate"}, gate: []string{"test-unit"}}})
		if got := reasons(t, res, "tests-done"); !slices.Equal(got, []string{"no selected job requires this no-op job"}) {
			t.Errorf("tests-done reasons = %v", got)
		}
	})
	t.Run("the change reaches nothing", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("notes/readme.md", "# notes\n\nmore\n"),
			want: rbWant{emitted: []string{"smarter-gate"}}})
	})
	t.Run("on the default branch it is not traced, and the map job does not wait for it", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, branch: "main", change: rbEdit("deploy/run.sh", "#!/bin/sh\n# changed\n"),
			want: rbWant{full: true, running: []string{"test-unit", "test-integration", "tests-done", "deploy"},
				emitted: []string{"test-unit", "test-integration", "tests-done", "deploy", "smarter-impact-map", "smarter-gate"},
				gate:    []string{"test-unit", "test-integration", "deploy"}}})
		wantTraced := []string{"test-unit", "test-integration", "deploy"}
		if got := res.Report.Trace.Jobs; !slices.Equal(got, wantTraced) {
			t.Errorf("traced jobs = %v, want %v", got, wantTraced)
		}
		if got := emittedEntry(t, res, "ci", planner.ImpactMapJob).Requires; !slices.Equal(got, wantTraced) {
			t.Errorf("the map job requires %v, want %v", got, wantTraced)
		}
		doc := rbDoc(t, res.Config)
		if rbEntryHas(t, res.Config, "ci", "tests-done", "post-steps") || rbEntryHas(t, res.Config, "ci", "tests-done", "pre-steps") {
			t.Error("the no-op job has trace steps")
		}
		if got := rbJob(t, doc, "tests-done"); !reflect.DeepEqual(got, map[string]any{"type": "no-op"}) {
			t.Errorf("the no-op job's definition is %v, want it as it was", got)
		}
	})
}

// A no-op job that nothing requires is a gatekeeper: a project uses it as the
// one required status check of its pipeline. Select mode keeps it, whatever is
// selected, or the check would wait for ever. It waits only for what stays, as
// the gate does, and for the gate when nothing it waited for stays, so that it
// never passes before the selected jobs have.
func TestRobustness_LeafNoOpGatekeeperIsKept(t *testing.T) {
	cfg := rbFixture(t, "noop_gatekeeper_three.yml")
	base := map[string]string{
		"a/x.go": "package a\n", "b/x.go": "package b\n", "c/x.go": "package c\n", "scan/x.sh": "#!/bin/sh\n",
		"shared/config.json": "{}\n", "docs/guide.md": "# guide\n",
	}
	reads := map[string]rbReads{
		"test-a":        {files: []string{"a/x.go", "shared/config.json"}, dirs: []string{"a", "shared"}},
		"test-b":        {files: []string{"b/x.go", "shared/config.json"}, dirs: []string{"b", "shared"}},
		"test-c":        {files: []string{"c/x.go", "shared/config.json"}, dirs: []string{"c", "shared"}},
		"security-scan": {files: []string{"scan/x.sh"}, dirs: []string{"scan"}},
	}
	const why = "kept: a no-op gatekeeper"
	gatekeeperRequires := func(t *testing.T, res *planner.Result) []string {
		t.Helper()
		return emittedEntry(t, res, "ci", "all-tests-passed").Requires
	}
	t.Run("one of three tests is reached", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("b/x.go", "package b\n\n// changed\n"),
			want: rbWant{running: []string{"test-b", "all-tests-passed"}, emitted: []string{"test-b", "all-tests-passed", "smarter-gate"}, gate: []string{"test-b"}}})
		if got := gatekeeperRequires(t, res); !slices.Equal(got, []string{"test-b"}) {
			t.Errorf("all-tests-passed requires %v, want [test-b]", got)
		}
		if got := reasons(t, res, "all-tests-passed"); !slices.Equal(got, []string{why}) {
			t.Errorf("all-tests-passed reasons = %v", got)
		}
		if got := rbJob(t, rbDoc(t, res.Config), "all-tests-passed"); !reflect.DeepEqual(got, map[string]any{"type": "no-op"}) {
			t.Errorf("the gatekeeper's definition is %v, want it as it was", got)
		}
	})
	t.Run("every test is reached", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("shared/config.json", "{\"changed\": true}\n"),
			want: rbWant{running: []string{"test-a", "test-b", "test-c", "all-tests-passed"},
				emitted: []string{"test-a", "test-b", "test-c", "all-tests-passed", "smarter-gate"}, gate: []string{"test-a", "test-b", "test-c"}}})
		if got := gatekeeperRequires(t, res); !slices.Equal(got, []string{"test-a", "test-b", "test-c"}) {
			t.Errorf("all-tests-passed requires %v: nothing it waited for is gone, so it is as it was", got)
		}
	})
	t.Run("no test is reached: it waits for the gate", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("docs/guide.md", "# guide\n\nmore\n"),
			want: rbWant{running: []string{"all-tests-passed"}, emitted: []string{"all-tests-passed", "smarter-gate"}, gate: nil}})
		if got := gatekeeperRequires(t, res); !slices.Equal(got, []string{"smarter-gate"}) {
			t.Errorf("all-tests-passed requires %v, want [smarter-gate]", got)
		}
		if got := reasons(t, res, "all-tests-passed"); !slices.Equal(got, []string{why}) {
			t.Errorf("all-tests-passed reasons = %v", got)
		}
	})
	t.Run("no test is reached, and a mandatory job runs: the gatekeeper waits for it through the gate", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("docs/guide.md", "# guide\n\nmore\n"), mandatory: []string{"security-scan"},
			want: rbWant{running: []string{"security-scan", "all-tests-passed"},
				emitted: []string{"security-scan", "all-tests-passed", "smarter-gate"}, gate: []string{"security-scan"}}})
		if got := gatekeeperRequires(t, res); !slices.Equal(got, []string{"smarter-gate"}) {
			t.Errorf("all-tests-passed requires %v, want [smarter-gate]", got)
		}
	})
	t.Run("on the default branch every job stays, as it was", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, branch: "main", change: rbEdit("docs/guide.md", "# guide\n\nmore\n"),
			want: rbWant{full: true, running: []string{"test-a", "test-b", "test-c", "security-scan", "all-tests-passed"},
				emitted: []string{"test-a", "test-b", "test-c", "security-scan", "all-tests-passed", "smarter-impact-map", "smarter-gate"},
				gate:    []string{"test-a", "test-b", "test-c", "security-scan"}}})
		if got := gatekeeperRequires(t, res); !slices.Equal(got, []string{"test-a", "test-b", "test-c"}) {
			t.Errorf("all-tests-passed requires %v", got)
		}
		if rbEntryHas(t, res.Config, "ci", "all-tests-passed", "post-steps") {
			t.Error("the gatekeeper has trace steps")
		}
	})
	t.Run("shadow mode keeps every job and says what select mode would do", func(t *testing.T) {
		rr := rbChange(t, base, "feature", rbEdit("b/x.go", "package b\n\n// changed\n"))
		o := rbOpts("ci", "feature", rr)
		o.Mode = planner.ModeShadow
		in := planner.Input{Config: cfg, Map: rbMap(rr.base, reads), Repo: rr.git, CommitMessage: "change"}
		res, err := planner.Run(context.Background(), in, o)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := emittedJobs(t, res, "ci"), []string{"test-a", "test-b", "test-c", "security-scan", "all-tests-passed", "smarter-gate"}; !slices.Equal(got, want) {
			t.Errorf("emitted jobs = %v, want %v", got, want)
		}
		if got := gatekeeperRequires(t, res); !slices.Equal(got, []string{"test-a", "test-b", "test-c"}) {
			t.Errorf("all-tests-passed requires %v: shadow mode edits nothing", got)
		}
		if got := running(res); !sameSet(got, []string{"test-b", "all-tests-passed"}) {
			t.Errorf("select mode would run %v", got)
		}
		if got := reasons(t, res, "all-tests-passed"); !slices.Equal(got, []string{why}) {
			t.Errorf("all-tests-passed reasons = %v", got)
		}
		rbValidate(t, res.Config)
	})
}

// A gatekeeper is kept only where it runs. It waits for the jobs that stay, and a
// job that its branch filters leave out is not one of them. One that waits only
// for jobs the filters leave out does not run on the branch (CircleCI runs a job
// when any job it requires runs), so it is dropped, not made to run.
func TestRobustness_GatekeeperAndFilters(t *testing.T) {
	cfg := rbFixture(t, "noop_gatekeeper_filtered.yml")
	base := map[string]string{"app/main.go": "package app\n", "docs/guide.md": "# guide\n", "release/publish.sh": "#!/bin/sh\n"}
	reads := map[string]rbReads{
		"lint":    {files: []string{"app/main.go"}, dirs: []string{"app"}},
		"publish": {files: []string{"release/publish.sh"}, dirs: []string{"release"}},
	}
	t.Run("a branch, and the change reaches the job it waits for", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("app/main.go", "package app\n\n// changed\n"),
			want: rbWant{running: []string{"lint", "release-ok"}, emitted: []string{"lint", "release-ok", "smarter-gate"}, gate: []string{"lint"}}})
		if got := emittedEntry(t, res, "ci", "release-ok").Requires; !slices.Equal(got, []string{"lint"}) {
			t.Errorf("release-ok requires %v, want [lint]: publish does not run on a branch", got)
		}
		if got := reasons(t, res, "main-ok"); !slices.Equal(got, []string{"does not run: it requires publish, which does not run"}) {
			t.Errorf("main-ok reasons = %v", got)
		}
	})
	t.Run("a branch, and the change reaches nothing", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("docs/guide.md", "# guide\n\nmore\n"),
			want: rbWant{running: []string{"release-ok"}, emitted: []string{"release-ok", "smarter-gate"}}})
		if got := emittedEntry(t, res, "ci", "release-ok").Requires; !slices.Equal(got, []string{"smarter-gate"}) {
			t.Errorf("release-ok requires %v, want [smarter-gate]", got)
		}
	})
	t.Run("the default branch runs everything, as it was", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, branch: "main", change: rbEdit("docs/guide.md", "# guide\n\nmore\n"),
			want: rbWant{full: true, running: []string{"lint", "publish", "release-ok", "main-ok"},
				emitted: []string{"lint", "publish", "release-ok", "main-ok", "smarter-impact-map", "smarter-gate"}, gate: []string{"lint", "publish"}}})
		if got := emittedEntry(t, res, "ci", "release-ok").Requires; !slices.Equal(got, []string{"lint", "publish"}) {
			t.Errorf("release-ok requires %v", got)
		}
	})
}

// An approval set in the job's definition (`hold: {type: approval}` under jobs)
// is an approval, as one set on the workflow entry is: the closure decides it,
// the gate does not wait for it or for what follows it, and it is not traced.
func TestRobustness_ApprovalDefinedUnderJobs(t *testing.T) {
	cfg := rbFixture(t, "approval_defined.yml")
	base := map[string]string{"app/main.go": "package app\n", "deploy/run.sh": "#!/bin/sh\n"}
	reads := map[string]rbReads{
		"build":  {files: []string{"app/main.go"}, dirs: []string{"app"}},
		"deploy": {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}},
	}
	t.Run("the change reaches the build only", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("app/main.go", "package app\n\n// changed\n"),
			want: rbWant{running: []string{"build"}, emitted: []string{"build", "smarter-gate"}, gate: []string{"build"}}})
	})
	t.Run("the change reaches the deploy: the approval and the build stay", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("deploy/run.sh", "#!/bin/sh\n# changed\n"),
			want: rbWant{running: []string{"build", "hold", "deploy"}, emitted: []string{"build", "hold", "deploy", "smarter-gate"}, gate: []string{"build"}}})
		if got := reasons(t, res, "hold"); !slices.Equal(got, []string{"required by deploy"}) {
			t.Errorf("hold reasons = %v", got)
		}
	})
	t.Run("on the default branch the map job waits for what runs before the approval", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, branch: "main", change: rbEdit("app/main.go", "package app\n\n// changed\n"),
			want: rbWant{full: true, running: []string{"build", "hold", "deploy"},
				emitted: []string{"build", "hold", "deploy", "smarter-impact-map", "smarter-gate"}, gate: []string{"build"}}})
		if got := emittedEntry(t, res, "ci", planner.ImpactMapJob).Requires; !slices.Equal(got, []string{"build"}) {
			t.Errorf("the map job requires %v, want [build]", got)
		}
		if got := res.Report.Trace.Jobs; !slices.Equal(got, []string{"build", "deploy"}) {
			t.Errorf("traced jobs = %v, want [build deploy]", got)
		}
		if rbEntryHas(t, res.Config, "ci", "hold", "post-steps") {
			t.Error("the approval has trace steps")
		}
	})
}

// A release job tracks the release of the job it requires: it is kept when that
// job is selected, and left out otherwise. It is not traced, and the gate waits
// for it, since a release can fail its validation.
func TestRobustness_ReleaseJobFollowsItsDeploy(t *testing.T) {
	cfg := rbFixture(t, "release_job.yml")
	base := map[string]string{"app/main.go": "package app\n", "api/x.go": "package api\n", "web/x.ts": "export {};\n"}
	reads := map[string]rbReads{
		"lint":       {files: []string{"app/main.go"}, dirs: []string{"app"}},
		"deploy-api": {files: []string{"api/x.go"}, dirs: []string{"api"}},
		"deploy-web": {files: []string{"web/x.ts"}, dirs: []string{"web"}},
	}
	t.Run("one deploy is selected", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("api/x.go", "package api\n\n// changed\n"),
			want: rbWant{running: []string{"lint", "deploy-api", "release-api"},
				emitted: []string{"lint", "deploy-api", "release-api", "smarter-gate"}, gate: []string{"lint", "deploy-api", "release-api"}}})
		if got := reasons(t, res, "release-api"); !slices.Equal(got, []string{"tracks the release of deploy-api"}) {
			t.Errorf("release-api reasons = %v", got)
		}
		if got := reasons(t, res, "release-web"); !slices.Equal(got, []string{"none of the jobs it requires is selected"}) {
			t.Errorf("release-web reasons = %v", got)
		}
		if got := rbJob(t, rbDoc(t, res.Config), "release-api"); !reflect.DeepEqual(got, map[string]any{"type": "release", "plan_name": "api-release"}) {
			t.Errorf("the release job's definition is %v, want it as it was", got)
		}
	})
	t.Run("both deploys are selected", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("app/main.go", "package app\n\n// changed\n"),
			want: rbWant{running: []string{"lint"}, emitted: []string{"lint", "smarter-gate"}, gate: []string{"lint"}}})
	})
	t.Run("nothing reaches a deploy", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("docs/guide.md", "# guide\n"),
			want: rbWant{emitted: []string{"smarter-gate"}}})
	})
	t.Run("on the default branch no release job is traced", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, branch: "main", change: rbEdit("app/main.go", "package app\n\n// changed\n"),
			want: rbWant{full: true, running: []string{"lint", "deploy-api", "release-api", "deploy-web", "release-web"},
				emitted: []string{"lint", "deploy-api", "release-api", "deploy-web", "release-web", "smarter-impact-map", "smarter-gate"},
				gate:    []string{"lint", "deploy-api", "release-api", "deploy-web", "release-web"}}})
		wantTraced := []string{"lint", "deploy-api", "deploy-web"}
		if got := res.Report.Trace.Jobs; !slices.Equal(got, wantTraced) {
			t.Errorf("traced jobs = %v, want %v", got, wantTraced)
		}
		if got := emittedEntry(t, res, "ci", planner.ImpactMapJob).Requires; !slices.Equal(got, wantTraced) {
			t.Errorf("the map job requires %v, want %v", got, wantTraced)
		}
		for _, name := range []string{"release-api", "release-web"} {
			if rbEntryHas(t, res.Config, "ci", name, "post-steps") {
				t.Errorf("%s has trace steps", name)
			}
		}
	})
}

// A workflow entry that names a job group is one opaque unit in v0.1: it always
// runs, with the jobs it requires, whatever the change is. It has no trace, the
// report says why it runs, and its requires, its serial-group and the job-groups
// key come out as they went in.
func TestRobustness_JobGroupIsAnOpaqueUnitThatAlwaysRuns(t *testing.T) {
	cfg := rbFixture(t, "job_group.yml")
	base := map[string]string{"app/main.go": "package app\n", "notify/run.sh": "#!/bin/sh\n", "docs/guide.md": "# guide\n"}
	reads := map[string]rbReads{
		"lint":   {files: []string{"app/main.go"}, dirs: []string{"app"}},
		"build":  {files: []string{"app/main.go"}, dirs: []string{"app"}},
		"notify": {files: []string{"notify/run.sh"}, dirs: []string{"notify"}},
	}
	const serialGroup = "<< pipeline.project.slug >>/deploy-group"
	checkUnit := func(t *testing.T, res *planner.Result) {
		t.Helper()
		doc := rbDoc(t, res.Config)
		body := rbEntryBody(t, doc, "ci", "deploy-and-verify")
		if got := body["serial-group"]; got != serialGroup {
			t.Errorf("the group's serial-group = %v, want %q", got, serialGroup)
		}
		if got := body["requires"]; !reflect.DeepEqual(got, []any{"build"}) {
			t.Errorf("the group requires %v, want [build]", got)
		}
		if got, want := doc["job-groups"], rbDoc(t, cfg)["job-groups"]; !reflect.DeepEqual(got, want) {
			t.Errorf("job-groups = %v, want it as it was: %v", got, want)
		}
		if body["pre-steps"] != nil || body["post-steps"] != nil {
			t.Errorf("the group has trace steps: %v", body)
		}
		noNewExpressions(t, cfg, res.Config)
	}
	t.Run("a change that reaches no job", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("docs/guide.md", "# guide\n\nmore\n"),
			want: rbWant{running: []string{"lint", "build", "deploy-and-verify"},
				emitted: []string{"lint", "build", "deploy-and-verify", "smarter-gate"}, gate: []string{"lint", "build", "deploy-and-verify"}}})
		want := "job group: runs as one unit, as v0.1 does not look inside it"
		if got := reasons(t, res, "deploy-and-verify"); !slices.Equal(got, []string{want}) {
			t.Errorf("the group's reasons = %v, want [%s]", got, want)
		}
		if got := reasons(t, res, "lint"); !slices.Equal(got, []string{"required by build"}) {
			t.Errorf("lint reasons = %v", got)
		}
		checkUnit(t, res)
	})
	t.Run("a change that reaches the job after it", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("notify/run.sh", "#!/bin/sh\n# changed\n"),
			want: rbWant{running: []string{"lint", "build", "deploy-and-verify", "notify"},
				emitted: []string{"lint", "build", "deploy-and-verify", "notify", "smarter-gate"}, gate: []string{"lint", "build", "deploy-and-verify", "notify"}}})
		checkUnit(t, res)
	})
	t.Run("on the default branch it is not traced, and the map job does not wait for it", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, branch: "main", change: rbEdit("docs/guide.md", "# guide\n\nmore\n"),
			want: rbWant{full: true, running: []string{"lint", "build", "deploy-and-verify", "notify"},
				emitted: []string{"lint", "build", "deploy-and-verify", "notify", "smarter-impact-map", "smarter-gate"},
				gate:    []string{"lint", "build", "deploy-and-verify", "notify"}}})
		wantTraced := []string{"lint", "build", "notify"}
		if got := res.Report.Trace.Jobs; !slices.Equal(got, wantTraced) {
			t.Errorf("traced jobs = %v, want %v", got, wantTraced)
		}
		if got := emittedEntry(t, res, "ci", planner.ImpactMapJob).Requires; !slices.Equal(got, wantTraced) {
			t.Errorf("the map job requires %v, want %v", got, wantTraced)
		}
		checkUnit(t, res)
	})
}

// A group invoked twice under two names is two units. The invocation that its
// own filters leave out on the branch does not run, and what requires only it
// goes with it, as for any job.
func TestRobustness_JobGroupInvokedTwiceAndFiltered(t *testing.T) {
	cfg := rbFixture(t, "job_group_filtered.yml")
	base := map[string]string{"app/main.go": "package app\n", "notify/run.sh": "#!/bin/sh\n"}
	reads := map[string]rbReads{
		"lint":   {files: []string{"app/main.go"}, dirs: []string{"app"}},
		"notify": {files: []string{"notify/run.sh"}, dirs: []string{"notify"}},
	}
	change := rbEdit("notify/run.sh", "#!/bin/sh\n# changed\n")
	t.Run("on a branch", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: change,
			want: rbWant{running: []string{"lint", "deploy-staging"}, emitted: []string{"lint", "deploy-staging", "smarter-gate"}, gate: []string{"lint", "deploy-staging"}}})
		if got := reasons(t, res, "deploy-production"); !slices.Equal(got, []string{"does not run: its filters leave out branch feature"}) {
			t.Errorf("deploy-production reasons = %v", got)
		}
		if got := rbEntryBody(t, rbDoc(t, res.Config), "ci", "deploy-staging")["serial-group"]; got != "<< pipeline.project.slug >>/staging" {
			t.Errorf("deploy-staging serial-group = %v", got)
		}
	})
	t.Run("on the default branch", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, branch: "main", change: change,
			want: rbWant{full: true, running: []string{"lint", "deploy-staging", "deploy-production", "notify"},
				emitted: []string{"lint", "deploy-staging", "deploy-production", "notify", "smarter-impact-map", "smarter-gate"},
				gate:    []string{"lint", "deploy-staging", "deploy-production", "notify"}}})
		doc := rbDoc(t, res.Config)
		for name, want := range map[string]string{"deploy-staging": "<< pipeline.project.slug >>/staging", "deploy-production": "<< pipeline.project.slug >>/production"} {
			if got := rbEntryBody(t, doc, "ci", name)["serial-group"]; got != want {
				t.Errorf("%s serial-group = %v, want %q", name, got, want)
			}
		}
	})
}

// A serial-group key of a workflow entry survives the emit, on the entries that
// stay, in a plan and in a traced full run.
func TestRobustness_SerialGroupSurvivesTheEmit(t *testing.T) {
	cfg := rbFixture(t, "serial_group.yml")
	base := map[string]string{"app/main.go": "package app\n", "deploy/run.sh": "#!/bin/sh\n", "docs/guide.md": "# guide\n"}
	reads := map[string]rbReads{
		"lint":   {files: []string{"app/main.go"}, dirs: []string{"app"}},
		"build":  {files: []string{"app/main.go"}, dirs: []string{"app"}},
		"deploy": {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}},
	}
	want := map[string]string{"build": "<< pipeline.project.slug >>/build-group", "deploy": "deploy/production"}
	check := func(t *testing.T, res *planner.Result, kept ...string) {
		t.Helper()
		doc := rbDoc(t, res.Config)
		for _, name := range kept {
			if got := rbEntryBody(t, doc, "ci", name)["serial-group"]; got != want[name] {
				t.Errorf("%s serial-group = %v, want %q", name, got, want[name])
			}
		}
		noNewExpressions(t, cfg, res.Config)
	}
	t.Run("a plan that keeps both", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("deploy/run.sh", "#!/bin/sh\n# changed\n"),
			want: rbWant{running: []string{"lint", "build", "deploy"}, emitted: []string{"lint", "build", "deploy", "smarter-gate"}, gate: []string{"lint", "build", "deploy"}}})
		check(t, res, "build", "deploy")
	})
	t.Run("a plan that keeps one", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: rbEdit("app/main.go", "package app\n\n// changed\n"),
			want: rbWant{running: []string{"lint", "build"}, emitted: []string{"lint", "build", "smarter-gate"}, gate: []string{"lint", "build"}}})
		check(t, res, "build")
	})
	t.Run("a traced full run", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, branch: "main", change: rbEdit("docs/guide.md", "# guide\n\nmore\n"),
			want: rbWant{full: true, running: []string{"lint", "build", "deploy"},
				emitted: []string{"lint", "build", "deploy", "smarter-impact-map", "smarter-gate"}, gate: []string{"lint", "build", "deploy"}}})
		check(t, res, "build", "deploy")
		// The trace steps are added next to the key, in the same entry.
		doc := rbDoc(t, res.Config)
		if rbCount(rbSteps(t, rbEntryBody(t, doc, "ci", "build")["post-steps"]), rbCollect) != 1 {
			t.Error("build has no single collect step")
		}
	})
}

// Policy: the restricted-context rule of the bundle reads an approval that is
// set in the job's definition, so a plan of such a config passes it, on a branch
// (the deploy is left out) and on main (the deploy stays, behind the approval).
func TestRobustness_PolicyBundleReadsAnApprovalDefinedUnderJobs(t *testing.T) {
	cfg := rbFixture(t, "policy_defined_approval.yml")
	base := map[string]string{"app/main.go": "package app\n", "go.mod": "module x\n", "deploy/run.sh": "#!/bin/sh\n"}
	reads := map[string]rbReads{
		"security-scan": {files: []string{"go.mod"}, dirs: []string{"."}},
		"build":         {files: []string{"app/main.go"}, dirs: []string{"app"}},
		"deploy":        {files: []string{"deploy/run.sh"}, dirs: []string{"deploy"}},
	}
	change := rbEdit("app/main.go", "package app\n\n// changed\n")
	t.Run("on a branch", func(t *testing.T) {
		rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, change: change, mandatory: []string{"security-scan"}, checker: rbBundle(t),
			want: rbWant{running: []string{"security-scan", "build"}, emitted: []string{"security-scan", "build", "smarter-gate"}, gate: []string{"security-scan", "build"}}})
	})
	t.Run("on the default branch", func(t *testing.T) {
		res := rbRun(t, rbCase{cfg: cfg, base: base, reads: reads, branch: "main", change: change, mandatory: []string{"security-scan"}, checker: rbBundle(t),
			want: rbWant{full: true, running: []string{"security-scan", "build", "hold", "deploy"},
				emitted: []string{"security-scan", "build", "hold", "deploy", "smarter-impact-map", "smarter-gate"}, gate: []string{"security-scan", "build"}}})
		if res.GuardrailsFailed || res.Report.Guardrails.Status != "PASS" {
			t.Errorf("guardrails failed = %v, status = %s: the deploy is behind an approval", res.GuardrailsFailed, res.Report.Guardrails.Status)
		}
	})
}

// A job that persists a workspace in the post-steps of its workflow entry
// persists it as much as one that does so in its definition: the job that
// attaches it is selected with it.
func TestRobustness_WorkspacePersistedInAnEntrysPostSteps(t *testing.T) {
	res := rbRun(t, rbCase{
		cfg:    rbFixture(t, "entry_steps_workspace.yml"),
		base:   map[string]string{"src/x.go": "package src\n", "check/rules.txt": "rules\n"},
		change: rbEdit("src/x.go", "package src\n\n// changed\n"),
		reads: map[string]rbReads{
			"compile": {files: []string{"src/x.go"}, dirs: []string{"src"}},
			"check":   {files: []string{"check/rules.txt"}, dirs: []string{"check"}},
		},
		want: rbWant{running: []string{"compile", "check"}, emitted: []string{"compile", "check", "smarter-gate"}, gate: []string{"compile", "check"}},
	})
	if got := reasons(t, res, "check"); !slices.Equal(got, []string{"consumes the workspace of compile"}) {
		t.Errorf("check reasons = %v", got)
	}
}
