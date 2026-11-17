package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/analyze"
	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
	"github.com/CircleCI-Labs/smarter-workflows/internal/learn"
)

const learnExitLowRecall = 3

func init() {
	register("learn", "refresh the learned layer and measure recall against history", func(args []string) int {
		return runLearn(args, os.Stdout, os.Stderr)
	})
}

func runLearn(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("learn", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project-slug", "", "CircleCI project slug (required for fetch)")
	var workflows multiFlag
	fs.Var(&workflows, "workflow", "workflow name (repeatable; default ci)")
	limit := fs.Int("limit", 50, "pipelines to consider per workflow")
	repo := fs.String("repo", ".", "git clone directory")
	configPath := fs.String("config", ".circleci/continue.yml", "continuation config")
	paramsPath := fs.String("parameters", "", "pipeline parameter values JSON")
	learned := fs.String("learned", "", "previous learned layer JSON")
	learnedOut := fs.String("learned-out", "", "write refreshed learned layer here (required)")
	historyPath := fs.String("history", "", "history JSON file (skips fetch)")
	historyOut := fs.String("history-out", "", "write fetched history here")
	aiMode := fs.String("ai", "auto", "AI classifier: auto|on|off|dry-run")
	model := fs.String("model", "claude-haiku-5-5", "Anthropic model id")
	minConf := fs.Float64("min-confidence", 0.8, "minimum AI confidence for complete jobs")
	reportMD := fs.String("report", "", "write Markdown report here")
	reportJSON := fs.String("report-json", "", "write JSON report here")
	minRecall := fs.Float64("min-recall", 0, "exit 3 when recall is below this value")
	host := fs.String("host", "", "CircleCI API host")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "circleci-smarter learn: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *learnedOut == "" {
		fmt.Fprintln(stderr, "circleci-smarter learn: --learned-out is required")
		return 2
	}
	wf := []string(workflows)
	if len(wf) == 0 {
		wf = []string{"ci"}
	}
	cfgPath := *configPath
	if !filepath.IsAbs(cfgPath) {
		cfgPath = filepath.Join(*repo, cfgPath)
	}
	planOpts := backtest.DefaultOptions()
	planOpts.Workflow = wf[0]
	var fetcher learn.HistoryFetcher
	if *historyPath == "" {
		if *project == "" {
			fmt.Fprintln(stderr, "circleci-smarter learn: --project-slug is required without --history")
			return 2
		}
		fetcher = learn.LiveFetcher{Host: *host, Log: stderr}
	}
	ctx := context.Background()
	res, err := learn.Run(ctx, learn.Options{
		RepoRoot:       *repo,
		ConfigPath:     cfgPath,
		Workflows:      wf,
		ProjectSlug:    *project,
		HistoryPath:    *historyPath,
		HistoryOutPath: *historyOut,
		LearnedPath:    *learned,
		LearnedOutPath: *learnedOut,
		Limit:          *limit,
		ParametersPath: *paramsPath,
		ReportMD:       *reportMD,
		ReportJSON:     *reportJSON,
		AIMode:         analyze.AIMode(strings.ToLower(*aiMode)),
		Model:          *model,
		MinConfidence:  *minConf,
		MinRecall:      *minRecall,
		Planner:        planOpts,
	}, fetcher, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter learn: %v\n", err)
		return 1
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(stderr, "circleci-smarter learn: warning: %s\n", w)
	}
	if res.BacktestAfter != nil {
		denom := res.BacktestAfter.Summary.CaughtFailures + res.BacktestAfter.Summary.Misses
		if denom == 0 {
			fmt.Fprintf(stdout, "circleci-smarter learn: recall n/a (no failing jobs), savings %.1f%% (%d commits)\n",
				res.BacktestAfter.Summary.SavingsPercent, res.BacktestAfter.Commits)
		} else {
			fmt.Fprintf(stdout, "circleci-smarter learn: recall %.1f%%, savings %.1f%% (%d commits)\n",
				100*res.BacktestAfter.Summary.Recall, res.BacktestAfter.Summary.SavingsPercent, res.BacktestAfter.Commits)
		}
	}
	if !res.RecallOK(*minRecall) {
		return learnExitLowRecall
	}
	return 0
}
