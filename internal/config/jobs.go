package config

import (
	"slices"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Kind says what a workflow entry runs.
type Kind string

const (
	// KindJob is a job the config defines that runs steps: `type: build`,
	// which is the default.
	KindJob Kind = "build"
	// KindApproval is a manual approval: `type: approval` on the entry, or in
	// the definition of the job it names. It runs nothing.
	KindApproval Kind = "approval"
	// KindNoOp is a job defined with `type: no-op`. It has no executor and no
	// steps, so it runs nothing and costs no credits.
	KindNoOp Kind = "no-op"
	// KindRelease is a job defined with `type: release`. It names the release
	// plan that a deploy job created (`plan_name`), and tracks that release.
	KindRelease Kind = "release"
	// KindJobGroup is an invocation of a group under the top-level
	// `job-groups` key: a named set of jobs that is expanded and, with a
	// `serial-group`, serialised as one unit.
	KindJobGroup Kind = "job-group"
	// KindOrb is a job that the config does not define: a job of an orb. Its
	// inside is unknown, unless the orb is defined inline in the config.
	KindOrb Kind = "orb"
)

// HasSteps reports whether an entry of this kind can run steps of its own: a
// job, an orb job or a job group can, and an approval, a no-op job and a
// release job cannot.
func (k Kind) HasSteps() bool {
	switch k {
	case KindApproval, KindNoOp, KindRelease:
		return false
	}
	return true
}

// classify sets the kind of an entry from the job it names.
func (c *Config) classify(e *Entry) {
	switch {
	case e.Approval: // `type: approval` on the entry itself
		e.Kind = KindApproval
	case c.jobDef(e.Ref) != nil:
		e.Kind = jobKind(c.jobDef(e.Ref))
	case c.jobGroup(e.Ref) != nil:
		e.Kind = KindJobGroup
	default:
		e.Kind = KindOrb
	}
	e.Approval = e.Kind == KindApproval
}

// jobKind reads the `type` of a job definition.
func jobKind(def *yaml.Node) Kind {
	t, _ := scalarValue(lookup(def, "type"))
	switch t {
	case "approval":
		return KindApproval
	case "no-op":
		return KindNoOp
	case "release":
		return KindRelease
	}
	return KindJob
}

// scope is where the commands and executors a job names are looked up: the
// config, or an orb defined inline in it. Each table is a mapping, or nil.
type scope struct {
	jobs, commands, executors *yaml.Node
}

func (c *Config) rootScope() scope {
	return scope{jobs: c.section("jobs"), commands: c.section("commands"), executors: c.section("executors")}
}

// orbScope is the scope of an orb defined inline under `orbs`. A registry or
// URL orb is a string, and its inside is not in the config.
func (c *Config) orbScope(alias string) (scope, bool) {
	orb := lookup(c.section("orbs"), alias)
	if orb == nil || orb.Kind != yaml.MappingNode {
		return scope{}, false
	}
	return scope{jobs: table(orb, "jobs"), commands: table(orb, "commands"), executors: table(orb, "executors")}, true
}

// table returns the mapping under key in m, or nil.
func table(m *yaml.Node, key string) *yaml.Node {
	if v := lookup(m, key); v != nil && v.Kind == yaml.MappingNode {
		return v
	}
	return nil
}

func (s scope) job(name string) *yaml.Node      { return table(s.jobs, name) }
func (s scope) command(name string) *yaml.Node  { return table(s.commands, name) }
func (s scope) executor(name string) *yaml.Node { return table(s.executors, name) }

// resolveJob finds the definition that a job reference names: a job of this
// config, or a job of an inline orb (`alias/job`).
func (c *Config) resolveJob(ref string) (*yaml.Node, scope) {
	if def := c.jobDef(ref); def != nil {
		return def, c.rootScope()
	}
	if alias, name, ok := strings.Cut(ref, "/"); ok {
		if sc, ok := c.orbScope(alias); ok {
			if def := sc.job(name); def != nil {
				return def, sc
			}
		}
	}
	return nil, scope{}
}

func (c *Config) jobGroup(name string) *yaml.Node {
	return table(c.section("job-groups"), name)
}

// JobGroup is a named set of jobs under the top-level `job-groups` key.
type JobGroup struct {
	Name string
	// Jobs are the names of the group's entries, in file order: `name:` if an
	// entry sets one, else the job it runs.
	Jobs []string
}

// JobGroups lists the job groups of the config, in file order.
func (c *Config) JobGroups() []JobGroup {
	groups := c.section("job-groups")
	var out []JobGroup
	for i := 0; groups != nil && i+1 < len(groups.Content); i += 2 {
		k, v := groups.Content[i], groups.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		g := JobGroup{Name: k.Value, Jobs: []string{}}
		if jobs := lookup(v, "jobs"); jobs != nil && jobs.Kind == yaml.SequenceNode {
			for _, item := range jobs.Content {
				if e, err := parseEntry(item); err == nil {
					g.Jobs = append(g.Jobs, e.Name)
				}
			}
		}
		out = append(out, g)
	}
	return out
}

// JobGroupOf names the first job group that has an entry with this name, or "".
// A job inside a group cannot be required from outside it: the group's own
// invocation is what other entries require.
func (c *Config) JobGroupOf(job string) string {
	for _, g := range c.JobGroups() {
		if slices.Contains(g.Jobs, job) {
			return g.Name
		}
	}
	return ""
}

// Executor says where a job runs, as far as the config shows.
type Executor struct {
	// Type is docker, machine, macos, windows, or unknown.
	Type string
	// Image is the docker image, the machine image, or `xcode:<version>`.
	Image         string
	ResourceClass string
}

// JobDef is what the config shows of the job a reference names.
type JobDef struct {
	// Known is false when the config does not show the job's inside: a job of
	// a registry orb, or a name that nothing defines. Everything below is
	// then empty.
	Known    bool
	Executor Executor
	// Steps names each step: `checkout`, `run: <name or first line>`, or the
	// step's key.
	Steps []string
	// Persists lists the paths of the job's persist_to_workspace steps, as
	// written, sorted, and Attaches says whether it attaches a workspace. Steps
	// that come from commands this config defines are included.
	Persists []string
	Attaches bool
}

// JobDef describes the job that ref names: a job defined in this config, or a
// job of an inline orb. A no-op, approval or release job has no executor and
// no steps.
func (c *Config) JobDef(ref string) JobDef {
	def, sc := c.resolveJob(ref)
	if def == nil {
		return JobDef{Executor: Executor{Type: "unknown"}}
	}
	jd := JobDef{Known: true, Executor: Executor{Type: "unknown"}, Steps: []string{}, Persists: []string{}}
	if !jobKind(def).HasSteps() {
		return jd
	}
	jd.Executor = sc.executorOf(def, 0)
	jd.Steps = stepLabels(def)
	use := sc.workspace(def)
	jd.Persists, jd.Attaches = use.paths, use.attaches
	return jd
}

// executorOf reads the executor of a job, or of a named executor.
func (s scope) executorOf(def *yaml.Node, depth int) Executor {
	ex := Executor{Type: "unknown"}
	switch {
	case lookup(def, "docker") != nil:
		ex.Type = "docker"
		if seq := lookup(def, "docker"); seq.Kind == yaml.SequenceNode && len(seq.Content) > 0 {
			ex.Image, _ = scalarValue(lookup(seq.Content[0], "image"))
		}
	case lookup(def, "machine") != nil:
		ex.Type = "machine"
		if m := lookup(def, "machine"); m.Kind == yaml.MappingNode {
			ex.Image, _ = scalarValue(lookup(m, "image"))
			if strings.HasPrefix(strings.ToLower(ex.Image), "windows") {
				ex.Type = "windows"
			}
		}
	case lookup(def, "macos") != nil:
		ex.Type = "macos"
		if xcode, ok := scalarValue(lookup(lookup(def, "macos"), "xcode")); ok {
			ex.Image = "xcode:" + xcode
		}
	case lookup(def, "executor") != nil && depth < 5:
		ref := lookup(def, "executor")
		name, ok := scalarValue(ref)
		if !ok { // the mapping form: {name: foo, ...arguments}
			name, ok = scalarValue(lookup(ref, "name"))
		}
		if ok && !strings.Contains(name, "<<") {
			if ed := s.executor(name); ed != nil {
				ex = s.executorOf(ed, depth+1)
			}
		}
	}
	// A class the job sets itself wins over the executor's.
	if rc, ok := scalarValue(lookup(def, "resource_class")); ok {
		ex.ResourceClass = rc
	}
	return ex
}

// stepLabels names the steps of a job definition, one string per step.
func stepLabels(def *yaml.Node) []string {
	steps := lookup(def, "steps")
	out := []string{}
	if steps == nil || steps.Kind != yaml.SequenceNode {
		return out
	}
	for _, st := range steps.Content {
		if label := stepLabel(st); label != "" {
			out = append(out, label)
		}
	}
	return out
}

func stepLabel(st *yaml.Node) string {
	switch st.Kind {
	case yaml.ScalarNode:
		return st.Value
	case yaml.MappingNode:
		if len(st.Content) < 2 || st.Content[0].Kind != yaml.ScalarNode {
			return ""
		}
		key := st.Content[0].Value
		if key != "run" || len(st.Content) != 2 {
			return key
		}
		switch v := st.Content[1]; v.Kind {
		case yaml.ScalarNode:
			return "run: " + firstLine(v.Value)
		case yaml.MappingNode:
			if name, ok := scalarValue(lookup(v, "name")); ok && strings.TrimSpace(name) != "" {
				return "run: " + firstLine(name)
			}
			if cmd, ok := scalarValue(lookup(v, "command")); ok {
				return "run: " + firstLine(cmd)
			}
		}
		return "run"
	}
	return ""
}

// maxStepLabel caps the text of a step label, in characters.
const maxStepLabel = 80

// firstLine is the first line of s that is not blank, trimmed and cut to
// maxStepLabel characters.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if utf8.RuneCountInString(line) > maxStepLabel {
				return string([]rune(line)[:maxStepLabel-3]) + "..."
			}
			return line
		}
	}
	return ""
}

// workspaceUse is what a job does with workspaces.
type workspaceUse struct {
	persists, attaches bool
	// paths are the `paths` of its persist_to_workspace steps, sorted and
	// without duplicates.
	paths []string
}

// WorkspaceUse is what steps do with workspaces.
type WorkspaceUse struct {
	Persists, Attaches bool
	// Paths are the `paths` of the persist_to_workspace steps, as written,
	// sorted and without duplicates.
	Paths []string
}

// CollectAttachWorkspaceAt returns every attach_workspace `at` path declared on
// jobRef (including pre/post steps and local commands), in YAML order.
func (c *Config) CollectAttachWorkspaceAt(jobRef string, entryArgs *yaml.Node) []string {
	def := c.jobDef(jobRef)
	if def == nil {
		return nil
	}
	sc := c.rootScope()
	seen := map[string]bool{}
	var out []string
	if entryArgs != nil {
		for _, key := range []string{"pre-steps", "post-steps"} {
			if seq := lookup(entryArgs, key); seq != nil {
				sc.collectAttachAt(seq, seen, 0, &out)
			}
		}
	}
	if steps := lookup(def, "steps"); steps != nil {
		sc.collectAttachAt(steps, seen, 0, &out)
	}
	return out
}

func (s scope) collectAttachAt(n *yaml.Node, seen map[string]bool, depth int, out *[]string) {
	if n == nil || depth > maxDepth {
		return
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			body := n.Content[i+1]
			if k.Kind == yaml.ScalarNode && k.Value == "attach_workspace" {
				if at, ok := scalarValue(lookup(body, "at")); ok && at != "" {
					*out = append(*out, at)
				}
			}
			if cmd, isCmd := stepCommandName(n); isCmd {
				if cmdBody := s.command(cmd); cmdBody != nil && !seen[cmd] {
					seen[cmd] = true
					if sub := lookup(cmdBody, "steps"); sub != nil {
						s.collectAttachAt(sub, seen, depth+1, out)
					}
				}
			}
			s.collectAttachAt(body, seen, depth+1, out)
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			s.collectAttachAt(item, seen, depth+1, out)
		}
	}
}

// EntryWorkspace is what the steps that a workflow entry carries do with
// workspaces: its pre-steps and post-steps, and any other steps it passes to
// the job as arguments. A job's own steps are not included (see Workspace and
// JobDef), and neither is what an orb job does inside.
func (c *Config) EntryWorkspace(e *Entry) WorkspaceUse {
	u := c.rootScope().workspace(e.args)
	return WorkspaceUse{Persists: u.persists, Attaches: u.attaches, Paths: u.paths}
}

// workspace looks through a job's steps, including the steps of the commands
// of the scope that they call. Orb commands cannot be read, so they are not
// followed.
func (s scope) workspace(def *yaml.Node) workspaceUse {
	u := workspaceUse{paths: []string{}}
	seen := map[string]bool{}
	var scan func(n *yaml.Node, depth int)
	scan = func(n *yaml.Node, depth int) {
		if n == nil || depth > maxDepth {
			return
		}
		visit := func(id string) {
			switch id {
			case "persist_to_workspace":
				u.persists = true
			case "attach_workspace":
				u.attaches = true
			default:
				if cmd := s.command(id); cmd != nil && !seen[id] {
					seen[id] = true
					scan(cmd, depth+1)
				}
			}
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				if k := n.Content[i]; k.Kind == yaml.ScalarNode {
					visit(k.Value)
					if k.Value == "persist_to_workspace" {
						if paths, ok := stringList(lookup(n.Content[i+1], "paths")); ok {
							u.paths = append(u.paths, paths...)
						}
					}
				}
				scan(n.Content[i+1], depth+1)
			}
		case yaml.SequenceNode:
			for _, item := range n.Content {
				if item.Kind == yaml.ScalarNode {
					visit(item.Value)
				}
				scan(item, depth+1)
			}
		}
	}
	scan(def, 0)
	slices.Sort(u.paths)
	u.paths = slices.Compact(u.paths)
	return u
}
