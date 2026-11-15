// Package config reads, edits and writes CircleCI configs. It works on
// go.yaml.in/yaml/v3 nodes, not on Go structs, so what the planner does not
// touch comes out with the meaning it had, and keeps its key order, comments
// and quoting. (Blank lines are dropped, and the indentation is normalised.)
//
// Parse expands every YAML alias and merge key into an independent copy and
// drops the anchors, so no two parts of a Config share a node: an edit to one
// job or workflow entry cannot reach another. The emitted YAML has no anchors.
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

// Parse reads one YAML document that holds a mapping, and expands its aliases
// and merge keys (see the package comment).
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
	if err := expand(&doc); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
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
	v := lookup(c.root, key)
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
		k, v := wfs.Content[i], wfs.Content[i+1]
		if k.Kind != yaml.ScalarNode || v.Kind != yaml.MappingNode {
			continue
		}
		if jobs := lookup(v, "jobs"); jobs != nil && jobs.Kind == yaml.SequenceNode {
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
	return c.rootScope().job(name)
}

// AddJob defines a new job. It is an error if the name is taken.
func (c *Config) AddJob(name string, def *yaml.Node) error {
	if c.HasJob(name) {
		return fmt.Errorf("the config already defines a job named %q", name)
	}
	if i := keyIndex(c.root, "jobs"); i >= 0 {
		jobs := c.root.Content[i+1]
		if jobs.Kind != yaml.MappingNode {
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
	at := keyIndex(c.root, "workflows")
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
	if c.jobUsesNonLinuxShell(def) {
		return PlatformOther
	}
	return c.rootScope().platformOf(def, 0)
}

func (c *Config) jobUsesNonLinuxShell(def *yaml.Node) bool {
	shell, ok := scalarValue(lookup(def, "shell"))
	if !ok {
		return false
	}
	low := strings.ToLower(shell)
	return strings.Contains(low, "powershell") || strings.Contains(low, "cmd")
}

func (s scope) platformOf(def *yaml.Node, depth int) Platform {
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
		if ed := s.executor(name); ed != nil {
			return s.platformOf(ed, depth+1)
		}
	}
	return PlatformUnknown
}

func machinePlatform(m *yaml.Node) Platform {
	if m.Kind != yaml.MappingNode {
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
	use := c.rootScope().workspace(def)
	return use.persists, use.attaches
}

// sameNode compares two nodes by content, ignoring style and comments.
func sameNode(a, b *yaml.Node) bool {
	if a.Kind != b.Kind || a.Value != b.Value || len(a.Content) != len(b.Content) {
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
	switch step.Kind {
	case yaml.ScalarNode:
		return step.Value == "checkout"
	case yaml.MappingNode:
		return len(step.Content) == 2 && step.Content[0].Value == "checkout"
	}
	return false
}
