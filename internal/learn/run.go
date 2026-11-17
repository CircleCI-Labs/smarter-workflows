package learn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/analyze"
	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
)

// Options configures one learn run.
type Options struct {
	RepoRoot       string
	ConfigPath     string
	Workflows      []string
	ProjectSlug    string
	HistoryPath    string
	HistoryOutPath string
	LearnedPath    string
	LearnedOutPath string
	Limit          int
	ParametersPath string
	ReportMD       string
	ReportJSON     string
	AIMode         analyze.AIMode
	Model          string
	MinConfidence  float64
	MinRecall      float64
	Planner        backtest.Options
}

// Result is the outcome of learn.
type Result struct {
	Report         *Report
	AnalyzeReport  *analyze.Report
	BacktestBefore *backtest.Report
	BacktestAfter  *backtest.Report
	Warnings       []string
}

// Run fetches history (unless HistoryPath is set), refreshes the learned layer,
// and backtests before/after history corrections.
func Run(ctx context.Context, opts Options, fetch HistoryFetcher, log io.Writer) (*Result, error) {
	if opts.RepoRoot == "" {
		opts.RepoRoot = "."
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = filepath.Join(opts.RepoRoot, ".circleci/continue.yml")
	}
	if len(opts.Workflows) == 0 {
		opts.Workflows = []string{opts.Planner.Workflow}
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	cfg, err := os.ReadFile(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	var warnings []string
	histPath := opts.HistoryPath
	if histPath == "" {
		if fetch == nil {
			return nil, fmt.Errorf("history path or a fetcher is required")
		}
		tmp, err := os.CreateTemp("", "smarter-history-*.json")
		if err != nil {
			return nil, err
		}
		tmp.Close()
		histPath = tmp.Name()
		defer os.Remove(histPath)
		if err := fetchWorkflows(ctx, fetch, opts, histPath, log); err != nil {
			return nil, err
		}
		if opts.HistoryOutPath != "" {
			if err := copyFile(histPath, opts.HistoryOutPath); err != nil {
				return nil, err
			}
		}
	}

	learnedPath := ""
	if opts.LearnedPath != "" {
		prev, learnedWarn, err := analyze.TryLoadLearned(opts.LearnedPath)
		if learnedWarn != "" {
			warnings = append(warnings, learnedWarn)
		} else if err != nil {
			return nil, err
		} else if prev != nil {
			learnedPath = opts.LearnedPath
		}
	}

	rev := gitHEAD(opts.RepoRoot)
	baseOpts := analyze.RunOptions{
		RepoRoot:      opts.RepoRoot,
		ConfigPath:    opts.ConfigPath,
		Workflows:     opts.Workflows,
		Revision:      rev,
		LearnedPath:   learnedPath,
		AIMode:        opts.AIMode,
		Model:         opts.Model,
		MinConfidence: opts.MinConfidence,
	}

	mapBefore, beforeAnalyze, _, err := analyze.Run(ctx, baseOpts)
	if err != nil {
		return nil, err
	}
	afterOpts := baseOpts
	afterOpts.HistoryPath = histPath
	afterOpts.LearnedOutPath = opts.LearnedOutPath
	mapAfter, afterAnalyze, _, err := analyze.Run(ctx, afterOpts)
	if err != nil {
		return nil, err
	}

	hf, err := os.Open(histPath)
	if err != nil {
		return nil, err
	}
	history, err := backtest.DecodeHistory(hf)
	hf.Close()
	if err != nil {
		return nil, err
	}
	planOpts := opts.Planner
	if opts.ParametersPath != "" {
		params, err := loadParams(opts.ParametersPath)
		if err != nil {
			return nil, err
		}
		planOpts.Parameters = params
	}
	btWarns, err := backtest.CheckWarnings(ctx, cfg, history, planOpts)
	if err != nil {
		return nil, err
	}
	for _, w := range btWarns {
		warnings = append(warnings, w.Message)
	}
	btBefore, err := backtest.Run(ctx, cfg, mapBefore, history, planOpts)
	if err != nil {
		return nil, err
	}
	btAfter, err := backtest.Run(ctx, cfg, mapAfter, history, planOpts)
	if err != nil {
		return nil, err
	}
	for _, w := range btWarns {
		btAfter.Warnings = append(btAfter.Warnings, w.Message)
	}

	rep := BuildReport(BuildInput{
		ProjectSlug:   opts.ProjectSlug,
		Workflows:     opts.Workflows,
		Commits:       len(history),
		Before:        btBefore,
		After:         btAfter,
		Analyze:       afterAnalyze,
		AnalyzeBefore: beforeAnalyze,
		Corrections:   countNewCorrections(beforeAnalyze, afterAnalyze),
		Warnings:      warnings,
		History:       history,
	})
	if opts.ReportMD != "" {
		if err := os.WriteFile(opts.ReportMD, []byte(rep.Markdown()), 0o644); err != nil {
			return nil, err
		}
	}
	if opts.ReportJSON != "" {
		data, err := rep.JSON()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(opts.ReportJSON, data, 0o644); err != nil {
			return nil, err
		}
	}
	return &Result{
		Report:         rep,
		AnalyzeReport:  afterAnalyze,
		BacktestBefore: btBefore,
		BacktestAfter:  btAfter,
		Warnings:       warnings,
	}, nil
}

func fetchWorkflows(ctx context.Context, fetch HistoryFetcher, opts Options, dest string, log io.Writer) error {
	if len(opts.Workflows) == 1 {
		return fetch.Fetch(ctx, opts.ProjectSlug, opts.Workflows, opts.Limit, opts.RepoRoot, dest)
	}
	var merged []backtest.CommitHistory
	for _, wf := range opts.Workflows {
		tmp := dest + "." + wf
		if err := fetch.Fetch(ctx, opts.ProjectSlug, []string{wf}, opts.Limit, opts.RepoRoot, tmp); err != nil {
			return err
		}
		list, err := readHistoryFile(tmp)
		os.Remove(tmp)
		if err != nil {
			return err
		}
		merged = append(merged, list...)
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(dest, data, 0o640)
}

func readHistoryFile(path string) ([]backtest.CommitHistory, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return backtest.DecodeHistory(f)
}

func loadParams(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func gitHEAD(repo string) string {
	cmd := exec.Command("git", "-C", repo, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o640)
}

func countNewCorrections(before, after *analyze.Report) int {
	if before == nil || after == nil {
		return 0
	}
	b := signalCount(before, analyze.SignalHistory)
	a := signalCount(after, analyze.SignalHistory)
	if a > b {
		return a - b
	}
	return 0
}

func signalCount(rep *analyze.Report, signal string) int {
	n := 0
	for _, j := range rep.Jobs {
		for k, paths := range j.InputsBySignal {
			if strings.HasPrefix(k, signal) {
				n += len(paths)
			}
		}
	}
	return n
}

// RecallOK reports whether after backtest recall meets minRecall (when set).
func (r *Result) RecallOK(min float64) bool {
	if min <= 0 || r.BacktestAfter == nil {
		return true
	}
	return r.BacktestAfter.Summary.Recall >= min
}
