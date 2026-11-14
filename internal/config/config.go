// Package config reads, edits and writes CircleCI configs. It works on
// go.yaml.in/yaml/v3 nodes, not on Go structs, so what the planner does not
// touch comes out as it went in: key order, comments, quoting and anchors.
// (Blank lines are the one thing the YAML library drops.)
//
// The edits are the ones the planner needs: prune the entries of one workflow,
// append a job and its workflow entry, and add steps to job entries and
// definitions.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Config is a parsed CircleCI config.
type Config struct {
	doc  *yaml.Node
	root *yaml.Node
}

// Parse reads one YAML document that holds a mapping.
func Parse(src []byte) (*Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("parse config: the file is empty")
		}
		return nil, fmt.Errorf("parse config: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("parse config: more than one YAML document")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("parse config: the top level is not a mapping")
	}
	return &Config{doc: &doc, root: doc.Content[0]}, nil
}

// Marshal writes the config as YAML with two-space indents.
func (c *Config) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c.doc); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}
	return buf.Bytes(), nil
}

// Version is the config's `version` value ("2.1"), or "" when it is not a
// plain scalar.
func (c *Config) Version() string {
	v, _ := scalarValue(lookup(c.root, "version"))
	return v
}

// section returns the mapping under a top-level key, or nil.
func (c *Config) section(key string) *yaml.Node {
	v := deref(lookup(c.root, key))
	if v == nil || v.Kind != yaml.MappingNode {
		return nil
	}
	return v
}

// Workflows lists the names of the workflows (in file order). A "version"
// entry of a 2.0 config is not a workflow.
func (c *Config) Workflows() []string {
	wfs := c.section("workflows")
	if wfs == nil {
		return nil
	}
	var names []string
	for i := 0; i+1 < len(wfs.Content); i += 2 {
		k, v := wfs.Content[i], deref(wfs.Content[i+1])
		if k.Kind != yaml.ScalarNode || isMergeKey(k) || v == nil || v.Kind != yaml.MappingNode {
			continue
		}
		if jobs := deref(lookup(v, "jobs")); jobs != nil && jobs.Kind == yaml.SequenceNode {
			names = append(names, k.Value)
		}
	}
	return names
}

// HasJob reports whether the config defines a job with this name.
func (c *Config) HasJob(name string) bool {
	return c.jobDef(name) != nil
}

func (c *Config) jobDef(name string) *yaml.Node {
	jobs := c.section("jobs")
	if jobs == nil {
		return nil
	}
	v := deref(lookup(jobs, name))
	if v == nil || v.Kind != yaml.MappingNode {
		return nil
	}
	return v
}

func (c *Config) commandDef(name string) *yaml.Node {
	cmds := c.section("commands")
	if cmds == nil {
		return nil
	}
	v := deref(lookup(cmds, name))
	if v == nil || v.Kind != yaml.MappingNode {
		return nil
	}
	return v
}

func (c *Config) executorDef(name string) *yaml.Node {
	ex := c.section("executors")
	if ex == nil {
		return nil
	}
	v := deref(lookup(ex, name))
	if v == nil || v.Kind != yaml.MappingNode {
		return nil
	}
	return v
}

// AddJob defines a new job. It is an error if the name is taken.
func (c *Config) AddJob(name string, def *yaml.Node) error {
	if c.HasJob(name) {
		return fmt.Errorf("the config already defines a job named %q", name)
	}
	if i := ownIndex(c.root, "jobs"); i >= 0 {
		jobs := deref(c.root.Content[i+1])
		if jobs == nil || jobs.Kind != yaml.MappingNode {
			if isNull(jobs) {
				jobs = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				c.root.Content[i+1] = jobs
			} else {
				return errors.New("the config's jobs section is not a mapping")
			}
		}
		jobs.Style &^= yaml.FlowStyle
		jobs.Content = append(jobs.Content, Str(name), def)
		return nil
	}
	// No jobs section yet: put it just before the workflows.
	jobs := Map(name, def)
	pair := []*yaml.Node{Str("jobs"), jobs}
	at := ownIndex(c.root, "workflows")
	if at < 0 {
		c.root.Content = append(c.root.Content, pair...)
		return nil
	}
	c.root.Content = append(c.root.Content[:at], append(pair, c.root.Content[at:]...)...)
	return nil
}

// Platform says what kind of machine a job runs on.
type Platform int

const (
	// PlatformUnknown: the executor cannot be read from this config (an orb
	// executor, a parameterised image, a job the config does not define).
	PlatformUnknown Platform = iota
	// PlatformLinux: a docker executor, or a machine executor with a Linux image.
	PlatformLinux
	// PlatformOther: macOS, Windows or anything else that is not Linux.
	PlatformOther
)

// JobPlatform classifies a job defined in this config.
func (c *Config) JobPlatform(job string) Platform {
	def := c.jobDef(job)
	if def == nil {
		return PlatformUnknown
	}
	return c.platformOf(def, 0)
}

func (c *Config) platformOf(def *yaml.Node, depth int) Platform {
	if lookup(def, "docker") != nil {
		return PlatformLinux
	}
	if m := lookup(def, "machine"); m != nil {
		return machinePlatform(m)
	}
	if lookup(def, "macos") != nil {
		return PlatformOther
	}
	if ex := lookup(def, "executor"); ex != nil && depth < 5 {
		name, ok := scalarValue(ex)
		if !ok { // the mapping form: {name: foo, ...arguments}
			name, ok = scalarValue(lookup(ex, "name"))
		}
		if !ok || strings.Contains(name, "<<") {
			return PlatformUnknown
		}
		if ed := c.executorDef(name); ed != nil {
			return c.platformOf(ed, depth+1)
		}
	}
	return PlatformUnknown
}

func machinePlatform(m *yaml.Node) Platform {
	m = deref(m)
	if m == nil || m.Kind != yaml.MappingNode {
		return PlatformUnknown
	}
	image, ok := scalarValue(lookup(m, "image"))
	if !ok || strings.Contains(image, "<<") {
		return PlatformUnknown
	}
	switch img := strings.ToLower(image); {
	case strings.HasPrefix(img, "windows"):
		return PlatformOther
	case strings.HasPrefix(img, "ubuntu-"), strings.HasPrefix(img, "android"),
		strings.HasPrefix(img, "linux"), img == "default":
		return PlatformLinux
	}
	return PlatformUnknown
}

// Workspace reports whether a job persists to or attaches from a workspace. It
// looks through the job's steps, including the steps of the local commands
// they call. Orb commands cannot be read, so they are not followed.
func (c *Config) Workspace(job string) (persists, attaches bool) {
	def := c.jobDef(job)
	if def == nil {
		return false, false
	}
	seen := map[string]bool{}
	var scan func(n *yaml.Node, depth int)
	scan = func(n *yaml.Node, depth int) {
		n = deref(n)
		if n == nil || depth > maxDepth {
			return
		}
		visit := func(id string) {
			switch id {
			case "persist_to_workspace":
				persists = true
			case "attach_workspace":
				attaches = true
			default:
				if cmd := c.commandDef(id); cmd != nil && !seen[id] {
					seen[id] = true
					scan(cmd, depth+1)
				}
			}
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				if k := n.Content[i]; k.Kind == yaml.ScalarNode && !isMergeKey(k) {
					visit(k.Value)
				}
				scan(n.Content[i+1], depth+1)
			}
		case yaml.SequenceNode:
			for _, item := range n.Content {
				if d := deref(item); d != nil && d.Kind == yaml.ScalarNode {
					visit(d.Value)
				}
				scan(item, depth+1)
			}
		}
	}
	scan(def, 0)
	return persists, attaches
}

// InsertAfterCheckout inserts step right after the first `checkout` step in
// the steps of a job defined in this config. It reports whether the job has
// such a step. Steps inside commands are not touched: a command may be shared
// with jobs that are not traced.
func (c *Config) InsertAfterCheckout(job string, step *yaml.Node) (bool, error) {
	def := c.jobDef(job)
	if def == nil {
		return false, fmt.Errorf("job %q is not defined in this config", job)
	}
	i := ownIndex(def, "steps")
	if i < 0 {
		return false, nil // the steps come from a merge key: leave them alone
	}
	steps := def.Content[i+1]
	if steps.Kind == yaml.AliasNode {
		steps = clone(steps)
		def.Content[i+1] = steps
	}
	if steps.Kind != yaml.SequenceNode {
		return false, nil
	}
	for j, s := range steps.Content {
		if !isCheckout(s) {
			continue
		}
		if j+1 < len(steps.Content) && sameNode(steps.Content[j+1], step) {
			return true, nil // already there
		}
		steps.Style &^= yaml.FlowStyle
		steps.Content = append(steps.Content[:j+1], append([]*yaml.Node{step}, steps.Content[j+1:]...)...)
		return true, nil
	}
	return false, nil
}

// sameNode compares two nodes by content, ignoring style, comments and anchors.
func sameNode(a, b *yaml.Node) bool {
	a, b = deref(a), deref(b)
	if a == nil || b == nil || a.Kind != b.Kind || a.Value != b.Value || len(a.Content) != len(b.Content) {
		return false
	}
	for i := range a.Content {
		if !sameNode(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}

func isCheckout(step *yaml.Node) bool {
	s := deref(step)
	if s == nil {
		return false
	}
	switch s.Kind {
	case yaml.ScalarNode:
		return s.Value == "checkout"
	case yaml.MappingNode:
		return len(s.Content) == 2 && s.Content[0].Value == "checkout"
	}
	return false
}
