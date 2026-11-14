package planner

import (
	"context"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
)

// maxListed caps how many changed paths one reason names.
const maxListed = 3

// analysis is the workflow as a graph, with what the planner needs to know
// about each entry.
type analysis struct {
	entries []*config.Entry
	byName  map[string]int
	// deps and rdeps are the requires edges, as entry indexes.
	deps, rdeps [][]int
	// unresolved is set for an entry that requires a name no entry answers to.
	unresolved []bool
	// will is step 5: whether the entry runs on this branch or tag.
	will []tri
	// producer and consumer are the workspace roles (step 4).
	producer, consumer []bool
	// afterApproval is set for entries that have an approval among their
	// ancestors.
	afterApproval []bool

	downstreamMemo map[int][]int
}

// analyse reads the workflow as a graph. Two entries that answer to one name
// are an error: no requires can tell which one it means.
func analyse(cfg *config.Config, wf *config.Workflow, branch, tag string) (*analysis, error) {
	an, clashes := buildAnalysis(cfg, wf, branch, tag)
	if len(clashes) > 0 {
		c := clashes[0]
		return nil, fmt.Errorf("jobs %d and %d of workflow %q share the name %q", c.first+1, c.second+1, wf.Name, c.name)
	}
	return an, nil
}

// nameClash is a name that two entries answer to.
type nameClash struct {
	first, second int
	name          string
}

// buildAnalysis is analyse that reports clashing names and goes on: the first
// entry of a name keeps it. The layout check uses it to report every problem of
// a workflow, and not only the first.
func buildAnalysis(cfg *config.Config, wf *config.Workflow, branch, tag string) (*analysis, []nameClash) {
	n := len(wf.Entries)
	an := &analysis{
		entries:        slices.Clone(wf.Entries),
		byName:         map[string]int{},
		deps:           make([][]int, n),
		rdeps:          make([][]int, n),
		unresolved:     make([]bool, n),
		producer:       make([]bool, n),
		consumer:       make([]bool, n),
		afterApproval:  make([]bool, n),
		downstreamMemo: map[int][]int{},
	}
	var clashes []nameClash
	for i, e := range wf.Entries {
		for _, name := range e.Names() {
			if j, dup := an.byName[name]; dup && j != i {
				clashes = append(clashes, nameClash{j, i, name})
				continue
			}
			an.byName[name] = i
		}
	}
	for i, e := range wf.Entries {
		for _, r := range e.Requires {
			j, ok := an.byName[r]
			switch {
			case !ok:
				an.unresolved[i] = true
			case j != i:
				an.deps[i] = append(an.deps[i], j)
				an.rdeps[j] = append(an.rdeps[j], i)
			}
		}
		switch e.Kind {
		case config.KindApproval, config.KindNoOp, config.KindRelease:
			// they run no steps, so they neither persist nor attach
		case config.KindJob:
			an.producer[i], an.consumer[i] = cfg.Workspace(e.Ref)
			// The entry's own pre-steps and post-steps run in the job too.
			u := cfg.EntryWorkspace(e)
			an.producer[i], an.consumer[i] = an.producer[i] || u.Persists, an.consumer[i] || u.Attaches
		default: // an orb job or a job group: unknown inside, so assume both
			an.producer[i], an.consumer[i] = true, true
		}
	}
	an.will = willRun(wf.Entries, an.deps, an.unresolved, branch, tag)

	memo := make([]int8, n) // 0 unknown, 1 no, 2 yes
	var after func(i int, depth int) bool
	after = func(i, depth int) bool {
		if memo[i] != 0 {
			return memo[i] == 2
		}
		if depth > n {
			return false
		}
		memo[i] = 1
		for _, d := range an.deps[i] {
			// An approval that the branch filters out is not there to wait for.
			if (an.entries[d].Approval && an.will[d] != triNo) || after(d, depth+1) {
				memo[i] = 2
				break
			}
		}
		return memo[i] == 2
	}
	for i := range wf.Entries {
		an.afterApproval[i] = after(i, 0)
	}
	return an, clashes
}

// gateIgnores says whether the gate never waits for an entry of this kind: an
// approval waits for a person, and a no-op job runs no steps, so it can only
// fail with the jobs it requires, which the gate waits for.
func gateIgnores(k config.Kind) bool {
	return k == config.KindApproval || k == config.KindNoOp
}

// downstream lists every entry that requires i, directly or not.
func (an *analysis) downstream(i int) []int {
	if out, ok := an.downstreamMemo[i]; ok {
		return out
	}
	seen := map[int]bool{i: true}
	queue := []int{i}
	var out []int
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, r := range an.rdeps[cur] {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
				queue = append(queue, r)
			}
		}
	}
	sort.Ints(out)
	an.downstreamMemo[i] = out
	return out
}

// changeSet is the diff, prepared for step 3.
type changeSet struct {
	// all is every changed path, renames split into delete plus add.
	all []diff.Change
	// effective is all minus the paths that match --ignore-paths.
	effective []diff.Change
	// movedDirs holds the directories that exist at only one of base and head.
	movedDirs map[string]bool
}

func newChangeSet(all []diff.Change, ignore []string) *changeSet {
	cs := &changeSet{all: append([]diff.Change(nil), all...)}
	sort.SliceStable(cs.all, func(i, j int) bool {
		if cs.all[i].Path != cs.all[j].Path {
			return cs.all[i].Path < cs.all[j].Path
		}
		return cs.all[i].Status < cs.all[j].Status
	})
	for _, c := range cs.all {
		if _, ignored := matchAny(ignore, c.Path); !ignored {
			cs.effective = append(cs.effective, c)
		}
	}
	return cs
}

// chain lists the non-root directories on the way to p, outermost first:
// "a/b/c.txt" gives "a" and "a/b".
func chain(p string) []string {
	var dirs []string
	for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
		dirs = append([]string{d}, dirs...)
	}
	return dirs
}

// loadDirs finds, for the added and deleted paths, which directories on their
// way exist at only one of base and head (step 3, with `git ls-tree`).
func (cs *changeSet) loadDirs(ctx context.Context, repo Repo, base, head string) error {
	cs.movedDirs = map[string]bool{}
	seen := map[string]bool{}
	var cands []string
	for _, c := range cs.effective {
		if c.Status == "M" {
			continue
		}
		for _, d := range chain(c.Path) {
			if !seen[d] {
				seen[d] = true
				cands = append(cands, d)
			}
		}
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Strings(cands)
	atBase, err := repo.DirsExist(ctx, base, cands)
	if err != nil {
		return fmt.Errorf("checking directories at the base: %w", err)
	}
	atHead, err := repo.DirsExist(ctx, head, cands)
	if err != nil {
		return fmt.Errorf("checking directories at the head: %w", err)
	}
	for _, d := range cands {
		if atBase[d] != atHead[d] {
			cs.movedDirs[d] = true
		}
	}
	return nil
}

// selection is the outcome of steps 3 and 4.
type selection struct {
	selected []bool
	reasons  [][]string // why an entry is selected, or why it is skipped
	// gatekeeper is set for a leaf no-op entry that the plan keeps although the
	// closure did not select it (see analysis.gatekeeper).
	gatekeeper []bool
}

// gatekeeperReason is why the report says a gatekeeper stays.
const gatekeeperReason = "kept: a no-op gatekeeper"

// directReasons is the direct selection (step 3) of one entry. An empty result
// means "inputs unchanged".
func (p *planner) directReasons(e *config.Entry, cs *changeSet, mandatory, forced map[string]bool) []string {
	var reasons []string
	if mandatory[e.Name] {
		reasons = append(reasons, "mandatory job")
	}
	if forced[e.Name] {
		reasons = append(reasons, fmt.Sprintf("required by the guardrails ([smarter:require-job=%s])", e.Name))
	}
	if e.Kind == config.KindJobGroup {
		// A group is one unit in v0.1: it has no trace, so nothing says which of
		// its jobs a change reaches, and it runs whatever the change is.
		return append(reasons, "job group: runs as one unit, as v0.1 does not look inside it")
	}
	if e.Matrix {
		reasons = append(reasons, "matrix entry always runs")
	}
	for _, glob := range p.o.ExtraInputs[e.Name] {
		for _, c := range cs.effective {
			if _, hit := matchAny([]string{glob}, c.Path); hit {
				reasons = append(reasons, fmt.Sprintf("extra input %s matches %s", glob, c.Path))
				break
			}
		}
	}

	mj := p.in.Map.Jobs[e.Name]
	switch {
	case mj == nil:
		return append(reasons, "no impact map entry")
	case !mj.Complete:
		return append(reasons, "the impact map entry is incomplete")
	}

	var reads, dirs []string
	for _, c := range cs.effective {
		if mj.ReadsFile(c.Path) {
			reads = append(reads, c.Path)
		}
		if c.Status == "M" {
			continue
		}
		verb := "added"
		if c.Status == "D" {
			verb = "deleted"
		}
		if parent := path.Dir(c.Path); mj.ListsDir(parent) {
			dirs = append(dirs, fmt.Sprintf("lists %s, where %s was %s", parent, c.Path, verb))
			continue
		}
		for _, d := range chain(c.Path) {
			if !cs.movedDirs[d] {
				continue
			}
			if above := path.Dir(d); mj.ListsDir(above) {
				state := "new"
				if c.Status == "D" {
					state = "gone"
				}
				dirs = append(dirs, fmt.Sprintf("lists %s, where directory %s is %s", above, d, state))
				break
			}
		}
	}
	reasons = append(reasons, capped(reads, "reads ", "changed files read")...)
	return append(reasons, capped(dirs, "", "directory changes")...)
}

// capped turns the first few items into reasons, each with the given prefix,
// and summarises the rest in one more reason.
func capped(items []string, prefix, what string) []string {
	var out []string
	for i, it := range items {
		if i == maxListed {
			out = append(out, fmt.Sprintf("and %d more %s", len(items)-maxListed, what))
			break
		}
		out = append(out, prefix+it)
	}
	return out
}

// selectJobs runs steps 3 and 4. An entry that does not run on this branch
// (step 5 says no) is never selected: not on its own inputs, not as a mandatory
// or guardrail-forced job, and not by the closure. So a deploy filtered to main
// does not keep its whole upstream chain alive on every branch. A selected job
// may require one that does not run; that one is left out, and so is the
// requirement on it, which CircleCI would ignore (step 7).
func (p *planner) selectJobs(an *analysis, cs *changeSet, forced map[string]bool) *selection {
	n := len(an.entries)
	sel := &selection{selected: make([]bool, n), reasons: make([][]string, n), gatekeeper: make([]bool, n)}
	mandatory := toSet(p.o.Mandatory)
	eligible := func(i int) bool { return an.will[i] != triNo }
	pick := func(i int, why string) bool {
		if sel.selected[i] || !eligible(i) {
			return false
		}
		sel.selected[i] = true
		sel.reasons[i] = append(sel.reasons[i], why)
		return true
	}

	for i, e := range an.entries {
		if !e.Kind.HasSteps() || !eligible(i) {
			// An approval, a no-op job and a release job run no steps. They have
			// no trace, so no inputs of their own: the jobs around them decide
			// (the closure below).
			continue
		}
		if rs := p.directReasons(e, cs, mandatory, forced); len(rs) > 0 {
			sel.selected[i], sel.reasons[i] = true, rs
		}
	}

	for changed := true; changed; {
		changed = false
		for i, e := range an.entries {
			if e.Kind == config.KindRelease && !sel.selected[i] {
				// A release job tracks the release of the job it requires: it is
				// kept when any of them is selected.
				for _, d := range an.deps[i] {
					if sel.selected[d] && pick(i, "tracks the release of "+an.entries[d].Name) {
						changed = true
						break
					}
				}
			}
			if !sel.selected[i] {
				continue
			}
			for _, d := range an.deps[i] {
				if pick(d, "required by "+an.entries[i].Name) {
					changed = true
				}
			}
			if !an.producer[i] {
				continue
			}
			for _, j := range an.downstream(i) {
				if an.consumer[j] && pick(j, "consumes the workspace of "+an.entries[i].Name) {
					changed = true
				}
			}
		}
	}

	// A gatekeeper is kept whatever else is selected: it is not selected, so it
	// pulls in nothing, and the plan cuts what it waits for (build).
	for i := range an.entries {
		sel.gatekeeper[i] = !sel.selected[i] && an.gatekeeper(i)
	}
	return sel
}

// gatekeeper says whether entry i is a gatekeeper: a no-op job that no entry
// requires, that waits only for success, and that runs on this branch or tag. It
// is the shape of a job that a project requires as its one status check
// (CircleCI's gatekeeper pattern), so select mode must not drop it: the check
// would wait for ever. A no-op job that some entry requires is a coordinator, and
// stays only when a selected job requires it.
func (an *analysis) gatekeeper(i int) bool {
	e := an.entries[i]
	return e.Kind == config.KindNoOp && !e.StatusRequires && len(an.rdeps[i]) == 0 && an.will[i] != triNo
}

// skipReason says why an entry was left out of the plan.
func (an *analysis) skipReason(i int, branch, tag string) string {
	if an.will[i] == triNo {
		return an.inertReason(i, branch, tag)
	}
	switch an.entries[i].Kind {
	case config.KindApproval:
		return "no selected job requires this approval"
	case config.KindNoOp:
		return "no selected job requires this no-op job"
	case config.KindRelease:
		return "none of the jobs it requires is selected"
	}
	return "inputs unchanged"
}

// inertReason says why an entry does not run on this branch or tag: its own
// filters, or no job it requires runs.
func (an *analysis) inertReason(i int, branch, tag string) string {
	e := an.entries[i]
	if filterVerdict(e.Filters, branch, tag) == triNo {
		if tag != "" {
			return "does not run: its filters leave out tag " + tag
		}
		return "does not run: its filters leave out branch " + branch
	}
	var names []string
	for _, d := range an.deps[i] {
		names = append(names, an.entries[d].Name)
	}
	switch len(names) {
	case 0:
		return "does not run"
	case 1:
		return fmt.Sprintf("does not run: it requires %s, which does not run", names[0])
	}
	return "does not run: none of the jobs it requires runs (" + strings.Join(names, ", ") + ")"
}

func toSet(list []string) map[string]bool {
	s := make(map[string]bool, len(list))
	for _, v := range list {
		s[v] = true
	}
	return s
}
