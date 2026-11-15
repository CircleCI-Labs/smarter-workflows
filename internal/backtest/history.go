// Package backtest replays historical CI runs against today's impact map and
// reports recall and job-time savings.
package backtest

import (
	"encoding/json"
	"fmt"
	"io"
)

// CommitHistory is one pipeline the history file records.
type CommitHistory struct {
	Commit  string        `json:"commit"`
	Base    string        `json:"base"`
	Branch  string        `json:"branch"`
	Tag     string        `json:"tag,omitempty"`
	Changed []Change      `json:"changed"`
	Jobs    []RecordedJob `json:"jobs"`
}

// Change is one path change at that commit (same letters as git: A, M, D).
type Change struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// RecordedJob is how a job finished in CI for that commit.
type RecordedJob struct {
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	DurationS float64 `json:"duration_s"`
	// Flaky is set when the job failed on a first run but passed on a rerun of
	// the same commit.
	Flaky bool `json:"flaky,omitempty"`
}

// DecodeHistory reads a JSON array of commit histories.
func DecodeHistory(r io.Reader) ([]CommitHistory, error) {
	var list []CommitHistory
	if err := json.NewDecoder(r).Decode(&list); err != nil {
		return nil, fmt.Errorf("decoding history: %w", err)
	}
	return list, nil
}

// jobFailed reports whether status counts as a real failure for recall.
func jobFailed(j RecordedJob) bool {
	return !j.Flaky && j.Status == "failed"
}
