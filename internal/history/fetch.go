package history

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
)

// FetchOptions tune history mining.
type FetchOptions struct {
	ProjectSlug string
	Workflow    string
	Limit       int
	Branch      string
	RepoDir     string
	Concurrency int
}

// FetchStats counts skipped pipelines while mining.
type FetchStats struct {
	Written         int
	SkippedNoCommit int
	SkippedNoClone  int
	SkippedBranch   int
	SkippedPipeline int
}

// Fetch downloads pipeline history and writes a JSON array to out.
func Fetch(ctx context.Context, client *Client, opts FetchOptions, out io.Writer, log io.Writer) (FetchStats, error) {
	if opts.Limit <= 0 {
		return FetchStats{}, fmt.Errorf("limit must be positive")
	}
	repo := Repo{Dir: opts.RepoDir}
	var list []backtest.CommitHistory
	var stats FetchStats

	nextToken := ""
	for stats.Written < opts.Limit {
		page, err := client.Pipelines(ctx, opts.ProjectSlug, nextToken)
		if err != nil {
			if log != nil {
				fmt.Fprintln(log, "history fetch: warning: pipeline list failed; stopping")
			}
			break
		}
		if len(page.Items) == 0 {
			break
		}

		for _, raw := range page.Items {
			if stats.Written >= opts.Limit {
				break
			}
			sha := commitFromPipeline(raw)
			if sha == "" {
				stats.SkippedNoCommit++
				continue
			}
			branch := branchFromPipeline(raw)
			if opts.Branch != "" && branch != opts.Branch {
				stats.SkippedBranch++
				continue
			}
			if !repo.commitExists(ctx, sha) {
				stats.SkippedNoClone++
				if log != nil {
					fmt.Fprintf(log, "history fetch: warning: commit %s not in clone; skipped\n", shortSHA(sha))
				}
				continue
			}
			var item pipelineItem
			if err := json.Unmarshal(raw, &item); err != nil {
				stats.SkippedPipeline++
				continue
			}

			entry, ok, err := recordPipeline(ctx, client, opts.Workflow, repo, raw, sha, branch)
			if err != nil || !ok {
				stats.SkippedPipeline++
				if log != nil {
					fmt.Fprintf(log, "history fetch: warning: pipeline %s skipped\n", item.ID)
				}
				continue
			}
			list = append(list, entry)
			stats.Written++
		}

		if stats.Written >= opts.Limit {
			break
		}
		nextToken = page.NextPageToken
		if nextToken == "" {
			break
		}
	}

	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(list); err != nil {
		return stats, err
	}
	if log != nil {
		fmt.Fprintf(log, "history fetch: wrote %d commits\n", stats.Written)
	}
	return stats, nil
}

func recordPipeline(ctx context.Context, client *Client, workflow string, repo Repo, pipelineJSON json.RawMessage, sha, branch string) (backtest.CommitHistory, bool, error) {
	var item pipelineItem
	if err := json.Unmarshal(pipelineJSON, &item); err != nil {
		return backtest.CommitHistory{}, false, err
	}
	wfPage, err := client.Workflows(ctx, item.ID)
	if err != nil {
		return backtest.CommitHistory{}, false, err
	}
	wfs := workflowsNamed(wfPage, workflow)
	if len(wfs) == 0 {
		return backtest.CommitHistory{}, false, nil
	}
	var bundles []workflowBundle
	for _, wf := range wfs {
		jobsPage, err := client.Jobs(ctx, wf.ID)
		if err != nil {
			continue
		}
		bundles = append(bundles, workflowBundle{CreatedAt: wf.CreatedAt, Items: jobsPage.Items})
	}
	if len(bundles) == 0 {
		return backtest.CommitHistory{}, false, nil
	}
	jobs := mergeWorkflowReruns(bundles)
	base := repo.mergeBase(ctx, sha)
	changed, err := repo.changedFiles(ctx, base, sha)
	if err != nil {
		changed = nil
	}
	if changed == nil {
		changed = []backtest.Change{}
	}
	return backtest.CommitHistory{
		Commit:  sha,
		Base:    base,
		Branch:  branch,
		Changed: changed,
		Jobs:    jobs,
	}, true, nil
}

// WriteFile writes a history JSON array to path.
func WriteFile(path string, list []backtest.CommitHistory) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}
