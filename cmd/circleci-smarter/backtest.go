package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

func init() {
	register("backtest", "measure recall and job-time savings against historical CI runs", func(args []string) int {
		return runBacktest(args, os.Stdout, os.Stderr)
	})
}

func runBacktest(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("backtest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: circleci-smarter backtest --history PATH --map PATH --config PATH --workflow NAME [flags]")
		fs.PrintDefaults()
	}
	historyPath := fs.String("history", "", "JSON file of historical commits (required)")
	mapPath := fs.String("map", "", "impact map JSON or gzip (required)")
	cfgPath := fs.String("config", ".circleci/continue.yml", "full continuation config")
	workflow := fs.String("workflow", "ci", "workflow to plan")
	defaultBranch := fs.String("default-branch", "main", "the default branch")
	mandatory := fs.String("mandatory", "", "comma-separated mandatory jobs")
	format := fs.String("format", "md", "report format: md or json")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "circleci-smarter backtest: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *historyPath == "" || *mapPath == "" {
		fmt.Fprintln(stderr, "circleci-smarter backtest: --history and --map are required")
		return 2
	}
	switch *format {
	case "md", "json":
	default:
		fmt.Fprintf(stderr, "circleci-smarter backtest: --format must be md or json, not %q\n", *format)
		return 2
	}

	cfg, err := os.ReadFile(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter backtest: cannot read config: %v\n", err)
		return 1
	}
	m, err := impactmap.Load(impactmap.ResolveMapPath(*mapPath))
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter backtest: cannot read map: %v\n", err)
		return 1
	}
	hf, err := os.Open(*historyPath)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter backtest: cannot open history: %v\n", err)
		return 1
	}
	defer hf.Close()
	history, err := backtest.DecodeHistory(hf)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter backtest: %v\n", err)
		return 1
	}

	opts := backtest.DefaultOptions()
	opts.Workflow = *workflow
	opts.DefaultBranch = *defaultBranch
	if *mandatory != "" {
		opts.Mandatory = planner.SplitList(*mandatory)
	}

	report, err := backtest.Run(context.Background(), cfg, m, history, opts)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter backtest: %v\n", err)
		return 1
	}

	switch *format {
	case "json":
		data, err := report.JSON()
		if err != nil {
			fmt.Fprintf(stderr, "circleci-smarter backtest: %v\n", err)
			return 1
		}
		if _, err := stdout.Write(data); err != nil {
			fmt.Fprintf(stderr, "circleci-smarter backtest: %v\n", err)
			return 1
		}
	default:
		fmt.Fprint(stdout, report.Markdown())
	}
	return 0
}
