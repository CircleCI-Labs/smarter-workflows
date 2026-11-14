package planner_test

// Robustness cases: unusual CircleCI configs, planned against temp git
// repositories and checked by rbInvariants. A test that fails here is a bug for
// the planner to fix; its comment says what the planner should do. A test that
// passes is a regression test. The configs are in testdata/robustness.

import (
	"bytes"
	"context"
	"fmt"
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
	full    bool
	running []string
	emitted []string
	gate    []string
}

// rbCase is one planned change.
type rbCase struct {
	cfg       []byte
	base      map[string]string
	branch    string // "feature" unless set; "main" is a full run
	change    func(r *difftest.Repo)
	reads     map[string]rbReads
	mandatory []string
	traceOrb  bool          // --trace-orb-jobs
	checker   guard.Checker // the policy bundle, or nil
	wf        string        // "ci" unless set
	want      rbWant
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
	o.TraceOrbJobs = c.traceOrb
	in := planner.Input{Config: c.cfg, Map: rbMap(rr.base, c.reads), Repo: rr.git, CommitMessage: "change"}
	in.Checker = c.checker
	res, err := planner.Run(context.Background(), in, o)
	if err != nil {
		t.Fatalf("planner.Run: %v", err)
	}
	rbCheckPlan(t, res, wf, c.want)
	rbInvariants(t, rbCheck{wf: wf, branch: branch, in: c.cfg, res: res, checker: c.checker})
	return res
}

// rbCheckPlan compares the plan with what the case expects.
func rbCheckPlan(t *testing.T, res *planner.Result, wf string, want rbWant) {
	t.Helper()
	if res.Report.Full != want.full {
		t.Errorf("full run = %v (%s), want %v", res.Report.Full, res.Report.FullReason, want.full)
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
//	(b) a job the output keeps keeps every requires ancestor it has in the input;
//	(c) the gate requires only jobs the report says run;
//	safety: the output never runs a job the input does not run on the branch;
//	report: in select mode, the report's running set is what the output runs;
//	(d) circleci config validate passes (with REQUIRE_CIRCLECI_CLI=1);
//	(e) an input that passes the policy bundle gives an output that passes it.
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
		for _, a := range rbAncestors(inEntries, inIdx, i) {
			if _, ok := outIdx[a.Name]; !ok {
				t.Errorf("(b) %s is kept, but its requirement %s is gone from the output", e.Name, a.Name)
			}
		}
	}

	// (c)
	inRuns, outRuns := rbOracle(inEntries, c.branch), rbOracle(outEntries, c.branch)
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

// rbAncestors lists the entries that entry i requires, directly or not.
func rbAncestors(entries []*config.Entry, idx map[string]int, i int) []*config.Entry {
	seen := map[int]bool{i: true}
	var out []*config.Entry
	queue := []int{i}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, r := range entries[cur].Requires {
			j, ok := idx[r]
			if !ok || seen[j] {
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

// A job runs only if every job it requires runs (spec step 5), and the planner
// never drops a requirement to make a job runnable. A mandatory job whose
// requirement the branch filters out does not run on the branch, so the plan
// leaves it out with what it requires, warns, and does not force it. Dropping
// an approval's edge instead would run the job without its approval.
func TestRobustness_MandatoryJobWithAFilteredRequirementIsLeftOutWithAWarning(t *testing.T) {
	t.Run("a job whose requirement is filtered out", func(t *testing.T) {
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
	t.Run("an approval that the branch filters out", func(t *testing.T) {
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

// A job whose required job is filtered out does not run, whatever else it
// requires and whatever changed in its inputs: it is not selected, and it leaves
// the plan with the job it requires. The gate does not wait for it. (CircleCI's
// reference ignores a filtered-out requirement while another one runs; the
// planner takes the stricter rule of spec step 5.)
func TestRobustness_JobWithAFilteredRequirementIsNotSelected(t *testing.T) {
	res := rbRun(t, rbCase{
		cfg:    rbFixture(t, "filtered_mixed.yml"),
		base:   map[string]string{"app/main.go": "package app\n", "build/main.sh": "#!/bin/sh\n"},
		change: rbEdit("app/main.go", "package app\n\n// changed\n"),
		reads: map[string]rbReads{
			"build-main": {files: []string{"build/main.sh"}, dirs: []string{"build"}},
			"test":       {files: []string{"app/main.go"}, dirs: []string{"app"}},
			"ship":       {files: []string{"app/main.go"}, dirs: []string{"app"}},
		},
		want: rbWant{running: []string{"test"}, emitted: []string{"test", "smarter-gate"}, gate: []string{"test"}},
	})
	if got := reasons(t, res, "ship"); len(got) != 1 || got[0] != "does not run: it requires build-main, which does not run" {
		t.Errorf("ship reasons = %v", got)
	}
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

// Case 7: a workflow with `when: << pipeline.parameters.x >>` gets a full run,
// keeps its `when`, and gets no new pipeline parameter or expression.
func TestRobustness_WhenParameterRunsEverything(t *testing.T) {
	cfg := rbFixture(t, "when_parameter.yml")
	res := rbRun(t, rbCase{
		cfg:    cfg,
		base:   map[string]string{"services/api/main.go": "package api\n", "app/x.go": "package app\n"},
		change: rbEdit("services/api/main.go", "package api\n\n// changed\n"),
		reads: map[string]rbReads{
			"lint": {files: []string{"services/api/main.go"}, dirs: []string{"services/api"}},
			"test": {files: []string{"app/x.go"}, dirs: []string{"app"}},
		},
		want: rbWant{full: true, running: []string{"lint", "test"}, emitted: []string{"lint", "test", "smarter-gate"}, gate: []string{"lint", "test"}},
	})
	if !strings.Contains(res.Report.FullReason, "when or unless") {
		t.Errorf("full-run reason = %q, want the when or unless reason", res.Report.FullReason)
	}
	noNewExpressions(t, cfg, res.Config)
	if got := rbWorkflow(t, rbDoc(t, res.Config), "ci")["when"]; got != "<< pipeline.parameters.run-ci >>" {
		t.Errorf("the workflow's when = %v, want it unchanged", got)
	}
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
