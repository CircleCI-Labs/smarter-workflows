package backtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Report is the outcome of a backtest run.
type Report struct {
	Version           int            `json:"version"`
	Commits           int            `json:"commits"`
	Summary           Summary        `json:"summary"`
	PerJobMisses      []JobMiss      `json:"per_job_misses"`
	PerJobUnmatched   []JobUnmatched `json:"per_job_unmatched"`
	PerJobSavings     []JobSavings   `json:"per_job_savings"`
	CommitsWithMisses []CommitResult `json:"commits_with_misses"`
}

// Summary holds overall recall and savings metrics.
type Summary struct {
	TotalFailures      int     `json:"total_failures"`
	CaughtFailures     int     `json:"caught_failures"`
	Misses             int     `json:"misses"`
	UnmatchedFailures  int     `json:"unmatched_failures"`
	FlakyFailures      int     `json:"flaky_failures"`
	Recall             float64 `json:"recall"`
	RanDurationS       float64 `json:"ran_duration_s"`
	UnmatchedDurationS float64 `json:"unmatched_duration_s"`
	SavedDurationS     float64 `json:"saved_duration_s"`
	SavingsPercent     float64 `json:"savings_percent"`
}

// JobMiss counts how often a failing job would have been skipped.
type JobMiss struct {
	Name   string `json:"name"`
	Misses int    `json:"misses"`
}

// JobUnmatched counts failures and job-time for jobs not in today's config.
type JobUnmatched struct {
	Name      string  `json:"name"`
	Failures  int     `json:"failures"`
	DurationS float64 `json:"duration_s,omitempty"`
}

// JobSavings totals skipped job-time for one job name.
type JobSavings struct {
	Name           string  `json:"name"`
	SavedDurationS float64 `json:"saved_duration_s"`
}

// CommitResult lists misses at one commit.
type CommitResult struct {
	Commit string   `json:"commit"`
	Branch string   `json:"branch,omitempty"`
	Full   bool     `json:"full"`
	Misses []string `json:"misses"`
}

func (r *Report) normalise() {
	if r.PerJobMisses == nil {
		r.PerJobMisses = []JobMiss{}
	}
	if r.PerJobUnmatched == nil {
		r.PerJobUnmatched = []JobUnmatched{}
	}
	if r.PerJobSavings == nil {
		r.PerJobSavings = []JobSavings{}
	}
	if r.CommitsWithMisses == nil {
		r.CommitsWithMisses = []CommitResult{}
	}
}

// JSON encodes the report as indented JSON with a trailing newline.
func (r *Report) JSON() ([]byte, error) {
	r.normalise()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Markdown renders a short human summary.
func (r *Report) Markdown() string {
	r.normalise()
	var b strings.Builder
	fmt.Fprintf(&b, "# Smarter Workflows backtest\n\n")
	fmt.Fprintf(&b, "Replayed **%d** commits in select mode.\n\n", r.Commits)
	fmt.Fprintf(&b, "## Recall\n\n")
	fmt.Fprintf(&b, "| Metric | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Failures (non-flaky, matched) | %d |\n", r.Summary.TotalFailures)
	fmt.Fprintf(&b, "| Caught (job would run) | %d |\n", r.Summary.CaughtFailures)
	fmt.Fprintf(&b, "| Misses (failed, would skip) | %d |\n", r.Summary.Misses)
	fmt.Fprintf(&b, "| Unmatched (job not in config) | %d |\n", r.Summary.UnmatchedFailures)
	fmt.Fprintf(&b, "| Flaky (excluded) | %d |\n", r.Summary.FlakyFailures)
	denom := r.Summary.CaughtFailures + r.Summary.Misses
	if denom > 0 {
		fmt.Fprintf(&b, "| **Recall** | **%.1f%%** |\n", 100*r.Summary.Recall)
	} else {
		b.WriteString("| **Recall** | n/a (no matched failures) |\n")
	}
	fmt.Fprintf(&b, "\n## Job-time savings\n\n")
	fmt.Fprintf(&b, "| Metric | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Matched job time | %.0fs |\n", r.Summary.RanDurationS)
	fmt.Fprintf(&b, "| Unmatched job time | %.0fs |\n", r.Summary.UnmatchedDurationS)
	fmt.Fprintf(&b, "| Would have skipped | %.0fs |\n", r.Summary.SavedDurationS)
	fmt.Fprintf(&b, "| **Savings** | **%.1f%%** |\n", r.Summary.SavingsPercent)

	if len(r.PerJobUnmatched) > 0 {
		b.WriteString("\n## Unmatched by job\n\n| Job | Failures | Time (s) |\n|---|---|---|\n")
		for _, row := range r.PerJobUnmatched {
			fmt.Fprintf(&b, "| %s | %d | %.0f |\n", row.Name, row.Failures, row.DurationS)
		}
	}

	if len(r.PerJobMisses) > 0 {
		b.WriteString("\n## Misses by job\n\n| Job | Misses |\n|---|---|\n")
		for _, row := range r.PerJobMisses {
			fmt.Fprintf(&b, "| %s | %d |\n", row.Name, row.Misses)
		}
	}
	if len(r.CommitsWithMisses) > 0 {
		b.WriteString("\n## Commits with misses\n\n")
		for _, c := range r.CommitsWithMisses {
			fmt.Fprintf(&b, "- `%s`", shortSHA(c.Commit))
			if c.Branch != "" {
				fmt.Fprintf(&b, " (%s)", c.Branch)
			}
			fmt.Fprintf(&b, ": %s\n", strings.Join(c.Misses, ", "))
		}
	}
	b.WriteString("\n")
	return b.String()
}
