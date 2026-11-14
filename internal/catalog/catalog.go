// Package catalog describes the jobs of one workflow as JSON, for an agent
// that composes workflows: what each job is, where it runs, what it requires
// and what it reads. It adds nothing to the config: it is a reading of it, and
// of the impact map when there is one. The output is deterministic, so two runs
// on the same inputs give the same bytes.
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

// Version is the format version of the catalog.
const Version = 1

// maxTopDirs caps how many directories Inputs.TopDirs lists.
const maxTopDirs = 12

// Catalog is the JSON document that `circleci-smarter catalog` prints.
type Catalog struct {
	Version  int    `json:"version"`
	Workflow string `json:"workflow"`
	// MapRevision is the commit the impact map was built at, or "" without a map.
	MapRevision string `json:"map_revision"`
	// Jobs are the entries of the workflow, sorted by name.
	Jobs      []Job   `json:"jobs"`
	JobGroups []Group `json:"job_groups"`
}

// Job is one entry of the workflow.
type Job struct {
	// Name is what other entries require: the entry's `name:`, else its job.
	Name string `json:"name"`
	// Ref is the job the entry runs.
	Ref string `json:"ref"`
	// Kind is build, approval, no-op, release, orb, matrix or job-group.
	Kind     string    `json:"kind"`
	Executor Executor  `json:"executor"`
	Contexts []string  `json:"contexts"`
	Filters  Filters   `json:"filters"`
	Requires []Require `json:"requires"`
	// RequiredBy names the entries that require this one.
	RequiredBy  []string  `json:"required_by"`
	Workspace   Workspace `json:"workspace"`
	SerialGroup string    `json:"serial_group"`
	// Matrix is null unless the entry has a matrix.
	Matrix *Matrix `json:"matrix"`
	Inputs Inputs  `json:"inputs"`
	// Steps names the steps of the job, or is ["unknown"] when the config does
	// not show them.
	Steps []string `json:"steps"`
}

// Executor says where the job runs. Type is docker, machine, macos, windows or
// unknown (an orb job, a parameterised executor, or a job with no executor).
type Executor struct {
	Type          string `json:"type"`
	Image         string `json:"image"`
	ResourceClass string `json:"resource_class"`
}

// Filters are the entry's branch and tag filters, as written. An entry with no
// filters has {}. Expression is set when the filters are an expression, which
// nothing here evaluates.
type Filters struct {
	Branches   *RefFilter `json:"branches,omitempty"`
	Tags       *RefFilter `json:"tags,omitempty"`
	Expression bool       `json:"expression,omitempty"`
}

// RefFilter is a `branches` or `tags` filter. A pattern in /slashes/ is a regular
// expression.
type RefFilter struct {
	Only   []string `json:"only,omitempty"`
	Ignore []string `json:"ignore,omitempty"`
}

// Require is one item of the entry's `requires`.
type Require struct {
	Job string `json:"job"`
	// Status lists the statuses of Job that the entry waits for: ["success"] for
	// a bare name.
	Status []string `json:"status"`
}

// Workspace says what the job does with workspaces. Both fields are null when
// the config does not show the job's steps (an orb job, a job group), so
// nothing is known.
type Workspace struct {
	// Persists lists the paths of its persist_to_workspace steps.
	Persists []string `json:"persists"`
	Attaches *bool    `json:"attaches"`
}

// Matrix holds the parameters of an entry that runs a matrix.
type Matrix struct {
	Alias      string           `json:"alias"`
	Parameters map[string][]any `json:"parameters"`
}

// Inputs says what the impact map knows of the job: how much of the repository
// it read in the last traced full run. Without a map, or without an entry for the
// job, Traced is false.
type Inputs struct {
	Traced   bool `json:"traced"`
	Complete bool `json:"complete"`
	Files    int  `json:"files"`
	Dirs     int  `json:"dirs"`
	// TopDirs are the directories that hold the files it read: the first two
	// path segments of each ("services/api"), or the first one when that
	// would be too many; "." is the root. At most 12, sorted.
	TopDirs []string `json:"top_dirs"`
}

// Group is a named set of jobs under the top-level `job-groups` key.
type Group struct {
	Name string   `json:"name"`
	Jobs []string `json:"jobs"`
}

// Build describes the entries of one workflow. m is the impact map, or nil.
func Build(cfg *config.Config, workflow string, m *impactmap.Map) (*Catalog, error) {
	wf, err := cfg.Workflow(workflow)
	if err != nil {
		return nil, err
	}
	cat := &Catalog{Version: Version, Workflow: workflow, Jobs: []Job{}, JobGroups: []Group{}}
	if m != nil {
		cat.MapRevision = m.Revision
	}

	// Entries answer to their names: an entry may be required by its name or,
	// for a matrix, its alias.
	byName := map[string]int{}
	for i, e := range wf.Entries {
		for _, n := range e.Names() {
			byName[n] = i
		}
	}
	requiredBy := make([][]string, len(wf.Entries))
	for i, e := range wf.Entries {
		for _, r := range e.Requires {
			if j, ok := byName[r]; ok && j != i {
				requiredBy[j] = append(requiredBy[j], e.Name)
			}
		}
	}

	for i, e := range wf.Entries {
		job := Job{
			Name:        e.Name,
			Ref:         e.Ref,
			Kind:        kindOf(e),
			Contexts:    sortedUnique(e.Contexts),
			Filters:     filtersOf(e.Filters),
			Requires:    requiresOf(e.Requirements),
			RequiredBy:  sortedUnique(requiredBy[i]),
			SerialGroup: e.SerialGroup,
			Inputs:      inputsOf(m, e.Name),
		}
		job.Executor, job.Steps, job.Workspace = definitionOf(cfg, e)
		if e.Matrix {
			job.Matrix = &Matrix{Alias: e.Alias, Parameters: e.MatrixParameters()}
			if job.Matrix.Parameters == nil {
				job.Matrix.Parameters = map[string][]any{}
			}
		}
		cat.Jobs = append(cat.Jobs, job)
	}
	sort.SliceStable(cat.Jobs, func(a, b int) bool {
		if cat.Jobs[a].Name != cat.Jobs[b].Name {
			return cat.Jobs[a].Name < cat.Jobs[b].Name
		}
		return cat.Jobs[a].Ref < cat.Jobs[b].Ref
	})

	for _, g := range cfg.JobGroups() {
		cat.JobGroups = append(cat.JobGroups, Group{Name: g.Name, Jobs: sortedUnique(g.Jobs)})
	}
	sort.SliceStable(cat.JobGroups, func(a, b int) bool { return cat.JobGroups[a].Name < cat.JobGroups[b].Name })
	return cat, nil
}

// kindOf is the kind the catalog reports: a matrix entry is a matrix, whatever
// it runs.
func kindOf(e *config.Entry) string {
	switch {
	case e.Kind == config.KindJobGroup, e.Kind == config.KindApproval, e.Kind == config.KindNoOp, e.Kind == config.KindRelease:
		return string(e.Kind)
	case e.Matrix:
		return "matrix"
	case e.Kind == config.KindOrb:
		return "orb"
	}
	return string(config.KindJob)
}

// definitionOf reads the executor, the steps and the workspace use of the job an
// entry runs. A job that runs no steps has no executor and no steps; a job whose
// inside the config does not show is "unknown".
func definitionOf(cfg *config.Config, e *config.Entry) (Executor, []string, Workspace) {
	unknown := Executor{Type: "unknown"}
	switch e.Kind {
	case config.KindApproval, config.KindNoOp, config.KindRelease:
		return unknown, []string{}, Workspace{Persists: []string{}, Attaches: ptr(false)}
	case config.KindJobGroup:
		return unknown, []string{"unknown"}, Workspace{}
	}
	jd := cfg.JobDef(e.Ref)
	if !jd.Known {
		return unknown, []string{"unknown"}, Workspace{}
	}
	// The entry's own pre-steps and post-steps run in the job as well.
	extra := cfg.EntryWorkspace(e)
	persists := sortedUnique(append(slices.Clone(jd.Persists), extra.Paths...))
	return Executor{Type: jd.Executor.Type, Image: jd.Executor.Image, ResourceClass: jd.Executor.ResourceClass},
		jd.Steps, Workspace{Persists: persists, Attaches: ptr(jd.Attaches || extra.Attaches)}
}

func ptr(b bool) *bool { return &b }

func filtersOf(f config.Filters) Filters {
	out := Filters{Expression: f.Expr}
	ref := func(r *config.RefFilter) *RefFilter {
		if r == nil {
			return nil
		}
		return &RefFilter{Only: r.Only, Ignore: r.Ignore}
	}
	out.Branches, out.Tags = ref(f.Branches), ref(f.Tags)
	return out
}

func requiresOf(reqs []config.Requirement) []Require {
	out := make([]Require, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, Require{Job: r.Job, Status: slices.Clone(r.Statuses)})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Job < out[b].Job })
	return out
}

func inputsOf(m *impactmap.Map, name string) Inputs {
	in := Inputs{TopDirs: []string{}}
	if m == nil {
		return in
	}
	j, ok := m.Jobs[name]
	if !ok || j == nil {
		return in
	}
	in.Traced, in.Complete = true, j.Complete
	in.Files, in.Dirs = len(j.Files), len(j.Dirs)
	in.TopDirs = topDirs(j.Files)
	return in
}

// topDirs lists the directories that hold the files, two path segments deep,
// or one when that gives more than maxTopDirs.
func topDirs(files []string) []string {
	var out []string
	for _, depth := range []int{2, 1} {
		set := map[string]bool{}
		for _, f := range files {
			set[dirPrefix(f, depth)] = true
		}
		out = out[:0]
		for d := range set {
			out = append(out, d)
		}
		if len(out) <= maxTopDirs {
			break
		}
	}
	sort.Strings(out)
	if len(out) > maxTopDirs {
		out = out[:maxTopDirs]
	}
	return append([]string{}, out...)
}

// dirPrefix is the directory of file cut to at most depth segments, and "." for
// a file in the root.
func dirPrefix(file string, depth int) string {
	dir := path.Dir(file)
	if dir == "." {
		return "."
	}
	parts := strings.Split(dir, "/")
	if len(parts) > depth {
		parts = parts[:depth]
	}
	return strings.Join(parts, "/")
}

// sortedUnique returns a sorted copy of list without duplicates. It is never nil.
func sortedUnique(list []string) []string {
	out := append([]string{}, list...)
	sort.Strings(out)
	return slices.Compact(out)
}

// JSON encodes the catalog as indented JSON with a trailing newline.
func (c *Catalog) JSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Parse reads a catalog that JSON wrote.
func Parse(data []byte) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	if c.Version != Version {
		return nil, fmt.Errorf("parse catalog: version %d, want %d", c.Version, Version)
	}
	if c.Jobs == nil {
		return nil, errors.New("parse catalog: it has no jobs list")
	}
	return &c, nil
}
