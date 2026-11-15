// Package report writes what the planner decided: plan.json for tools and
// plan.md for people (spec, "Plan report").
package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Version is the plan.json format version.
const Version = 1

// maxChangedInMarkdown is how many changed files plan.md lists.
const maxChangedInMarkdown = 50

// Plan is the content of plan.json.
type Plan struct {
	Version      int      `json:"version"`
	Mode         string   `json:"mode"`
	Full         bool     `json:"full"`
	FullReason   string   `json:"full_reason"`
	Workflow     string   `json:"workflow"`
	Base         string   `json:"base"`
	Head         string   `json:"head"`
	Branch       string   `json:"branch"`
	MapRevision  string   `json:"map_revision"`
	MapCreatedAt string   `json:"map_created_at"`
	Changed      []Change `json:"changed"`
	Jobs         []Job    `json:"jobs"`
	// Gate is absent when the plan could not add the gate job.
	Gate *Gate `json:"gate,omitempty"`
	// Trace is present when the plan adds the trace steps and the map job.
	Trace      *Trace     `json:"trace,omitempty"`
	Guardrails Guardrails `json:"guardrails"`
	Warnings   []string   `json:"warnings"`
}

// Change is one changed path: status A, M or D.
type Change struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// Job is the decision for one entry of the planned workflow. In shadow mode
// Run is what select mode would do; every job still runs.
type Job struct {
	Name    string   `json:"name"`
	Run     bool     `json:"run"`
	Reasons []string `json:"reasons"`
}

// Gate is the gate job and the jobs it waits for.
type Gate struct {
	Name     string   `json:"name"`
	Requires []string `json:"requires"`
}

// Trace describes the trace injection of a full run on the default branch.
type Trace struct {
	// Jobs are the entries that got the trace steps.
	Jobs []string `json:"jobs"`
	// MapJob is the job that merges the traces, and Requires what it waits for.
	MapJob   string   `json:"map_job"`
	Requires []string `json:"requires"`
}

// Guardrails is the outcome of the pre-flight check. Status is PASS, SOFT_FAIL,
// HARD_FAIL or ERROR (as the policy service says), or SKIPPED when no policy
// bundle was given.
type Guardrails struct {
	Status     string      `json:"status"`
	Repairs    []string    `json:"repairs"`
	Violations []Violation `json:"violations"`
}

// Violation is one failed guardrail rule.
type Violation struct {
	Policy  string `json:"policy"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
	Hard    bool   `json:"hard"`
}

// normalise makes empty lists encode as [] and not as null.
func (p *Plan) normalise() {
	if p.Version == 0 {
		p.Version = Version
	}
	if p.Changed == nil {
		p.Changed = []Change{}
	}
	if p.Jobs == nil {
		p.Jobs = []Job{}
	}
	for i := range p.Jobs {
		if p.Jobs[i].Reasons == nil {
			p.Jobs[i].Reasons = []string{}
		}
	}
	if p.Gate != nil && p.Gate.Requires == nil {
		p.Gate.Requires = []string{}
	}
	if p.Trace != nil {
		if p.Trace.Jobs == nil {
			p.Trace.Jobs = []string{}
		}
		if p.Trace.Requires == nil {
			p.Trace.Requires = []string{}
		}
	}
	if p.Guardrails.Repairs == nil {
		p.Guardrails.Repairs = []string{}
	}
	if p.Guardrails.Violations == nil {
		p.Guardrails.Violations = []Violation{}
	}
	if p.Warnings == nil {
		p.Warnings = []string{}
	}
}

// JSON encodes the plan as indented JSON with a trailing newline.
func (p *Plan) JSON() ([]byte, error) {
	p.normalise()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RunCount is the number of jobs the plan runs (N of M in the summary line).
func (p *Plan) RunCount() int {
	n := 0
	for _, j := range p.Jobs {
		if j.Run {
			n++
		}
	}
	return n
}

// Markdown renders the human summary: one line with the mode, N of M jobs
// running and the full-run reason; a table of jobs with run or skip and the
// first reason; and up to 50 changed files. Guardrail findings and warnings
// follow when there are any.
func (p *Plan) Markdown() string {
	p.normalise()
	var b strings.Builder

	n, m := p.RunCount(), len(p.Jobs)
	switch p.Mode {
	case "shadow":
		fmt.Fprintf(&b, "**Smarter Workflows, shadow mode:** every job runs; select mode would run %d of %d.", n, m)
	default:
		fmt.Fprintf(&b, "**Smarter Workflows, %s mode:** %d of %d jobs running.", p.Mode, n, m)
	}
	if p.Full && p.FullReason != "" {
		fmt.Fprintf(&b, " Full run: %s.", p.FullReason)
	}
	b.WriteString("\n\n")

	column := "Run"
	if p.Mode == "shadow" {
		column = "Select mode" // nothing is skipped in shadow mode: this is what select would do
	}
	fmt.Fprintf(&b, "| Job | %s | Reason |\n|---|---|---|\n", column)
	for _, j := range p.Jobs {
		verdict, reason := "skip", ""
		if j.Run {
			verdict = "run"
		}
		if len(j.Reasons) > 0 {
			reason = j.Reasons[0]
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", cell(j.Name), verdict, cell(reason))
	}

	if len(p.Changed) > 0 {
		fmt.Fprintf(&b, "\n**Changed files (%d)**\n\n", len(p.Changed))
		for i, c := range p.Changed {
			if i == maxChangedInMarkdown {
				fmt.Fprintf(&b, "- ... and %d more\n", len(p.Changed)-maxChangedInMarkdown)
				break
			}
			fmt.Fprintf(&b, "- %s %s\n", c.Status, line(c.Path))
		}
	}

	g := p.Guardrails
	if g.Status != "" && g.Status != "SKIPPED" && (g.Status != "PASS" || len(g.Repairs) > 0 || len(g.Violations) > 0) {
		fmt.Fprintf(&b, "\n**Guardrails: %s**\n\n", p.Guardrails.Status)
		for _, r := range p.Guardrails.Repairs {
			fmt.Fprintf(&b, "- repaired: %s\n", line(r))
		}
		for _, v := range p.Guardrails.Violations {
			kind := "soft"
			if v.Hard {
				kind = "hard"
			}
			fmt.Fprintf(&b, "- %s violation, %s: %s\n", kind, line(v.Rule), line(v.Message))
		}
	}

	if len(p.Warnings) > 0 {
		b.WriteString("\n**Warnings**\n\n")
		for _, w := range p.Warnings {
			fmt.Fprintf(&b, "- %s\n", line(w))
		}
	}
	return b.String()
}

// line makes text safe for one line of a Markdown list.
func line(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\x00", "")), " ")
}

// cell makes text safe for a Markdown table cell.
func cell(s string) string {
	return strings.ReplaceAll(line(s), "|", `\|`)
}

// Write puts plan.json and plan.md in dir, creating it if needed.
func Write(dir string, p *Plan) error {
	data, err := p.JSON()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), data, 0o640); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "plan.md"), []byte(p.Markdown()), 0o640)
}
