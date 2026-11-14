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
	// Requires lists the names of the entries this one waits for.
	Requires []string
	// StatusRequires is set when a requires item names statuses
	// (`- build: failed`), so the entry may run even if that job does not.
	StatusRequires bool
	// Approval is set for `type: approval` entries.
	Approval bool
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
	w := &Workflow{Name: name, node: node, jobs: jobs}
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
		names, status, err := parseRequires(v)
		if err != nil {
			return nil, fmt.Errorf("requires of %q: %w", e.Ref, err)
		}
		e.Requires, e.StatusRequires = names, status
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

func parseRequires(seq *yaml.Node) (names []string, status bool, err error) {
	if seq.Kind != yaml.SequenceNode {
		return nil, false, errors.New("not a list")
	}
	for _, it := range seq.Content {
		name, isStatus, ok := requireItem(it)
		if !ok {
			return nil, false, errors.New("an item is neither a name nor a name with statuses")
		}
		names = append(names, name)
		status = status || isStatus
	}
	return names, status, nil
}

// requireItem reads one requires item: a name, or {name: status(es)}.
func requireItem(it *yaml.Node) (name string, withStatus, ok bool) {
	switch it.Kind {
	case yaml.ScalarNode:
		return it.Value, false, true
	case yaml.MappingNode:
		if len(it.Content) == 2 && it.Content[0].Kind == yaml.ScalarNode {
			return it.Content[0].Value, true, true
		}
	}
	return "", false, false
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

// Prune removes the entries at the given indexes and drops their names from
// the requires of the entries that stay. It re-reads Entries, so the entry
// pointers from before the call must not be used afterwards.
func (w *Workflow) Prune(remove map[int]bool) error {
	if len(remove) == 0 {
		return nil
	}
	var kept []*yaml.Node
	gone := map[string]bool{}
	keptNames := map[string]bool{}
	for i, item := range w.jobs.Content {
		if remove[i] {
			for _, n := range w.Entries[i].Names() {
				gone[n] = true
			}
			continue
		}
		kept = append(kept, item)
		for _, n := range w.Entries[i].Names() {
			keptNames[n] = true
		}
	}
	for n := range keptNames {
		delete(gone, n)
	}
	w.jobs.Content = kept
	if err := w.reload(); err != nil {
		return err
	}
	for _, e := range w.Entries {
		if err := w.scrubRequires(e, gone); err != nil {
			return err
		}
	}
	return w.reload()
}

func (w *Workflow) scrubRequires(e *Entry, gone map[string]bool) error {
	hit := false
	for _, r := range e.Requires {
		if gone[r] {
			hit = true
		}
	}
	if !hit {
		return nil
	}
	seq := lookup(e.args, "requires")
	var remain []*yaml.Node
	for _, it := range seq.Content {
		if name, _, ok := requireItem(it); ok && gone[name] {
			continue
		}
		remain = append(remain, it)
	}
	args, err := w.ownArgs(e)
	if err != nil {
		return err
	}
	if len(remain) > 0 {
		ns := *seq
		ns.Content = remain
		setOwn(args, "requires", &ns)
	} else {
		delOwn(args, "requires")
	}
	if len(args.Content) == 0 { // nothing but the name is left
		w.collapse(e)
	}
	return nil
}

// collapse turns an entry whose argument mapping is empty back into a bare name.
func (w *Workflow) collapse(e *Entry) {
	item := e.item
	if item.Kind != yaml.MappingNode || len(item.Content) != 2 {
		return
	}
	key := item.Content[0]
	item.Kind, item.Tag, item.Style = yaml.ScalarNode, "!!str", 0
	item.Value = key.Value
	if key.LineComment != "" {
		item.LineComment = key.LineComment
	}
	if item.HeadComment == "" {
		item.HeadComment = key.HeadComment
	}
	item.Content = nil
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

// AddPreSteps puts steps before the entry's own pre-steps.
func (w *Workflow) AddPreSteps(e *Entry, steps ...*yaml.Node) error {
	return w.addSteps(e, "pre-steps", true, steps)
}

// AddPostSteps puts steps after the entry's own post-steps.
func (w *Workflow) AddPostSteps(e *Entry, steps ...*yaml.Node) error {
	return w.addSteps(e, "post-steps", false, steps)
}

func (w *Workflow) addSteps(e *Entry, key string, prepend bool, steps []*yaml.Node) error {
	args, err := w.ownArgs(e)
	if err != nil {
		return err
	}
	var seq *yaml.Node
	if i := keyIndex(args, key); i >= 0 {
		seq = args.Content[i+1]
	} else {
		seq = Seq()
		args.Content = append(args.Content, Str(key), seq)
	}
	if seq.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s of %q is not a list", key, e.Name)
	}
	seq.Style &^= yaml.FlowStyle
	if prepend {
		seq.Content = append(append([]*yaml.Node{}, steps...), seq.Content...)
	} else {
		seq.Content = append(seq.Content, steps...)
	}
	return nil
}

// AppendEntry adds `- ref` (or `- ref: {requires: [...]}`) at the end of the
// job list.
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
