package history

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
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

type pipelineWork struct {
	raw    json.RawMessage
	sha    string
	branch string
	id     string
}

// Fetch downloads pipeline history and writes a JSON array to out.
func Fetch(ctx context.Context, client *Client, opts FetchOptions, out io.Writer, log io.Writer) (FetchStats, error) {
	if opts.Limit <= 0 {
		return FetchStats{}, fmt.Errorf("limit must be positive")
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	repo := &Repo{G: diff.Git{Dir: opts.RepoDir}}
	if _, err := repo.firstParentChain(ctx); err != nil && log != nil {
		fmt.Fprintf(log, "history fetch: warning: cannot read first-parent chain: %v\n", err)
	}
	var stats FetchStats

	var work []pipelineWork
	nextToken := ""
	for len(work) < opts.Limit {
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
			if len(work) >= opts.Limit {
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
			work = append(work, pipelineWork{raw: raw, sha: sha, branch: branch, id: item.ID})
		}
		if len(work) >= opts.Limit {
			break
		}
		nextToken = page.NextPageToken
		if nextToken == "" {
			break
		}
	}

	results := make([]*backtest.CommitHistory, len(work))
	lim := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var recMu sync.Mutex

	for i, w := range work {
		wg.Add(1)
		lim <- struct{}{}
		go func(i int, w pipelineWork) {
			defer wg.Done()
			defer func() { <-lim }()
			entry, ok, err := recordPipeline(ctx, client, opts.Workflow, repo, w.raw, w.sha, w.branch)
			if err != nil || !ok {
				recMu.Lock()
				stats.SkippedPipeline++
				recMu.Unlock()
				if log != nil {
					fmt.Fprintf(log, "history fetch: warning: pipeline %s skipped\n", w.id)
				}
				return
			}
			results[i] = &entry
		}(i, w)
	}
	wg.Wait()

	var list []backtest.CommitHistory
	for _, r := range results {
		if r != nil {
			list = append(list, *r)
			stats.Written++
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

func recordPipeline(ctx context.Context, client *Client, workflow string, repo *Repo, pipelineJSON json.RawMessage, sha, branch string) (backtest.CommitHistory, bool, error) {
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
	base, err := repo.historicalBase(ctx, sha)
	if err != nil {
		return backtest.CommitHistory{}, false, err
	}
	changed, err := repo.changedFiles(ctx, base, sha)
	if err != nil {
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
