package config

import (
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Entry is one item of a workflow's job list.
type Entry struct {
	// Ref is the job the entry runs: a job defined in the config, or an orb job.
	Ref string
	// Name is the entry's name in the workflow: `name:` if set, else Ref.
	Name string
	// Alias is the matrix alias, if the entry has a matrix that sets one.
	Alias string
	// Kind says what the entry runs. Workflow sets it from the job's
	// definition, so an entry read on its own (an item of a job group) has
	// none.
	Kind Kind
	// Requires lists the names of the entries this one waits for.
	Requires []string
	// Requirements is Requires with the statuses each name waits for.
	Requirements []Requirement
	// StatusRequires is set when a requires item names a status other than
	// success (`- build: failed`), so the entry may run even if that job does
	// not succeed. `- build: success` is the same as `- build`.
	StatusRequires bool
	// Approval is set for approval entries: `type: approval` on the entry, or
	// on the definition of the job it names (Kind is KindApproval).
	Approval bool
	// Contexts are the values of the entry's `context`, as written.
	Contexts []string
	// SerialGroup is the entry's `serial-group` as written ("" if none). It is
	// a key of the entry, never of the job's definition.
	SerialGroup string
	// Matrix is set for entries that expand into a matrix of jobs.
	Matrix bool
	// Dynamic is set when the name, alias or a requires item holds an
	// `<< expression >>`, so the entry cannot be matched by name.
	Dynamic bool
	// Filters are the entry's branch and tag filters.
	Filters Filters

	item *yaml.Node // the item in the workflow's job list
	args *yaml.Node // the mapping of arguments, or nil
}

// MatrixParameters returns the parameters of the entry's matrix: each name
// with the values it takes, as plain Go values (strings, numbers, booleans). It
// is nil for an entry with no matrix, or no parameters.
func (e *Entry) MatrixParameters() map[string][]any {
	params := lookup(lookup(e.args, "matrix"), "parameters")
	if params == nil || params.Kind != yaml.MappingNode {
		return nil
	}
	out := map[string][]any{}
	for i := 0; i+1 < len(params.Content); i += 2 {
		k, v := params.Content[i], params.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		var values []any
		items := []*yaml.Node{v}
		if v.Kind == yaml.SequenceNode {
			items = v.Content
		}
		for _, item := range items {
			var x any
			if err := item.Decode(&x); err == nil {
				values = append(values, x)
			}
		}
		out[k.Value] = values
	}
	return out
}

// Requirement is one item of an entry's `requires`: a name and the statuses of
// that entry it waits for.
type Requirement struct {
	Job string
	// Statuses are as written; a bare name is ["success"].
	Statuses []string
}

// Names lists the names under which other entries may require this one.
func (e *Entry) Names() []string {
	var names []string
	add := func(n string) {
		if n == "" || strings.Contains(n, "<<") {
			return
		}
		for _, have := range names {
			if have == n {
				return
			}
		}
		names = append(names, n)
	}
	add(e.Name)
	add(e.Alias)
	if e.Matrix {
		add(e.Ref)
	}
	return names
}

// EntryArgs is the workflow entry's argument mapping (pre-steps, filters, and
// so on), or nil for a bare job name.
func (e *Entry) EntryArgs() *yaml.Node {
	return e.args
}

// RequireName is the name another entry writes in `requires` to wait for this
// one. It differs from Name for a matrix entry whose name is an expression: the
// matrix is then required by its alias, or by its job name.
func (e *Entry) RequireName() string {
	switch {
	case e.Matrix && e.Alias != "":
		return e.Alias
	case e.Matrix && strings.Contains(e.Name, "<<"):
		return e.Ref
	}
	return e.Name
}

// Filters are the branch and tag filters of a workflow entry.
type Filters struct {
	// Present is set when the entry has a `filters` key.
	Present bool
	// Expr is set when the filters are an expression, or any form this package
	// does not read: the planner cannot tell where the job runs.
	Expr bool
	// Branches and Tags are nil when the entry has no such filter.
	Branches *RefFilter
	Tags     *RefFilter
}

// RefFilter is a `branches` or `tags` filter.
type RefFilter struct {
	Only, Ignore       []string
	HasOnly, HasIgnore bool
	// Dynamic is set when a value is an `<< expression >>` or has an unexpected form.
	Dynamic bool
}

// Condition is what the workflow's own `when` and `unless` say.
type Condition int

const (
	// NoCondition: the workflow has neither key.
	NoCondition Condition = iota
	// LiteralCondition: every key present is a literal true or false.
	LiteralCondition
	// DynamicCondition: a key holds an expression, a parameter or anything else.
	DynamicCondition
)

// Workflow is one workflow of the config.
type Workflow struct {
	Name    string
	Entries []*Entry

	cfg  *Config    // the config the workflow is in, which says what its entries run
	node *yaml.Node // the workflow mapping
	jobs *yaml.Node // its job list
}

// Workflow returns the workflow with this name.
func (c *Config) Workflow(name string) (*Workflow, error) {
	wfs := c.section("workflows")
	if wfs == nil {
		return nil, errors.New("the config has no workflows")
	}
	node := lookup(wfs, name)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("the config has no workflow named %q", name)
	}
	jobs := lookup(node, "jobs")
	if jobs == nil || jobs.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("workflow %q has no job list", name)
	}
	w := &Workflow{Name: name, cfg: c, node: node, jobs: jobs}
	if err := w.reload(); err != nil {
		return nil, err
	}
	return w, nil
}

// reload parses the job list again into a new Entries slice, so that a slice
// handed out earlier keeps describing the list as it was.
func (w *Workflow) reload() error {
	entries := make([]*Entry, 0, len(w.jobs.Content))
	for i, item := range w.jobs.Content {
		e, err := parseEntry(item)
		if err != nil {
			return fmt.Errorf("workflow %q, job %d: %w", w.Name, i+1, err)
		}
		w.cfg.classify(e)
		entries = append(entries, e)
	}
	w.Entries = entries
	return nil
}

func parseEntry(item *yaml.Node) (*Entry, error) {
	e := &Entry{item: item}
	switch item.Kind {
	case yaml.ScalarNode:
		e.Ref = item.Value
	case yaml.MappingNode:
		if len(item.Content) != 2 {
			return nil, fmt.Errorf("a job entry has %d keys, want 1", len(item.Content)/2)
		}
		if item.Content[0].Kind != yaml.ScalarNode {
			return nil, errors.New("a job entry has a key that is not a name")
		}
		e.Ref = item.Content[0].Value
		args := item.Content[1]
		switch {
		case isNull(args):
		case args.Kind == yaml.MappingNode:
			e.args = args
		default:
			return nil, fmt.Errorf("the arguments of %q are not a mapping", e.Ref)
		}
	default:
		return nil, errors.New("a job entry is neither a name nor a mapping")
	}
	if e.Ref == "" {
		return nil, errors.New("a job entry has an empty name")
	}
	e.Name = e.Ref
	if e.args == nil {
		return e, nil
	}

	if v := lookup(e.args, "name"); v != nil {
		s, ok := scalarValue(v)
		if !ok || s == "" {
			return nil, fmt.Errorf("the name of %q is not a string", e.Ref)
		}
		e.Name = s
	}
	if v, ok := scalarValue(lookup(e.args, "type")); ok && v == "approval" {
		e.Approval = true
	}
	if m := lookup(e.args, "matrix"); m != nil {
		e.Matrix = true
		if a, ok := scalarValue(lookup(m, "alias")); ok {
			e.Alias = a
		}
	}
	if v := lookup(e.args, "requires"); v != nil {
		reqs, status, err := parseRequires(v)
		if err != nil {
			return nil, fmt.Errorf("requires of %q: %w", e.Ref, err)
		}
		e.Requirements, e.StatusRequires = reqs, status
		for _, r := range reqs {
			e.Requires = append(e.Requires, r.Job)
		}
	}
	if v := lookup(e.args, "context"); v != nil {
		e.Contexts, _ = stringList(v)
	}
	if v, ok := scalarValue(lookup(e.args, "serial-group")); ok {
		e.SerialGroup = v
	}
	if v := lookup(e.args, "filters"); v != nil {
		e.Filters = parseFilters(v)
	}

	e.Dynamic = strings.Contains(e.Name, "<<") || strings.Contains(e.Alias, "<<")
	for _, r := range e.Requires {
		if strings.Contains(r, "<<") {
			e.Dynamic = true
		}
	}
	return e, nil
}

func parseRequires(seq *yaml.Node) (reqs []Requirement, status bool, err error) {
	if seq.Kind != yaml.SequenceNode {
		return nil, false, errors.New("not a list")
	}
	for _, it := range seq.Content {
		name, isStatus, ok := requireItem(it)
		if !ok {
			return nil, false, errors.New("an item is neither a name nor a name with statuses")
		}
		reqs = append(reqs, Requirement{Job: name, Statuses: requireStatuses(it)})
		status = status || isStatus
	}
	return reqs, status, nil
}

// requireItem reads one requires item: a name, or {name: status(es)}. It
// reports withStatus for a status other than success.
func requireItem(it *yaml.Node) (name string, withStatus, ok bool) {
	switch it.Kind {
	case yaml.ScalarNode:
		return it.Value, false, true
	case yaml.MappingNode:
		if len(it.Content) == 2 && it.Content[0].Kind == yaml.ScalarNode {
			return it.Content[0].Value, !onlySuccess(it.Content[1]), true
		}
	}
	return "", false, false
}

// requireStatuses lists the statuses a requires item waits for, as written. A
// bare name waits for success.
func requireStatuses(it *yaml.Node) []string {
	if it.Kind == yaml.MappingNode && len(it.Content) == 2 {
		if statuses, ok := stringList(it.Content[1]); ok && len(statuses) > 0 {
			return statuses
		}
	}
	return []string{"success"}
}

// onlySuccess reports whether the statuses of a requires item are success and
// nothing else, which is what a bare job name means.
func onlySuccess(v *yaml.Node) bool {
	switch v.Kind {
	case yaml.ScalarNode:
		return v.Value == "success"
	case yaml.SequenceNode:
		for _, st := range v.Content {
			if st.Kind != yaml.ScalarNode || st.Value != "success" {
				return false
			}
		}
		return len(v.Content) > 0
	}
	return false
}

func parseFilters(m *yaml.Node) Filters {
	f := Filters{Present: true}
	if m.Kind != yaml.MappingNode {
		f.Expr = true // a string: the expression form
		return f
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		switch m.Content[i].Value {
		case "branches":
			f.Branches = parseRefFilter(m.Content[i+1])
		case "tags":
			f.Tags = parseRefFilter(m.Content[i+1])
		default:
			f.Expr = true
		}
	}
	return f
}

func parseRefFilter(m *yaml.Node) *RefFilter {
	r := &RefFilter{}
	if m.Kind != yaml.MappingNode {
		r.Dynamic = true
		return r
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		key := m.Content[i].Value
		vals, ok := stringList(m.Content[i+1])
		for _, s := range vals {
			if strings.Contains(s, "<<") {
				ok = false
			}
		}
		switch key {
		case "only":
			r.HasOnly, r.Only = true, vals
		case "ignore":
			r.HasIgnore, r.Ignore = true, vals
		default:
			ok = false
		}
		if !ok {
			r.Dynamic = true
		}
	}
	return r
}

// Node is the workflow mapping in the config YAML (when, unless, jobs, …).
func (w *Workflow) Node() *yaml.Node { return w.node }

// Condition inspects the workflow's `when` and `unless`.
func (w *Workflow) Condition() Condition {
	result := NoCondition
	for _, key := range []string{"when", "unless"} {
		v := lookup(w.node, key)
		if v == nil {
			continue
		}
		if v.Kind == yaml.ScalarNode && v.Tag == "!!bool" {
			result = LiteralCondition
			continue
		}
		return DynamicCondition
	}
	return result
}

// HasName reports whether an entry already uses this name.
func (w *Workflow) HasName(name string) bool {
	for _, e := range w.Entries {
		for _, n := range e.Names() {
			if n == name {
				return true
			}
		}
	}
	return false
}

// Prune removes the entries at the given indexes. It re-reads Entries, so the
// entry pointers from before the call must not be used afterwards.
//
// inert is the part of remove that does not run on this branch or tag.
// CircleCI ignores a requirement on a job it does not execute, so Prune drops
// the name of such a job from the requires of the entries that stay, and what
// runs is the same. It edits nothing else. If an entry that stays requires
//   - a removed entry that would run, or
//   - nothing but removed entries that do not run (CircleCI does not run a job
//     whose requirements are all filtered out, and with none left it would),
//
// Prune returns an error and leaves the workflow as it was. The caller removes
// whole chains of jobs that would run.
func (w *Workflow) Prune(remove, inert map[int]bool) error {
	if len(remove) == 0 {
		return nil
	}
	var kept []*yaml.Node
	keptNames, live, dead := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, item := range w.jobs.Content {
		names := w.Entries[i].Names()
		set := keptNames
		switch {
		case !remove[i]:
			kept = append(kept, item)
		case inert[i]:
			set = dead
		default:
			set = live
		}
		for _, n := range names {
			set[n] = true
		}
	}
	type edit struct {
		requires *yaml.Node
		remain   []*yaml.Node
	}
	var edits []edit
	for i, e := range w.Entries {
		if remove[i] || len(e.Requires) == 0 {
			continue
		}
		requires := lookup(e.args, "requires")
		var remain []*yaml.Node
		dropped := false
		for _, it := range requires.Content {
			name, _, _ := requireItem(it)
			switch {
			case keptNames[name] || !(live[name] || dead[name]):
				remain = append(remain, it)
			case live[name]:
				return fmt.Errorf("job %s stays and requires %s, which would be removed and would run", e.Name, name)
			default:
				dropped = true
			}
		}
		if dropped && len(remain) == 0 {
			return fmt.Errorf("job %s requires only jobs that do not run here, so dropping them would leave it with no requirement", e.Name)
		}
		if dropped {
			edits = append(edits, edit{requires, remain})
		}
	}
	for _, ed := range edits {
		ed.requires.Content = ed.remain
	}
	w.jobs.Content = kept
	return w.reload()
}

// ownArgs makes the entry's arguments a mapping and returns it. A bare name
// becomes `name: {}`, and a null argument list an empty mapping.
func (w *Workflow) ownArgs(e *Entry) (*yaml.Node, error) {
	item := e.item
	switch item.Kind {
	case yaml.ScalarNode:
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: item.Value, LineComment: item.LineComment}
		args := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		item.Kind, item.Tag, item.Style, item.Value, item.LineComment = yaml.MappingNode, "!!map", 0, "", ""
		item.Content = []*yaml.Node{key, args}
	case yaml.MappingNode:
		if len(item.Content) != 2 {
			return nil, errors.New("a job entry must have one key")
		}
		item.Style &^= yaml.FlowStyle
		if isNull(item.Content[1]) {
			item.Content[1] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		item.Content[1].Style &^= yaml.FlowStyle
	default:
		return nil, errors.New("a job entry is neither a name nor a mapping")
	}
	if item.Content[1].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("the arguments of %q are not a mapping", e.Ref)
	}
	e.args = item.Content[1]
	return e.args, nil
}

// CheckSteps reports whether AddPreSteps and AddPostSteps can edit the entry:
// its pre-steps and post-steps, when it has them, must each be a list. A key
// with no value counts as an empty list. The check does not touch the entry.
func (e *Entry) CheckSteps() error {
	for _, key := range []string{"pre-steps", "post-steps"} {
		if err := stepsProblem(e, key); err != nil {
			return err
		}
	}
	return nil
}

func stepsProblem(e *Entry, key string) error {
	if v := lookup(e.args, key); v != nil && !isNull(v) && v.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s of %q is not a list", key, e.Name)
	}
	return nil
}

// AddPreSteps puts steps before the entry's own pre-steps.
func (w *Workflow) AddPreSteps(e *Entry, steps ...*yaml.Node) error {
	return w.addSteps(e, "pre-steps", true, steps)
}

// AddPostSteps puts steps after the entry's own post-steps.
func (w *Workflow) AddPostSteps(e *Entry, steps ...*yaml.Node) error {
	return w.addSteps(e, "post-steps", false, steps)
}

// addSteps edits the entry only if it can finish: a key that is not a list
// leaves the entry as it was.
func (w *Workflow) addSteps(e *Entry, key string, prepend bool, steps []*yaml.Node) error {
	if err := stepsProblem(e, key); err != nil {
		return err
	}
	args, err := w.ownArgs(e)
	if err != nil {
		return err
	}
	var seq *yaml.Node
	if i := keyIndex(args, key); i >= 0 {
		seq = args.Content[i+1]
		if isNull(seq) { // `post-steps:` with no value is an empty list
			seq.Kind, seq.Tag, seq.Value, seq.Style = yaml.SequenceNode, "!!seq", "", 0
		}
	} else {
		seq = Seq()
		args.Content = append(args.Content, Str(key), seq)
	}
	seq.Style &^= yaml.FlowStyle
	if prepend {
		seq.Content = append(append([]*yaml.Node{}, steps...), seq.Content...)
	} else {
		seq.Content = append(seq.Content, steps...)
	}
	return nil
}

// RetainRequires cuts the requires of the entry at index i to the items whose
// name keep accepts. The items that stay are kept as written, with their
// statuses and comments. If none stays, the list becomes [fallback]. An entry
// with no requires is left alone. It re-reads Entries, so the entry pointers from
// before the call must not be used afterwards.
func (w *Workflow) RetainRequires(i int, keep func(name string) bool, fallback string) error {
	if i < 0 || i >= len(w.Entries) {
		return fmt.Errorf("workflow %q has no job %d", w.Name, i+1)
	}
	seq := lookup(w.Entries[i].args, "requires")
	if seq == nil || seq.Kind != yaml.SequenceNode || len(seq.Content) == 0 {
		return nil
	}
	var remain []*yaml.Node
	for _, it := range seq.Content {
		if name, _, ok := requireItem(it); !ok || keep(name) {
			remain = append(remain, it)
		}
	}
	switch {
	case len(remain) == len(seq.Content):
		return nil
	case len(remain) == 0:
		remain = []*yaml.Node{Str(fallback)}
	}
	seq.Content = remain
	return w.reload()
}

// AppendEntry adds `- ref` (or `- ref: {requires: [...]}`) at the end of the
// job list.
// JobHashMaterial is the canonical JSON object hashed into impact map job_hash.
func (c *Config) JobHashMaterial(entry *Entry) (map[string]any, error) {
	if entry == nil {
		return nil, fmt.Errorf("job hash: nil entry")
	}
	out := map[string]any{
		"ref":  entry.Ref,
		"name": entry.Name,
	}
	if m := entry.MatrixParameters(); m != nil {
		out["matrix"] = m
	}
	if args := entry.EntryArgs(); args != nil {
		var decoded any
		if err := args.Decode(&decoded); err != nil {
			return nil, fmt.Errorf("job hash: entry args: %w", err)
		}
		out["workflow_args"] = decoded
	}
	jd := c.JobDef(entry.Ref)
	out["job"] = map[string]any{
		"known":     jd.Known,
		"executor":  jd.Executor,
		"steps":     jd.Steps,
		"persists":  jd.Persists,
		"attaches":  jd.Attaches,
		"run_steps": c.CollectRunSteps(entry.Ref, entry.EntryArgs()),
	}
	out["orbs"] = c.orbVersionPins()
	return out, nil
}

func (c *Config) orbVersionPins() map[string]string {
	orbs := c.section("orbs")
	if orbs == nil || orbs.Kind != yaml.MappingNode {
		return nil
	}
	pins := map[string]string{}
	for i := 0; i+1 < len(orbs.Content); i += 2 {
		k := orbs.Content[i]
		v := orbs.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		if v.Kind == yaml.ScalarNode {
			pins[k.Value] = v.Value
		}
	}
	return pins
}

func (w *Workflow) AppendEntry(ref string, requires []string) error {
	if w.HasName(ref) {
		return fmt.Errorf("workflow %q already has a job named %q", w.Name, ref)
	}
	item := Str(ref)
	if len(requires) > 0 {
		item = Map(ref, Map("requires", Strs(requires...)))
	}
	w.jobs.Style &^= yaml.FlowStyle
	w.jobs.Content = append(w.jobs.Content, item)
	return w.reload()
}
