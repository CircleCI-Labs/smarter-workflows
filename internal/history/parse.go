package history

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
)

// commitFromPipeline returns the checkout SHA for OAuth or GitHub App pipelines.
func commitFromPipeline(raw json.RawMessage) string {
	var p pipelineItem
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	candidates := []string{}
	if p.VCS != nil && p.VCS.Revision != "" {
		candidates = append(candidates, p.VCS.Revision)
	}
	tp := p.TriggerParameters
	if tp != nil {
		if v, ok := tp["git.checkout_sha"].(string); ok && v != "" {
			candidates = append(candidates, v)
		}
		if git, ok := tp["git"].(map[string]any); ok {
			if s, ok := git["checkout_sha"].(string); ok {
				candidates = append(candidates, s)
			}
		}
		if gha, ok := tp["github_app"].(map[string]any); ok {
			if s, ok := gha["checkout_sha"].(string); ok {
				candidates = append(candidates, s)
			}
		}
	}
	for _, c := range candidates {
		if validCommitSHA(c) {
			return c
		}
	}
	return ""
}

// branchFromPipeline returns the branch name for a pipeline item.
func branchFromPipeline(raw json.RawMessage) string {
	var p pipelineItem
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	if p.VCS != nil && p.VCS.Branch != "" {
		return p.VCS.Branch
	}
	tp := p.TriggerParameters
	if tp == nil {
		return ""
	}
	if v, ok := tp["git.branch"].(string); ok {
		return v
	}
	if git, ok := tp["git"].(map[string]any); ok {
		if s, ok := git["branch"].(string); ok {
			return s
		}
	}
	if gha, ok := tp["github_app"].(map[string]any); ok {
		if s, ok := gha["branch"].(string); ok {
			return s
		}
	}
	return ""
}

type pipelineItem struct {
	ID                string         `json:"id"`
	VCS               *vcsInfo       `json:"vcs"`
	TriggerParameters map[string]any `json:"trigger_parameters"`
}

type vcsInfo struct {
	Revision string `json:"revision"`
	Branch   string `json:"branch"`
}

type workflowPage struct {
	Items         []workflowItem `json:"items"`
	NextPageToken string         `json:"next_page_token"`
}

type workflowItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

type jobPage struct {
	Items         []jobItem `json:"items"`
	NextPageToken string    `json:"next_page_token"`
}

type jobItem struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Duration  *int64 `json:"duration"`
	StartedAt string `json:"started_at"`
	StoppedAt string `json:"stopped_at"`
}

// workflowBundle is one workflow run's jobs plus its created_at (for reruns).
type workflowBundle struct {
	CreatedAt string    `json:"created_at"`
	Items     []jobItem `json:"items"`
}

// workflowsNamed lists workflows with the given name, oldest first.
func workflowsNamed(page workflowPage, name string) []workflowItem {
	var out []workflowItem
	for _, w := range page.Items {
		if w.Name == name {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// mergeWorkflowReruns merges job outcomes across workflow reruns (oldest first).
type jobEvent struct {
	name   string
	status string
	wfAt   string
	job    jobItem
}

func mergeWorkflowReruns(bundles []workflowBundle) []backtest.RecordedJob {
	byName := map[string][]jobEvent{}
	var order []string
	for _, b := range bundles {
		for _, j := range b.Items {
			if j.Type != "build" {
				continue
			}
			if j.Status != "success" && j.Status != "failed" {
				continue
			}
			if _, seen := byName[j.Name]; !seen {
				order = append(order, j.Name)
			}
			byName[j.Name] = append(byName[j.Name], jobEvent{name: j.Name, status: j.Status, wfAt: b.CreatedAt, job: j})
		}
	}
	var out []backtest.RecordedJob
	for _, name := range order {
		evs := byName[name]
		sort.Slice(evs, func(i, j int) bool { return evs[i].wfAt < evs[j].wfAt })
		last := evs[len(evs)-1]
		rec := backtest.RecordedJob{
			Name:      name,
			Status:    normalizeStatus(last.status),
			DurationS: jobDurationS(last.job),
		}
		if flakyPair(evs) {
			rec.Flaky = true
		}
		out = append(out, rec)
	}
	return out
}

func normalizeStatus(s string) string {
	switch s {
	case "success", "failed":
		return s
	default:
		return s
	}
}

func flakyPair(evs []jobEvent) bool {
	for i := 0; i < len(evs); i++ {
		if evs[i].status != "failed" {
			continue
		}
		for j := i + 1; j < len(evs); j++ {
			if evs[j].status == "success" {
				return true
			}
		}
	}
	return false
}

func jobDurationS(j jobItem) float64 {
	if j.Duration != nil {
		return float64(*j.Duration) / 1000
	}
	if j.StartedAt != "" && j.StoppedAt != "" {
		start, err1 := isoEpoch(j.StartedAt)
		stop, err2 := isoEpoch(j.StoppedAt)
		if err1 == nil && err2 == nil && stop >= start {
			return float64(stop - start)
		}
	}
	return 0
}

// isoEpoch parses an ISO8601 timestamp, stripping a fractional second before the zone.
func isoEpoch(s string) (int64, error) {
	if dot := strings.Index(s, "."); dot >= 0 {
		rest := s[dot+1:]
		off := strings.IndexAny(rest, "Z+-")
		if off >= 0 {
			s = s[:dot] + rest[off:]
		}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}

// jobsFromSingleWorkflow maps one workflow job page without reruns.
func jobsFromSingleWorkflow(page jobPage, createdAt string) []backtest.RecordedJob {
	if createdAt == "" {
		createdAt = "1970-01-01T00:00:00Z"
	}
	return mergeWorkflowReruns([]workflowBundle{{CreatedAt: createdAt, Items: page.Items}})
}
