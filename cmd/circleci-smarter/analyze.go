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
	"time"

	"github.com/CircleCI-Labs/smarter-workflows/internal/analyze"
)

func init() {
	register("analyze", "infer impact maps from config and source", func(args []string) int {
		return runAnalyze(args, os.Stdout, os.Stderr)
	})
}

func runAnalyze(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", ".", "repository root")
	configPath := fs.String("config", ".circleci/continue.yml", "CircleCI continue config")
	var workflows multiFlag
	fs.Var(&workflows, "workflow", "workflow name (repeatable; default all)")
	revision := fs.String("revision", "", "revision label (default git HEAD)")
	history := fs.String("history", "", "pipeline history JSON for corrections")
	learned := fs.String("learned", "", "learned layer JSON to merge")
	learnedOut := fs.String("learned-out", "", "write learned layer here")
	aiMode := fs.String("ai", "auto", "AI classifier: auto|on|off|dry-run")
	model := fs.String("model", "claude-haiku-5-5", "Anthropic model id")
	minConf := fs.Float64("min-confidence", 0.8, "minimum AI confidence for complete jobs")
	aiMaxTok := fs.Int("ai-max-input-tokens", 90000, "max input tokens per AI request")
	aiBatch := fs.Int("ai-batch-size", 20, "jobs per AI batch")
	aiTimeout := fs.Duration("ai-timeout", 5*time.Minute, "AI layer timeout")
	out := fs.String("out", "smarter-map.json", "write impact map here")
	report := fs.String("report", "", "write Markdown report here")
	reportJSON := fs.String("report-json", "", "write report JSON here")
	truth := fs.String("truth", "", "reference impact map for recall section in report (developer)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	cfgPath := *configPath
	if !filepath.IsAbs(cfgPath) {
		cfgPath = filepath.Join(*repo, cfgPath)
	}
	ctx := context.Background()
	m, rep, _, err := analyze.Run(ctx, analyze.RunOptions{
		RepoRoot:         *repo,
		ConfigPath:       cfgPath,
		Workflows:        []string(workflows),
		Revision:         *revision,
		HistoryPath:      *history,
		LearnedPath:      *learned,
		LearnedOutPath:   *learnedOut,
		AIMode:           analyze.AIMode(strings.ToLower(*aiMode)),
		Model:            *model,
		MinConfidence:    *minConf,
		AIMaxInputTokens: *aiMaxTok,
		AIBatchSize:      *aiBatch,
		AITimeout:        *aiTimeout,
		MapOut:           *out,
		ReportMD:         *report,
		ReportJSON:       *reportJSON,
		TruthPath:        *truth,
	})
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter analyze: %v\n", err)
		return 1
	}
	complete, incomplete := 0, 0
	if rep != nil {
		complete, incomplete = rep.CompleteJobs, rep.IncompleteJobs
	} else {
		for _, j := range m.Jobs {
			if j.Complete {
				complete++
			} else {
				incomplete++
			}
		}
	}
	fmt.Fprintf(stdout, "circleci-smarter analyze: %d jobs (%d complete, %d incomplete)\n",
		len(m.Jobs), complete, incomplete)
	return 0
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
