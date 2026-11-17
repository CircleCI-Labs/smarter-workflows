package learn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/analyze"
	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
)

const correctionsListLimit = 20

// Report is the combined learn output.
type Report struct {
	Version            int              `json:"version"`
	ProjectSlug        string           `json:"project_slug,omitempty"`
	Workflows          []string         `json:"workflows"`
	Commits            int              `json:"commits"`
	Warnings           []string         `json:"warnings,omitempty"`
	Summary            Summary          `json:"summary"`
	MissesBefore       []JobMiss        `json:"misses_before"`
	MissesAfter        []JobMiss        `json:"misses_after"`
	CorrectionsAdded   []PathCorrection `json:"corrections_added"`
	FlakyJobs          []FlakyJob       `json:"flaky_jobs"`
	IncompleteJobs     []string         `json:"incomplete_jobs"`
	AI                 AISection        `json:"ai"`
}

// Summary holds top-level savings metrics (after corrections).
type Summary struct {
	Recall          *float64 `json:"recall"`
	FailingJobs     int      `json:"failing_jobs"`
	SavedDurationS  float64  `json:"saved_duration_s"`
	SavingsPercent  float64  `json:"savings_percent"`
	CommitsReplayed int      `json:"commits_replayed"`
}

// PathCorrection is one history-mined path addition.
type PathCorrection struct {
	Job      string `json:"job"`
	Path     string `json:"path"`
	Commit   string `json:"commit"`
	Pipeline string `json:"pipeline,omitempty"`
}

// FlakyJob counts flaky reruns for one job name in history.
type FlakyJob struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// JobMiss counts skipped failures for one job.
type JobMiss struct {
	Name   string `json:"name"`
	Misses int    `json:"misses"`
}

// AISection records classifier usage during learn.
type AISection struct {
	Calls        int     `json:"calls"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"estimated_cost_usd"`
}

// BuildInput assembles a learn report.
type BuildInput struct {
	ProjectSlug    string
	Workflows      []string
	Commits        int
	Before, After  *backtest.Report
	Analyze        *analyze.Report
	AnalyzeBefore  *analyze.Report
	Corrections    []PathCorrection
	Warnings       []string
	History        []backtest.CommitHistory
}

// BuildReport creates the learn report from run results.
func BuildReport(in BuildInput) *Report {
	r := &Report{
		Version:          1,
		ProjectSlug:      in.ProjectSlug,
		Workflows:        append([]string(nil), in.Workflows...),
		Commits:          in.Commits,
		Warnings:         append([]string(nil), in.Warnings...),
		CorrectionsAdded: append([]PathCorrection(nil), in.Corrections...),
	}
	if in.After != nil {
		failing := in.After.Summary.CaughtFailures + in.After.Summary.Misses
		r.Summary = Summary{
			FailingJobs:     failing,
			SavedDurationS:  in.After.Summary.SavedDurationS,
			SavingsPercent:  in.After.Summary.SavingsPercent,
			CommitsReplayed: in.After.Commits,
		}
		if failing > 0 {
			rec := in.After.Summary.Recall
			r.Summary.Recall = &rec
		}
		r.MissesAfter = misses(in.After)
	}
	if in.Before != nil {
		r.MissesBefore = misses(in.Before)
	}
	if in.Analyze != nil {
		r.AI = AISection{
			Calls:        in.Analyze.AICalls,
			InputTokens:  in.Analyze.InputTokens,
			OutputTokens: in.Analyze.OutputTokens,
			CostUSD:      in.Analyze.EstimatedCostUSD,
		}
		for _, j := range in.Analyze.Jobs {
			if !j.Complete {
				r.IncompleteJobs = append(r.IncompleteJobs, fmt.Sprintf("%s — %s", j.Name, j.IncompleteWhy))
			}
		}
		sort.Strings(r.IncompleteJobs)
	}
	r.FlakyJobs = flakyJobs(in.History)
	return r
}

func flakyJobs(history []backtest.CommitHistory) []FlakyJob {
	counts := map[string]int{}
	for _, h := range history {
		for _, j := range h.Jobs {
			if j.Flaky {
				counts[j.Name]++
			}
		}
	}
	out := make([]FlakyJob, 0, len(counts))
	for name, n := range counts {
		out = append(out, FlakyJob{Name: name, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func misses(bt *backtest.Report) []JobMiss {
	var out []JobMiss
	for _, j := range bt.PerJobMisses {
		if j.Misses > 0 {
			out = append(out, JobMiss{Name: j.Name, Misses: j.Misses})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Report) JSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Markdown renders the learn trial report.
func (r *Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Smarter Workflows learn\n\n")
	fmt.Fprintf(&b, "Measured **%d** recent commits on workflow(s) %s", r.Commits, strings.Join(r.Workflows, ", "))
	if r.ProjectSlug != "" {
		fmt.Fprintf(&b, " for project `%s`", r.ProjectSlug)
	}
	b.WriteString(". This report estimates how select mode would have behaved with a refreshed learned layer.\n\n")
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "> **Warning:** %s\n\n", w)
	}
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "| Metric | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Commits replayed | %d |\n", r.Summary.CommitsReplayed)
	if r.Summary.Recall == nil {
		fmt.Fprintf(&b, "| Recall (after) | n/a (no failing jobs in the replayed history) |\n")
	} else {
		fmt.Fprintf(&b, "| Recall (after) | **%.1f%%** (%d failing jobs) |\n", 100**r.Summary.Recall, r.Summary.FailingJobs)
	}
	fmt.Fprintf(&b, "| Projected savings | **%.0fs** (%.1f%% of matched job time) |\n", r.Summary.SavedDurationS, r.Summary.SavingsPercent)
	counts := correctionCounts(r.CorrectionsAdded)
	for _, job := range sortedCorrectionJobs(counts) {
		fmt.Fprintf(&b, "| Corrections added (%s) | %d |\n", job, counts[job])
	}

	if len(r.MissesBefore) > 0 || len(r.MissesAfter) > 0 {
		b.WriteString("\n## Misses\n\n")
		b.WriteString("Jobs that failed on a commit where select mode would have skipped them.\n\n")
		b.WriteString("| Job | Before corrections | After corrections |\n|---|---|---|\n")
		rows := map[string][2]int{}
		for _, j := range r.MissesBefore {
			rows[j.Name] = [2]int{j.Misses, rows[j.Name][1]}
		}
		for _, j := range r.MissesAfter {
			rows[j.Name] = [2]int{rows[j.Name][0], j.Misses}
		}
		names := sortedKeys(rows)
		for _, name := range names {
			pair := rows[name]
			fmt.Fprintf(&b, "| %s | %d | %d |\n", name, pair[0], pair[1])
		}
	}

	b.WriteString("\n## Corrections added\n\n")
	if len(r.CorrectionsAdded) == 0 {
		b.WriteString("No new history corrections this run.\n")
	} else {
		fmt.Fprintf(&b, "History mining added **%d** path corrections this run.\n\n", len(r.CorrectionsAdded))
		writeCorrectionGroups(&b, r.CorrectionsAdded)
	}

	if len(r.FlakyJobs) > 0 {
		b.WriteString("\n## Flaky jobs\n\n")
		for _, j := range r.FlakyJobs {
			fmt.Fprintf(&b, "- %s (%d)\n", j.Name, j.Count)
		}
	}

	if len(r.IncompleteJobs) > 0 {
		b.WriteString("\n## Incomplete jobs\n\n")
		for _, line := range r.IncompleteJobs {
			fmt.Fprintf(&b, "- %s\n", line)
		}
	}

	fmt.Fprintf(&b, "\n## AI\n\n")
	fmt.Fprintf(&b, "Calls: %d; tokens in %d, out %d; estimated cost $%.4f.\n",
		r.AI.Calls, r.AI.InputTokens, r.AI.OutputTokens, r.AI.CostUSD)
	return b.String()
}

func sortedCorrectionJobs(counts map[string]int) []string {
	out := make([]string, 0, len(counts))
	for k := range counts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func correctionCounts(in []PathCorrection) map[string]int {
	out := map[string]int{}
	for _, c := range in {
		out[c.Job]++
	}
	return out
}

func writeCorrectionGroups(b *strings.Builder, in []PathCorrection) {
	byJob := map[string][]PathCorrection{}
	jobs := []string{}
	for _, c := range in {
		if _, ok := byJob[c.Job]; !ok {
			jobs = append(jobs, c.Job)
		}
		byJob[c.Job] = append(byJob[c.Job], c)
	}
	sort.Strings(jobs)
	for _, job := range jobs {
		list := byJob[job]
		fmt.Fprintf(b, "### %s (%d)\n\n", job, len(list))
		limit := len(list)
		if limit > correctionsListLimit {
			limit = correctionsListLimit
		}
		for _, c := range list[:limit] {
			pipe := c.Pipeline
			if pipe == "" {
				pipe = c.Commit
			}
			fmt.Fprintf(b, "- `%s` — commit `%s`, pipeline `%s`\n", c.Path, shortSHA(c.Commit), shortSHA(pipe))
		}
		if len(list) > correctionsListLimit {
			fmt.Fprintf(b, "- and %d more\n", len(list)-correctionsListLimit)
		}
		b.WriteString("\n")
	}
}

func shortSHA(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 12 {
		return s
	}
	return s[:7]
}

func sortedKeys(m map[string][2]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PathCorrectionsFromAnalyze copies history corrections into learn report form.
func PathCorrectionsFromAnalyze(rep *analyze.Report) []PathCorrection {
	if rep == nil || len(rep.HistoryCorrections) == 0 {
		return nil
	}
	out := make([]PathCorrection, 0, len(rep.HistoryCorrections))
	for _, c := range rep.HistoryCorrections {
		out = append(out, PathCorrection{
			Job:      c.Job,
			Path:     c.Path,
			Commit:   c.Commit,
			Pipeline: c.Pipeline,
		})
	}
	return out
}