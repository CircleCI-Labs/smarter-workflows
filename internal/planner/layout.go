package planner

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"slices"
	"sort"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/catalog"
	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
)

// The layout check (spec: `layout check`) reads one workflow as a graph, with
// the same analysis the planner uses, and reports what would go wrong when it
// runs: a requirement that points nowhere, a cycle, a job that attaches a
// workspace nothing persists, a restricted context used without an approval,
// a mandatory job that is missing, and a gate that does not do its work. It
// plans nothing and edits nothing.

// Severities of a Finding.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Codes of the findings of CheckLayout. A finding of the policy bundle has the
// code "policy:" and the name of the rule.
const (
	CodeDuplicateName           = "duplicate-name"
	CodeDanglingRequires        = "dangling-requires"
	CodeCycle                   = "cycle"
	CodeConsumerBeforeProducer  = "consumer-before-producer"
	CodeRestrictedContext       = "restricted-context-without-approval"
	CodeMandatoryMissing        = "mandatory-missing"
	CodeGateMissing             = "gate-missing"
	CodeGateNotNoOp             = "gate-not-noop"
	CodeGateMissingRequirement  = "gate-missing-requirement"
	CodeGateAwaitsStatusJob     = "gate-awaits-status-job"
	CodeFilteredApprovalFootgun = "filtered-approval-footgun"
	// PolicyCodePrefix starts the code of a finding of the policy bundle.
	PolicyCodePrefix = "policy:"
)

const (
	// probeBranch and probeTag are names that no filter spells out. They stand
	// for every other branch and tag when the filters are tried.
	probeBranch = "smarter-probe-branch"
	probeTag    = "smarter-probe-tag"
	// maxRefCandidates caps how many branches, and how many tags, are tried.
	maxRefCandidates = 64
	// maxWitnesses caps the strings made from one regular expression.
	maxWitnesses = 6
	// maxHintedNames caps the names that a fix hint lists.
	maxHintedNames = 3
)

// Finding is one problem of a layout.
type Finding struct {
	// Code says what is wrong; see the Code constants.
	Code     string `json:"code"`
	Severity string `json:"severity"`
	// Job is the entry the finding is about: the one to edit. It is empty for a
	// finding of the policy bundle that names no job.
	Job     string `json:"job"`
	Message string `json:"message"`
	// FixHint says what to change.
	FixHint string `json:"fix_hint"`

	order int // the entry's place in the workflow, for sorting
}

// LayoutOptions are the settings of CheckLayout: the flags of `layout check`.
type LayoutOptions struct {
	// Workflow is the workflow to check; "" means the only one.
	Workflow string
	// Catalog describes jobs that the config does not define. It is optional:
	// without it, a job that the config does not show is unknown inside.
	Catalog *catalog.Catalog
	// Mandatory are the jobs the workflow must hold.
	Mandatory []string
	// RestrictedContexts are the contexts that only a job downstream of an
	// approval may use.
	RestrictedContexts []string
	// GateJob is the name of the gate. "" skips the checks of the gate.
	GateJob string
	// Checker evaluates the policy bundle on the config; nil skips it.
	Checker guard.Checker
}

// LayoutResult is the outcome of CheckLayout.
type LayoutResult struct {
	Workflow string
	// Findings are sorted: errors first, then by code and place in the workflow.
	Findings []Finding
}

// Errors counts the findings of severity error.
func (r *LayoutResult) Errors() int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			n++
		}
	}
	return n
}

// CheckLayout checks one workflow of a config. It fails when the config cannot
// be read, when the workflow cannot be chosen (a UsageError), or when the
// policy bundle cannot be evaluated. Findings are not failures.
func CheckLayout(ctx context.Context, data []byte, o LayoutOptions) (*LayoutResult, error) {
	cfg, err := config.Parse(data)
	if err != nil {
		return nil, err
	}
	name, err := ChooseWorkflow(cfg, o.Workflow)
	if err != nil {
		return nil, err
	}
	wf, err := cfg.Workflow(name)
	if err != nil {
		return nil, err
	}
	l := &layoutCheck{cfg: cfg, wf: wf, o: o}
	l.useCatalog()
	// No branch and no tag: the verdicts say "unknown", never "does not run", so
	// what the analysis answers (the ancestors, the approvals upstream) holds on
	// every branch.
	var clashes []nameClash
	l.an, clashes = buildAnalysis(cfg, wf, "", "")

	l.checkNames(clashes)
	l.checkRequires()
	l.checkCycles()
	l.checkWorkspaces()
	l.checkContexts()
	l.checkMandatory()
	l.checkGate()
	l.checkApprovalFilters()
	if o.Checker != nil {
		if err := l.checkPolicy(ctx, data); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(l.findings, func(a, b int) bool {
		fa, fb := l.findings[a], l.findings[b]
		switch {
		case fa.Severity != fb.Severity:
			return fa.Severity == SeverityError
		case fa.Code != fb.Code:
			return fa.Code < fb.Code
		}
		return fa.order < fb.order
	})
	return &LayoutResult{Workflow: name, Findings: l.findings}, nil
}

type layoutCheck struct {
	cfg *config.Config
	wf  *config.Workflow
	o   LayoutOptions
	an  *analysis
	// cat is the catalog's jobs by the job they run.
	cat      map[string]catalog.Job
	findings []Finding
}

// add records a finding about the entry at index i: the job it names is the
// entry's name.
func (l *layoutCheck) add(code, severity string, i int, message, hint string) {
	l.findings = append(l.findings, Finding{
		Code: code, Severity: severity, Job: l.an.entries[i].Name, Message: message, FixHint: hint, order: i,
	})
}

// addJob records a finding about a job that is not an entry of the workflow
// (or about none), such as a mandatory job that is missing.
func (l *layoutCheck) addJob(code, severity, job, message, hint string) {
	l.findings = append(l.findings, Finding{
		Code: code, Severity: severity, Job: job, Message: message, FixHint: hint, order: len(l.an.entries) + len(l.findings),
	})
}

// useCatalog lets the catalog say what an entry is when the config does not
// define the job it runs: the layout may be a workflow that names the jobs of
// another file.
func (l *layoutCheck) useCatalog() {
	if l.o.Catalog == nil {
		return
	}
	l.cat = map[string]catalog.Job{}
	for _, j := range l.o.Catalog.Jobs {
		if _, ok := l.cat[j.Ref]; !ok {
			l.cat[j.Ref] = j
		}
	}
	for _, e := range l.wf.Entries {
		if e.Kind != config.KindOrb {
			continue
		}
		j, ok := l.cat[e.Ref]
		if !ok {
			continue
		}
		switch j.Kind {
		case "approval":
			e.Kind, e.Approval = config.KindApproval, true
		case "no-op":
			e.Kind = config.KindNoOp
		case "release":
			e.Kind = config.KindRelease
		case "build":
			e.Kind = config.KindJob
		case "job-group":
			e.Kind = config.KindJobGroup
		}
	}
}

// ancestors lists the entries that entry i requires, directly or not, in the
// order of the workflow. It leaves out i itself.
func (l *layoutCheck) ancestors(i int) []int {
	seen := map[int]bool{i: true}
	queue := []int{i}
	var out []int
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range l.an.deps[cur] {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
				queue = append(queue, d)
			}
		}
	}
	sort.Ints(out)
	return out
}

// names lists the effective names of the entries at the indexes.
func (l *layoutCheck) names(idx []int) []string {
	out := make([]string, 0, len(idx))
	for _, i := range idx {
		out = append(out, l.an.entries[i].Name)
	}
	return out
}

// ---- names and requirements ----

func (l *layoutCheck) checkNames(clashes []nameClash) {
	for _, c := range clashes {
		l.add(CodeDuplicateName, SeverityError, c.second,
			fmt.Sprintf("jobs %d and %d of workflow %s are both called %s, so a requires cannot say which one it means", c.first+1, c.second+1, l.wf.Name, c.name),
			"give one of the entries its own `name:`, and require it by that name")
	}
}

func (l *layoutCheck) checkRequires() {
	for i, e := range l.an.entries {
		for _, r := range e.Requires {
			if strings.Contains(r, "<<") {
				continue // an expression: it is only known when CircleCI compiles the config
			}
			if _, ok := l.an.byName[r]; ok {
				continue
			}
			hint := fmt.Sprintf("add a job called %s to the workflow, or remove it from the requires of %s", r, e.Name)
			if g := l.cfg.JobGroupOf(r); g != "" {
				hint = fmt.Sprintf("%s is a job inside the job group %s, and a job outside the group cannot require it: require the group's invocation (%s) instead", r, g, g)
			} else if other := l.entryRunning(r); other != "" {
				hint = fmt.Sprintf("the entry that runs %s is called %s: require it by that name", r, other)
			}
			l.add(CodeDanglingRequires, SeverityError, i,
				fmt.Sprintf("%s requires %s, which no job of workflow %s answers to", e.Name, r, l.wf.Name), hint)
		}
	}
}

// entryRunning returns the name of the entry that runs the job ref, when that
// name is another one ("" if there is no such entry).
func (l *layoutCheck) entryRunning(ref string) string {
	for _, e := range l.an.entries {
		if e.Ref == ref && e.Name != ref && !e.Matrix {
			return e.Name
		}
	}
	return ""
}

// ---- cycles ----

func (l *layoutCheck) checkCycles() {
	n := len(l.an.entries)
	// Tarjan's strongly connected components over the requires edges.
	index, low := make([]int, n), make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int
	next := 0
	var comps [][]int
	var visit func(v int)
	visit = func(v int) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range l.an.deps[v] {
			switch {
			case index[w] < 0:
				visit(w)
				low[v] = min(low[v], low[w])
			case onStack[w]:
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var comp []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sort.Ints(comp)
			comps = append(comps, comp)
		}
	}
	for v := 0; v < n; v++ {
		if index[v] < 0 {
			visit(v)
		}
	}
	sort.Slice(comps, func(a, b int) bool { return comps[a][0] < comps[b][0] })

	for _, comp := range comps {
		if len(comp) < 2 {
			continue
		}
		path := l.cycleThrough(comp)
		var steps []string
		for k, i := range path {
			steps = append(steps, fmt.Sprintf("%s requires %s", l.an.entries[i].Name, l.an.entries[path[(k+1)%len(path)]].Name))
		}
		l.add(CodeCycle, SeverityError, comp[0],
			fmt.Sprintf("these jobs wait for each other, so none of them can start: %s", strings.Join(steps, ", ")),
			fmt.Sprintf("remove one of those requirements; the one that points back to %s is usually the wrong one", l.an.entries[path[0]].Name))
	}
	// A job that requires itself.
	for i, e := range l.an.entries {
		for _, r := range e.Requires {
			if j, ok := l.an.byName[r]; ok && j == i {
				l.add(CodeCycle, SeverityError, i,
					fmt.Sprintf("%s requires itself, so it can never start", e.Name),
					fmt.Sprintf("remove %s from the requires of %s", r, e.Name))
				break
			}
		}
	}
}

// cycleThrough returns a shortest cycle through the first entry of a strongly
// connected component, as the entries in the order that each requires the next.
func (l *layoutCheck) cycleThrough(comp []int) []int {
	in := map[int]bool{}
	for _, i := range comp {
		in[i] = true
	}
	start := comp[0]
	parent := map[int]int{start: -1}
	queue := []int{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range l.an.deps[cur] {
			if !in[d] {
				continue
			}
			if d == start {
				var path []int
				for v := cur; v != -1; v = parent[v] {
					path = append([]int{v}, path...)
				}
				return path
			}
			if _, seen := parent[d]; !seen {
				parent[d] = cur
				queue = append(queue, d)
			}
		}
	}
	return comp
}

// ---- workspaces ----

// role is what an entry does with workspaces, as far as anything shows.
type role struct {
	producer, consumer bool
	// known is false when the job's steps are not visible: an orb job, a job
	// group. Such an entry is assumed to do both, and is never reported.
	known bool
}

func (l *layoutCheck) roleOf(e *config.Entry) role {
	switch e.Kind {
	case config.KindApproval, config.KindNoOp, config.KindRelease:
		return role{known: true}
	case config.KindJobGroup:
		return role{producer: true, consumer: true}
	}
	// The steps that the entry brings (pre-steps, post-steps) run in the job too.
	extra := l.cfg.EntryWorkspace(e)
	if jd := l.cfg.JobDef(e.Ref); jd.Known {
		return role{producer: len(jd.Persists) > 0 || extra.Persists, consumer: jd.Attaches || extra.Attaches, known: true}
	}
	if j, ok := l.cat[e.Ref]; ok && j.Workspace.Persists != nil && j.Workspace.Attaches != nil {
		return role{producer: len(j.Workspace.Persists) > 0 || extra.Persists, consumer: *j.Workspace.Attaches || extra.Attaches, known: true}
	}
	return role{producer: true, consumer: true}
}

func (l *layoutCheck) checkWorkspaces() {
	roles := make([]role, len(l.an.entries))
	for i, e := range l.an.entries {
		roles[i] = l.roleOf(e)
	}
	for i, e := range l.an.entries {
		if !roles[i].known || !roles[i].consumer {
			continue
		}
		anc := l.ancestors(i)
		if slices.ContainsFunc(anc, func(j int) bool { return roles[j].producer }) {
			continue
		}
		var producers []int
		for j := range l.an.entries {
			if j != i && roles[j].known && roles[j].producer {
				producers = append(producers, j)
			}
		}
		hint := fmt.Sprintf("add a job that persists a workspace (persist_to_workspace), and make %s require it", e.Name)
		if len(producers) > 0 {
			hint = fmt.Sprintf("make %s require a job that persists the workspace, such as %s", e.Name, joinNames(l.names(producers), maxHintedNames))
		}
		l.add(CodeConsumerBeforeProducer, SeverityError, i,
			fmt.Sprintf("%s attaches a workspace, but none of the jobs it requires, directly or not, persists one", e.Name), hint)
	}
}

// joinNames lists up to limit names, and says how many more there are.
func joinNames(names []string, limit int) string {
	if len(names) <= limit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:limit], ", "), len(names)-limit)
}

// ---- contexts and mandatory jobs ----

func (l *layoutCheck) checkContexts() {
	if len(l.o.RestrictedContexts) == 0 {
		return
	}
	for i, e := range l.an.entries {
		if e.Kind == config.KindApproval { // an approval runs no code
			continue
		}
		var used []string
		for _, c := range e.Contexts {
			if slices.Contains(l.o.RestrictedContexts, c) && !slices.Contains(used, c) {
				used = append(used, c)
			}
		}
		if len(used) == 0 || l.an.afterApproval[i] {
			continue
		}
		l.add(CodeRestrictedContext, SeverityError, i,
			fmt.Sprintf("%s uses the restricted context %s, but does not run downstream of an approval job", e.Name, strings.Join(used, ", ")),
			fmt.Sprintf("add an approval job (a job with `type: approval`) and make %s require it, directly or through other jobs", e.Name))
	}
}

func (l *layoutCheck) checkMandatory() {
	for _, name := range l.o.Mandatory {
		if _, ok := l.an.byName[name]; ok {
			continue
		}
		l.addJob(CodeMandatoryMissing, SeverityError, name,
			fmt.Sprintf("the mandatory job %s is not in workflow %s", name, l.wf.Name),
			fmt.Sprintf("add %s to the workflow: it must run in every pipeline", name))
	}
}

// ---- the gate ----

func (l *layoutCheck) checkGate() {
	gate := l.o.GateJob
	if gate == "" {
		return
	}
	g, ok := l.an.byName[gate]
	if !ok {
		l.addJob(CodeGateMissing, SeverityError, gate,
			fmt.Sprintf("workflow %s has no gate job called %s", l.wf.Name, gate),
			fmt.Sprintf("define `%s: {type: no-op}` under jobs, and add it to the workflow with a requires that lists every job that runs; the planner adds it to every plan", gate))
		return
	}
	ge := l.an.entries[g]

	if ge.Kind != config.KindNoOp {
		what := map[config.Kind]string{
			config.KindJob:      "a job that runs steps",
			config.KindApproval: "an approval",
			config.KindRelease:  "a release job",
			config.KindJobGroup: "a job group",
			config.KindOrb:      "a job whose definition is not in this config",
		}[ge.Kind]
		hint := fmt.Sprintf("define it as `%s: {type: no-op}` under jobs, with no executor and no steps", gate)
		if ge.Kind == config.KindOrb {
			hint += "; if another file defines it, give that file's catalog (--catalog)"
		}
		l.add(CodeGateNotNoOp, SeverityError, g,
			fmt.Sprintf("the gate %s is %s, not a no-op job: it would cost credits and could fail for its own reasons", gate, what), hint)
	}

	anc := l.ancestors(g)
	awaited := map[int]bool{g: true}
	for _, i := range anc {
		awaited[i] = true
	}
	after := map[int]bool{}
	for _, i := range l.an.downstream(g) {
		after[i] = true
	}
	for i, e := range l.an.entries {
		switch {
		case awaited[i], after[i], gateIgnores(e.Kind), l.an.afterApproval[i], e.StatusRequires:
			// The gate waits for it; or it comes after the gate; or the gate does
			// not wait for this kind of job, or for a job behind an approval
			// (nobody may ever approve it), or for one that waits for a status
			// other than success.
			continue
		}
		l.add(CodeGateMissingRequirement, SeverityError, i,
			fmt.Sprintf("%s will run, but %s does not wait for it, so the gate can pass before %s has finished", e.Name, gate, e.Name),
			fmt.Sprintf("add %s to the requires of %s (or of a job that %s requires)", e.Name, gate, gate))
	}

	for _, i := range append([]int{g}, anc...) {
		e := l.an.entries[i]
		if !e.StatusRequires {
			continue
		}
		msg := fmt.Sprintf("%s requires a job status other than success, so it may never run, and %s would wait for it for ever", e.Name, gate)
		hint := fmt.Sprintf("remove %s from what %s waits for, and the status requirement or the job behind it from the gate's requires", e.Name, gate)
		if i == g {
			msg = fmt.Sprintf("the gate %s requires a job status other than success, so it may never run", gate)
			hint = fmt.Sprintf("make %s require its jobs by their success, which is what a bare name means", gate)
		}
		l.add(CodeGateAwaitsStatusJob, SeverityError, i, msg, hint)
	}
}

// ---- approvals with filters ----

// checkApprovalFilters looks for a job that requires an approval, and also
// something else, where the approval's filters leave the approval out on a
// branch or tag that the job runs on. CircleCI ignores a requirement on a job
// that does not run, so there the job runs without the approval.
func (l *layoutCheck) checkApprovalFilters() {
	var approvals []int
	for i, e := range l.an.entries {
		// A job that requires only the approval does not run where the approval
		// is left out, so only a job with another requirement can slip past it.
		if e.Kind == config.KindApproval && slices.ContainsFunc(l.an.rdeps[i], func(j int) bool { return len(l.an.deps[j]) > 1 }) {
			approvals = append(approvals, i)
		}
	}
	if len(approvals) == 0 {
		return
	}
	branches, tags := refCandidates(l.an.entries)
	type verdicts struct {
		branch, tag string
		will        []tri
	}
	var runs []verdicts
	for _, b := range branches {
		runs = append(runs, verdicts{branch: b, will: willRun(l.an.entries, l.an.deps, l.an.unresolved, b, "")})
	}
	for _, t := range tags {
		runs = append(runs, verdicts{tag: t, will: willRun(l.an.entries, l.an.deps, l.an.unresolved, "", t)})
	}
	for _, a := range approvals {
		for _, j := range l.an.rdeps[a] {
			je := l.an.entries[j]
			if je.Kind == config.KindApproval || len(l.an.deps[j]) < 2 {
				continue
			}
			for _, r := range runs {
				if r.will[a] != triNo || r.will[j] != triYes {
					continue
				}
				where := fmt.Sprintf("branch %s", r.branch)
				switch {
				case r.tag != "":
					where = fmt.Sprintf("tag %s", r.tag)
				case r.branch == probeBranch:
					where = "a branch that no filter names"
				}
				ae := l.an.entries[a]
				l.add(CodeFilteredApprovalFootgun, SeverityWarning, j,
					fmt.Sprintf("%s requires the approval %s, but on %s the approval's filters leave it out and %s still runs, so CircleCI ignores the approval there", je.Name, ae.Name, where, je.Name),
					fmt.Sprintf("give %s the filters of %s (or %s the filters of %s), so that the approval is there wherever the job runs", ae.Name, je.Name, je.Name, ae.Name))
				break
			}
		}
	}
}

// refCandidates lists the branches and tags worth trying against the filters of
// the entries: every name that a filter spells out, a name that matches each
// regular expression, and one name that no filter spells out.
func refCandidates(entries []*config.Entry) (branches, tags []string) {
	bset, tset := map[string]bool{probeBranch: true}, map[string]bool{probeTag: true}
	add := func(set map[string]bool, f *config.RefFilter) {
		if f == nil {
			return
		}
		for _, p := range slices.Concat(f.Only, f.Ignore) {
			if len(p) >= 2 && strings.HasPrefix(p, "/") && strings.HasSuffix(p, "/") {
				for _, w := range regexWitnesses(p[1 : len(p)-1]) {
					set[w] = true
				}
			} else if p != "" && !strings.Contains(p, "<<") {
				set[p] = true
			}
		}
	}
	for _, e := range entries {
		add(bset, e.Filters.Branches)
		add(tset, e.Filters.Tags)
	}
	list := func(set map[string]bool) []string {
		out := make([]string, 0, len(set))
		for s := range set {
			out = append(out, s)
		}
		sort.Strings(out)
		if len(out) > maxRefCandidates {
			out = out[:maxRefCandidates]
		}
		return out
	}
	return list(bset), list(tset)
}

// regexWitnesses returns a few strings that the whole-name match of the pattern
// accepts, the way CircleCI matches a filter: the planner anchors the pattern.
func regexWitnesses(pattern string) []string {
	re, err := regexp.Compile(`^(?:` + pattern + `)$`)
	if err != nil {
		return nil
	}
	tree, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil
	}
	var out []string
	for _, w := range witnessOf(tree.Simplify()) {
		if w != "" && re.MatchString(w) && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

// witnessOf builds strings that the regular expression matches, a few at most.
func witnessOf(re *syntax.Regexp) []string {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary, syntax.OpStar, syntax.OpQuest:
		return []string{""}
	case syntax.OpLiteral:
		return []string{string(re.Rune)}
	case syntax.OpCharClass:
		if r, ok := pickRune(re.Rune); ok {
			return []string{string(r)}
		}
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return []string{"a"}
	case syntax.OpCapture, syntax.OpPlus:
		return witnessOf(re.Sub[0])
	case syntax.OpRepeat:
		sub := witnessOf(re.Sub[0])
		if len(sub) == 0 {
			return nil
		}
		return []string{strings.Repeat(sub[0], re.Min)}
	case syntax.OpConcat:
		out := []string{""}
		for _, sub := range re.Sub {
			ws := witnessOf(sub)
			if len(ws) == 0 {
				return nil
			}
			var next []string
			for _, a := range out {
				for _, b := range ws {
					next = append(next, a+b)
				}
			}
			out = next[:min(len(next), maxWitnesses)]
		}
		return out
	case syntax.OpAlternate:
		var out []string
		for _, sub := range re.Sub {
			out = append(out, witnessOf(sub)...)
		}
		return out[:min(len(out), maxWitnesses)]
	}
	return nil
}

// pickRune picks a rune of a character class that reads well in a branch name.
func pickRune(ranges []rune) (rune, bool) {
	for _, want := range []rune{'a', 'A', '0', '-', '_', '.', '/'} {
		for i := 0; i+1 < len(ranges); i += 2 {
			if ranges[i] <= want && want <= ranges[i+1] {
				return want, true
			}
		}
	}
	if len(ranges) >= 2 {
		return ranges[0], true
	}
	return 0, false
}

// ---- the policy bundle ----

// checkPolicy evaluates the guardrail bundle on the config. A hard violation is
// an error, and a soft one a warning, as for `policy check`.
func (l *layoutCheck) checkPolicy(ctx context.Context, data []byte) error {
	res, err := l.o.Checker.Check(ctx, data)
	if err == nil && res.Status == guard.Error {
		err = errors.New("the policy check reported an error")
		for _, v := range res.Violations {
			if v.Rule == "error" && v.Message != "" {
				err = fmt.Errorf("the policy check reported an error: %s", oneLine(v.Message))
			}
		}
	}
	if err != nil {
		return fmt.Errorf("policy bundle: %w", err)
	}
	for _, v := range res.Violations {
		severity := SeverityWarning
		if v.Hard {
			severity = SeverityError
		}
		job, hint := "", "change the config so that the rule passes, or ask the owner of the policy bundle"
		if v.Policy != "" {
			hint += " (policy " + v.Policy + ")"
		}
		if len(v.RequireJobs) > 0 {
			job = v.RequireJobs[0]
			hint = fmt.Sprintf("add the job %s to the workflow", strings.Join(v.RequireJobs, ", "))
		}
		l.addJob(PolicyCodePrefix+v.Rule, severity, job, oneLine(v.Message), hint)
	}
	return nil
}
