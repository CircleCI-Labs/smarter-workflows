package planner

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/report"
)

// maxRepairRounds is how many times the guardrail loop re-plans after a repair
// (step 9.2).
const maxRepairRounds = 3

// Repo is what the planner asks git.
type Repo interface {
	// Changes lists the changed paths between two commits, renames split into a
	// delete and an add.
	Changes(ctx context.Context, base, head string) ([]diff.Change, error)
	// DirsExist reports which of the directories exist at the revision.
	DirsExist(ctx context.Context, rev string, dirs []string) (map[string]bool, error)
}

// Input is everything the planner reads.
type Input struct {
	// Config is the full continuation config (the catalog).
	Config []byte
	// Map is the impact map. When it is nil, MapProblem says why, and the plan
	// is a full run.
	Map        *impactmap.Map
	MapProblem string
	// Repo answers questions about the change. It may be nil when there is no
	// base.
	Repo Repo
	// CommitMessage is the head commit's message.
	CommitMessage string
	// Checker evaluates the guardrails; nil means there is no policy bundle.
	// GuardrailsProblem is set when a bundle was asked for but cannot be used:
	// the plan is a full run and the problem is recorded.
	Checker           guard.Checker
	GuardrailsProblem string
	// MapJobSummaries is optional per-job input totals from analyze (plan integration).
	MapJobSummaries map[string]string
	// AIAdded forces jobs into the selection with a reason (plan-time AI review).
	AIAdded map[string]string
	// MapFromAnalyze is set when the impact map was built in-process for this plan.
	MapFromAnalyze bool
}

// Result is the outcome of planning.
type Result struct {
	// Config is the continuation config to send.
	Config []byte
	// Report is plan.json.
	Report *report.Plan
	// Parameters are passed to continue unchanged when --parameters was set.
	Parameters map[string]any
	// GuardrailsFailed is set when even the full plan breaks a hard guardrail:
	// the pipeline must not continue (exit 3).
	GuardrailsFailed bool
}

// UsageError is a mistake in how the planner was called (an unknown workflow,
// bad settings), as opposed to a failure while planning.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Run plans one workflow. It fails only when the config cannot be read or the
// workflow cannot be chosen. Any later problem produces a full plan, with the
// reason in the report, and no error.
func Run(ctx context.Context, in Input, opts Options) (*Result, error) {
	if err := opts.Validate(); err != nil {
		return nil, &UsageError{Msg: err.Error()}
	}
	cfg, err := config.Parse(in.Config)
	if err != nil {
		return nil, err
	}
	name, err := ChooseWorkflow(cfg, opts.Workflow)
	if err != nil {
		return nil, err
	}
	p := &planner{in: in, o: opts, wf: name}
	return p.run(ctx), nil
}

// ChooseWorkflow picks the workflow to work on: the one that is named, or the
// only one. Anything else is a UsageError.
func ChooseWorkflow(cfg *config.Config, want string) (string, error) {
	names := cfg.Workflows()
	switch {
	case want != "":
		if !slices.Contains(names, want) {
			return "", &UsageError{Msg: fmt.Sprintf("the config has no workflow %q (it has: %s)", want, strings.Join(names, ", "))}
		}
		return want, nil
	case len(names) == 1:
		return names[0], nil
	case len(names) == 0:
		return "", &UsageError{Msg: "the config has no workflow with a job list"}
	}
	return "", &UsageError{Msg: fmt.Sprintf("the config has %d workflows (%s): name the one to plan with --workflow", len(names), strings.Join(names, ", "))}
}

type planner struct {
	in Input
	o  Options
	wf string

	changes     []diff.Change
	changesErr  error
	changesDone bool
}

// attempt is one candidate plan.
type attempt struct {
	cfg  []byte
	rep  *report.Plan
	full bool
}

// run is step 9 around the planning of steps 1 to 8.
func (p *planner) run(ctx context.Context) *Result {
	forced := map[string]bool{}
	var repairs, warnings []string
	var violations []report.Violation
	fullReason := ""
	status := "SKIPPED"
	checker := p.in.Checker

	if p.in.GuardrailsProblem != "" {
		checker = nil
		status = guard.Error
		fullReason = "the guardrails cannot be checked: " + p.in.GuardrailsProblem
		warnings = append(warnings, "guardrails: "+p.in.GuardrailsProblem+"; planned a full run without checking it")
	}

	failed := false
	rounds := 0
	var final *attempt
	for {
		a := p.plan(ctx, forced, fullReason)
		if checker == nil {
			final = a
			break
		}
		res, err := checker.Check(ctx, a.cfg)
		if err == nil && res.Status == guard.Error {
			err = errors.New("the policy check reported an error")
		}
		if err != nil {
			// The check itself broke. The user's own pipeline is the baseline the
			// guardrails protect, so fall back to the full plan, and say so.
			status = guard.Error
			warnings = append(warnings, fmt.Sprintf("guardrails: the check failed (%v)", err))
			if !a.full {
				a = p.plan(ctx, forced, "the guardrails could not be checked: "+err.Error())
			}
			final = a
			break
		}

		violations = convert(res.Violations)
		hard := hardViolations(res)
		if len(hard) == 0 {
			status = res.Status
			if status == "" {
				status = guard.Pass
			}
			for _, v := range res.Violations {
				warnings = append(warnings, fmt.Sprintf("guardrails (soft): %s: %s", v.Rule, oneLine(v.Message)))
			}
			final = a
			break
		}
		if a.full {
			status, failed, final = guard.HardFail, true, a
			break
		}
		var fresh []string
		for _, v := range hard {
			for _, name := range v.RequireJobs {
				if !forced[name] && !slices.Contains(fresh, name) {
					fresh = append(fresh, name)
				}
			}
		}
		if len(fresh) > 0 && rounds < maxRepairRounds {
			rounds++
			for _, name := range fresh {
				forced[name] = true
				repairs = append(repairs, "require-job="+name)
			}
			continue
		}
		fullReason = "the guardrails reject the plan and it cannot be repaired: " + oneLine(hard[0].Message)
	}

	rep := final.rep
	rep.Guardrails = report.Guardrails{Status: status, Repairs: repairs, Violations: violations}
	rep.Warnings = append(rep.Warnings, warnings...)
	out := &Result{Config: final.cfg, Report: rep, GuardrailsFailed: failed}
	if len(p.o.Parameters) > 0 {
		out.Parameters = p.o.Parameters
	}
	return out
}

func (p *planner) pipelineEnv() pipelineEnv {
	return pipelineEnv{
		Branch: p.o.Branch, Tag: p.o.Tag, TriggerType: p.o.TriggerType,
		Parameters: p.o.Parameters,
	}
}

// buildWorkflowSkipped leaves the planned workflow untouched and records that
// it will not run (when/unless is false for this pipeline).
func (p *planner) buildWorkflowSkipped(wf *config.Workflow, rep *report.Plan) *attempt {
	rep.WorkflowRuns = false
	rep.WorkflowSkipReason = "the workflow's when/unless is false for this pipeline"
	const reason = "workflow does not run"
	for _, e := range wf.Entries {
		rep.Jobs = append(rep.Jobs, report.Job{Name: e.Name, Run: false, Reasons: []string{reason}})
	}
	return &attempt{cfg: slices.Clone(p.in.Config), rep: rep, full: false}
}

func hardViolations(r guard.Result) []guard.Violation {
	var out []guard.Violation
	for _, v := range r.Violations {
		if v.Hard {
			out = append(out, v)
		}
	}
	if len(out) == 0 && r.Status == guard.HardFail {
		out = append(out, guard.Violation{Message: "the policy check failed", Hard: true})
	}
	return out
}

func convert(vs []guard.Violation) []report.Violation {
	out := make([]report.Violation, 0, len(vs))
	for _, v := range vs {
		out = append(out, report.Violation{Policy: v.Policy, Rule: v.Rule, Message: oneLine(v.Message), Hard: v.Hard})
	}
	return out
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// plan builds a plan. If building fails, it plans a full run instead and
// records why, or last passes the config through untouched.
func (p *planner) plan(ctx context.Context, forced map[string]bool, fullReason string) *attempt {
	a, err := p.build(ctx, forced, fullReason)
	if err == nil {
		return a
	}
	reason := "internal error: " + oneLine(err.Error())
	note := "internal error, planned a full run: " + oneLine(err.Error())
	if a, err2 := p.build(ctx, nil, reason); err2 == nil {
		a.rep.Warnings = append(a.rep.Warnings, note)
		return a
	}
	return p.verbatim(reason, note)
}

// verbatim is the last resort: the config as it came, with no gate.
func (p *planner) verbatim(reason, note string) *attempt {
	rep := p.newReport()
	rep.Full, rep.FullReason = true, reason
	if cfg, err := config.Parse(p.in.Config); err == nil {
		if wf, err := cfg.Workflow(p.wf); err == nil {
			for _, e := range wf.Entries {
				rep.Jobs = append(rep.Jobs, report.Job{Name: e.Name, Run: true, Reasons: []string{"full run"}})
			}
		}
	}
	rep.Warnings = append(rep.Warnings, note, "the config is passed through unchanged, without the gate job")
	return &attempt{cfg: p.in.Config, rep: rep, full: true}
}

func (p *planner) newReport() *report.Plan {
	rep := &report.Plan{
		Version:      report.Version,
		Mode:         string(p.o.Mode),
		Workflow:     p.wf,
		WorkflowRuns: true,
		Base:         p.o.Base,
		Head:         p.o.Head,
		Branch:       p.o.Branch,
		Guardrails:   report.Guardrails{Status: "SKIPPED"},
	}
	if m := p.in.Map; m != nil {
		rep.MapRevision, rep.MapCreatedAt = m.Revision, m.CreatedAt
	}
	return rep
}

func (p *planner) getChanges(ctx context.Context) ([]diff.Change, error) {
	if p.changesDone {
		return p.changes, p.changesErr
	}
	p.changesDone = true
	switch {
	case p.o.Base == "":
	case p.o.Head == "":
		p.changesErr = errors.New("there is no head commit")
	case p.in.Repo == nil:
		p.changesErr = errors.New("there is no repository to ask")
	default:
		p.changes, p.changesErr = p.in.Repo.Changes(ctx, p.o.Base, p.o.Head)
	}
	return p.changes, p.changesErr
}

func isScheduled(trigger string) bool {
	switch strings.ToLower(strings.TrimSpace(trigger)) {
	case "schedule", "scheduled", "scheduled_pipeline":
		return true
	}
	return false
}

// fullRunReason is step 1. It returns "" when the change gets a real plan.
func (p *planner) fullRunReason(wf *config.Workflow, cs *changeSet, changesErr error) string {
	o := p.o
	switch {
	case o.ForceFull:
		return "forced by --force-full"
	case o.Tag != "":
		return "tag " + o.Tag
	case o.Branch != "" && o.Branch == o.DefaultBranch:
		return "default branch " + o.Branch
	case isScheduled(o.TriggerType):
		return "scheduled pipeline"
	case diff.AsksForFullRun(p.in.CommitMessage):
		return "the head commit asks for a full run"
	case o.Base == "":
		if o.NoBaseReason != "" {
			return "no diff base: " + o.NoBaseReason
		}
		return "no diff base"
	case p.in.Map == nil && p.in.MapProblem != "" && !strings.HasPrefix(p.in.MapProblem, "there is no impact map"):
		return "no usable impact map: " + p.in.MapProblem
	case changesErr != nil:
		return "the changed files could not be listed: " + oneLine(changesErr.Error())
	}
	for _, c := range cs.all {
		if g, ok := matchAny(o.FullRunPaths, c.Path); ok {
			return fmt.Sprintf("changed path %s matches the full-run path %s", c.Path, g)
		}
	}
	for _, e := range wf.Entries {
		if e.Dynamic && !e.Matrix {
			return fmt.Sprintf("job %q has a name or requires that is an expression", e.Name)
		}
	}
	return ""
}

// build is steps 1 to 8: one plan, from a fresh parse of the config. A
// non-empty fullReason forces a full run.
func (p *planner) build(ctx context.Context, forced map[string]bool, fullReason string) (a *attempt, err error) {
	defer func() {
		if r := recover(); r != nil {
			a, err = nil, fmt.Errorf("planner panic: %v", r)
		}
	}()

	cfg, err := config.Parse(p.in.Config)
	if err != nil {
		return nil, err
	}
	wf, err := cfg.Workflow(p.wf)
	if err != nil {
		return nil, err
	}
	an, err := analyse(cfg, wf, p.o.Branch, p.o.Tag)
	if err != nil {
		return nil, err
	}

	rep := p.newReport()
	changes, changesErr := p.getChanges(ctx)
	cs := newChangeSet(changes, p.o.IgnorePaths)
	for _, c := range cs.all {
		rep.Changed = append(rep.Changed, report.Change{Path: c.Path, Status: c.Status})
	}

	full := fullReason
	if full == "" {
		switch evalWorkflowRuns(wf.Node(), p.pipelineEnv()) {
		case triNo:
			return p.buildWorkflowSkipped(wf, rep), nil
		case triUnknown:
			if workflowConditionUnknown(wf) {
				full = "the workflow has a when or unless that cannot be evaluated"
			}
		}
	}
	if full == "" {
		full = p.fullRunReason(wf, cs, changesErr)
	}
	rep.Full, rep.FullReason = full != "", full

	n := len(an.entries)
	var sel *selection
	if rep.Full {
		sel = &selection{selected: make([]bool, n), reasons: make([][]string, n), gatekeeper: make([]bool, n)}
		for i := range sel.selected {
			sel.selected[i], sel.reasons[i] = true, []string{"full run"}
		}
	} else {
		if err := cs.loadDirs(ctx, p.in.Repo, p.o.Base, p.o.Head); err != nil {
			return nil, err
		}
		sel = p.selectJobs(an, cs, forced)
		rep.Warnings = append(rep.Warnings, p.planWarnings(an)...)
		if m := p.in.Map; m != nil && !p.in.MapFromAnalyze && m.Revision != p.o.Base {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("the impact map was built at %s, and the diff base is %s", short(m.Revision), short(p.o.Base)))
		}
	}
	if m := p.in.Map; m != nil {
		rep.Warnings = append(rep.Warnings, m.Problems...)
	}

	removing := p.o.Mode == ModeSelect && !rep.Full
	kept := make([]bool, n)
	for i := range kept {
		kept[i] = !removing || sel.selected[i] || sel.gatekeeper[i]
	}

	// The report: what the plan runs. In shadow mode this is what select mode
	// would run, since every job stays.
	for i, e := range an.entries {
		job := report.Job{Name: e.Name, Run: (sel.selected[i] || sel.gatekeeper[i]) && an.will[i] != triNo}
		switch {
		case sel.gatekeeper[i]:
			job.Reasons = []string{gatekeeperReason}
		case sel.selected[i]:
			// What keeps a job from running comes first: it is the answer to "why not?".
			if an.will[i] == triNo {
				job.Reasons = append(job.Reasons, an.inertReason(i, p.o.Branch, p.o.Tag))
			}
			job.Reasons = append(job.Reasons, sel.reasons[i]...)
		default:
			job.Reasons = []string{an.skipReason(i, p.o.Branch, p.o.Tag)}
			if sum := p.in.MapJobSummaries[e.Name]; sum != "" && job.Reasons[0] == "inputs unchanged" {
				job.Reasons = append(job.Reasons, sum+"; none touched")
			}
		}
		rep.Jobs = append(rep.Jobs, job)
		if kept[i] && an.will[i] == triUnknown && filterVerdict(e.Filters, p.o.Branch, p.o.Tag) == triUnknown {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("job %s: its filters cannot be evaluated here, so it is left out of the gate", e.Name))
		}
		if kept[i] && an.will[i] != triNo && e.StatusRequires && !gateIgnores(e.Kind) && !an.afterApproval[i] {
			// A job that waits for a failure may never run, and the gate would
			// wait for it for ever.
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s does not wait for %s: it requires a job status other than success", p.o.GateJob, e.Name))
		}
	}

	// The gate waits for every kept job that is certain to run, except what
	// comes after an approval. It does not wait for an approval, or for a
	// no-op job: they run no steps, so they can only fail with the jobs they
	// require, and the gate waits for those.
	var gateRequires []string
	for i, e := range an.entries {
		if kept[i] && an.will[i] == triYes && !gateIgnores(e.Kind) && !an.afterApproval[i] {
			gateRequires = append(gateRequires, e.RequireName())
		}
	}

	var keptNames []string
	if removing {
		// CircleCI ignores a requirement on a job that does not run, so Prune may
		// drop the names of those jobs, and no others, from the jobs that stay.
		remove, inert := map[int]bool{}, map[int]bool{}
		goes := map[string]bool{} // the names of the entries that leave
		for i, e := range an.entries {
			if !kept[i] {
				remove[i], inert[i] = true, an.will[i] == triNo
				for _, name := range e.Names() {
					goes[name] = true
				}
			}
		}
		// A gatekeeper waits only for what stays, which is what it would wait for
		// were the project to run just this plan. If nothing it waited for stays,
		// it waits for the gate, so that it never passes before the jobs the plan
		// selects have.
		for i := range an.entries {
			if sel.gatekeeper[i] {
				if err := wf.RetainRequires(i, func(name string) bool { return !goes[name] }, p.o.GateJob); err != nil {
					return nil, err
				}
			}
		}
		if err := wf.Prune(remove, inert); err != nil {
			return nil, err
		}
	}
	for i, e := range an.entries {
		if kept[i] {
			keptNames = append(keptNames, e.Name)
		}
	}

	if err := p.addGate(cfg, wf, gateRequires); err != nil {
		return nil, err
	}
	keptNames = append(keptNames, p.o.GateJob)
	rep.Gate = &report.Gate{Name: p.o.GateJob, Requires: gateRequires}

	out, err := cfg.Marshal()
	if err != nil {
		return nil, err
	}
	if err := p.verify(out, keptNames, an); err != nil {
		return nil, err
	}
	return &attempt{cfg: out, rep: rep, full: rep.Full}, nil
}

// planWarnings reports settings that name jobs the workflow does not have, and
// mandatory jobs that do not run on this branch or tag. The planner cannot make
// them run: that would mean editing the filters, or the requirements, of the
// project's own pipeline.
func (p *planner) planWarnings(an *analysis) []string {
	var out []string
	for _, name := range p.o.Mandatory {
		i, ok := an.byName[name]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("mandatory job %s is not in workflow %s", name, p.wf))
		case an.will[i] == triNo:
			out = append(out, fmt.Sprintf("mandatory job %s %s; the plan leaves it out and does not force it",
				name, an.inertReason(i, p.o.Branch, p.o.Tag)))
		}
	}
	for _, name := range sortedKeys(p.o.ExtraInputs) {
		if _, ok := an.byName[name]; !ok {
			out = append(out, fmt.Sprintf("extra inputs name job %s, which is not in workflow %s", name, p.wf))
		}
	}
	return out
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	if sha == "" {
		return "unknown"
	}
	return sha
}

// verify re-reads what the planner emitted and checks the workflow holds the
// entries it meant to keep, and that no requires points at a job that is gone.
func (p *planner) verify(out []byte, wantNames []string, an *analysis) error {
	cfg, err := config.Parse(out)
	if err != nil {
		return fmt.Errorf("the emitted config does not parse: %w", err)
	}
	wf, err := cfg.Workflow(p.wf)
	if err != nil {
		return fmt.Errorf("the emitted config lost the workflow: %w", err)
	}
	got := map[string]bool{}
	var gotNames []string
	for _, e := range wf.Entries {
		for _, n := range e.Names() {
			got[n] = true
		}
		got[e.RequireName()] = true
		gotNames = append(gotNames, e.Name)
	}
	if !slices.Equal(gotNames, wantNames) {
		return fmt.Errorf("the emitted workflow has jobs %v, want %v", gotNames, wantNames)
	}
	for _, e := range wf.Entries {
		for _, r := range e.Requires {
			if _, existed := an.byName[r]; existed && !got[r] {
				return fmt.Errorf("job %s requires %s, which the plan removed", e.Name, r)
			}
		}
	}
	return nil
}
