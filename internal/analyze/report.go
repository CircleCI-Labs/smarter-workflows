package analyze

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

// Report summarizes one analyze run for humans and automation.
type Report struct {
	Jobs             []JobReport  `json:"jobs"`
	CompleteJobs     int          `json:"complete_jobs"`
	IncompleteJobs   int          `json:"incomplete_jobs"`
	AICalls          int          `json:"ai_calls"`
	InputTokens      int64        `json:"input_tokens"`
	OutputTokens     int64        `json:"output_tokens"`
	EstimatedCostUSD float64      `json:"estimated_cost_usd"`
	PriceNote        string       `json:"price_note"`
	Warnings         []string     `json:"warnings,omitempty"`
	Truth            *TruthReport `json:"truth,omitempty"`
}

// JobReport is one job's completeness and input provenance.
type JobReport struct {
	Name           string              `json:"name"`
	Complete       bool                `json:"complete"`
	IncompleteWhy  string              `json:"incomplete_why,omitempty"`
	JobHash        string              `json:"job_hash,omitempty"`
	InputsBySignal map[string][]string `json:"inputs_by_signal"`
	Steps          []StepRecord        `json:"steps,omitempty"`
	DroppedTokens  []string            `json:"dropped_tokens,omitempty"`
}

// TruthReport is optional recall vs a reference map (`--truth`).
type TruthReport struct {
	WeightedRecall float64 `json:"weighted_recall"`
	Jobs           int     `json:"jobs"`
}

// WriteReportMarkdown writes a human-readable report.
func WriteReportMarkdown(path string, r *Report) error {
	var b strings.Builder
	b.WriteString("# Analyze report\n\n")
	b.WriteString(fmt.Sprintf("- Complete jobs: **%d**\n", r.CompleteJobs))
	b.WriteString(fmt.Sprintf("- Incomplete jobs: **%d**\n", r.IncompleteJobs))
	if r.AICalls > 0 {
		b.WriteString(fmt.Sprintf("- AI calls: %d (tokens in %d, out %d; est. $%.4f — %s)\n",
			r.AICalls, r.InputTokens, r.OutputTokens, r.EstimatedCostUSD, r.PriceNote))
	}
	if r.Truth != nil {
		b.WriteString(fmt.Sprintf("- Truth weighted recall: **%.4f** (%d jobs scored)\n", r.Truth.WeightedRecall, r.Truth.Jobs))
	}
	b.WriteString("\n")
	for _, j := range r.Jobs {
		b.WriteString(fmt.Sprintf("## %s\n\n", j.Name))
		if j.Complete {
			b.WriteString("**Complete**\n\n")
		} else {
			b.WriteString(fmt.Sprintf("**Incomplete** — %s\n\n", j.IncompleteWhy))
		}
		if j.JobHash != "" {
			b.WriteString(fmt.Sprintf("Job hash: `%s`\n\n", j.JobHash))
		}
		for _, st := range j.Steps {
			if st.Understood {
				b.WriteString(fmt.Sprintf("- Step %d `%s` understood: %s\n", st.Num, st.Key, st.Summary))
			} else {
				b.WriteString(fmt.Sprintf("- Step %d `%s` opaque: %s\n", st.Num, st.Key, st.Opaque))
			}
		}
		if len(j.Steps) > 0 {
			b.WriteString("\n")
		}
		keys := sortedKeysReport(j.InputsBySignal)
		for _, sig := range keys {
			paths := j.InputsBySignal[sig]
			sort.Strings(paths)
			b.WriteString(fmt.Sprintf("### %s\n\n", sig))
			for _, p := range paths {
				b.WriteString(fmt.Sprintf("- `%s`\n", p))
			}
			b.WriteString("\n")
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func sortedKeysReport(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// WriteReportJSON writes report JSON.
func WriteReportJSON(path string, r *Report) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func buildJobReport(name string, in *InputSet, proof *JobProof, complete bool, why, hash string) JobReport {
	bySig := map[string][]string{}
	for _, li := range in.labeledInputs() {
		sig := "static"
		detail := ""
		if len(li.notes) > 0 {
			sig = li.notes[0].Signal
			detail = li.notes[0].Detail
		}
		label := sig
		if detail != "" {
			label = sig + " (" + detail + ")"
		}
		p := li.path
		if li.isDir {
			p = p + "/"
		}
		bySig[label] = append(bySig[label], p)
	}
	jr := JobReport{
		Name:           name,
		Complete:       complete,
		IncompleteWhy:  why,
		JobHash:        hash,
		InputsBySignal: bySig,
	}
	if proof != nil {
		jr.Steps = proof.Steps
		jr.DroppedTokens = proof.DroppedTokens
	}
	return jr
}

func appendTruthReport(truthPath string, m *impactmap.Map, rep *Report) error {
	data, err := os.ReadFile(truthPath)
	if err != nil {
		return err
	}
	truth, err := impactmap.Parse(data)
	if err != nil {
		return err
	}
	ev := Evaluate(m, truth, nil)
	rep.Truth = &TruthReport{WeightedRecall: ev.WeightedRecall, Jobs: len(ev.Jobs)}
	return nil
}
