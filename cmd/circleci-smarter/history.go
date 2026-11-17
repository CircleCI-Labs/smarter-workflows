package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/CircleCI-Labs/smarter-workflows/internal/history"
)

const historyFetchUsage = `usage: circleci-smarter history fetch --project-slug SLUG --workflow NAME [--limit N] [--branch NAME]
                                      [--repo DIR] [--host URL] [--concurrency N] --out FILE

Fetches recent pipeline history for backtest. Needs CIRCLE_TOKEN or CIRCLECI_TOKEN.

  --project-slug SLUG   CircleCI project slug (required)
  --workflow NAME     workflow to mine (required)
  --limit N           pipelines to consider, newest first (default 50)
  --branch NAME       only pipelines on this branch
  --repo DIR          git clone for merge-base and changed files (default .)
  --host URL          API host (default $CIRCLECI_HOST or https://circleci.com)
  --concurrency N     concurrent API requests (default 4)
  --out FILE          write the history JSON here (required)
`

func init() {
	register("history", "fetch CircleCI workflow history for backtest", func(args []string) int {
		return runHistory(args, os.Stdout, os.Stderr)
	})
}

func runHistory(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, historyFetchUsage)
		return 2
	}
	switch args[0] {
	case "fetch":
		return runHistoryFetch(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, historyFetchUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "history: unknown subcommand %q\n\n%s", args[0], historyFetchUsage)
		return 2
	}
}

func runHistoryFetch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("history fetch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, historyFetchUsage) }
	projectSlug := fs.String("project-slug", "", "CircleCI project slug")
	workflow := fs.String("workflow", "", "workflow name")
	limit := fs.Int("limit", 50, "pipelines to consider")
	branch := fs.String("branch", "", "branch filter")
	repoDir := fs.String("repo", ".", "git clone directory")
	host := fs.String("host", "", "API host URL")
	concurrency := fs.Int("concurrency", 4, "concurrent API requests")
	outPath := fs.String("out", "", "output JSON path")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "circleci-smarter history fetch: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *projectSlug == "" || *workflow == "" || *outPath == "" {
		fmt.Fprintln(stderr, "circleci-smarter history fetch: --project-slug, --workflow and --out are required")
		return 2
	}
	token := os.Getenv("CIRCLE_TOKEN")
	if token == "" {
		token = os.Getenv("CIRCLECI_TOKEN")
	}
	if token == "" {
		fmt.Fprintln(stderr, "circleci-smarter history fetch: set CIRCLE_TOKEN or CIRCLECI_TOKEN")
		return 2
	}
	baseHost := *host
	if baseHost == "" {
		baseHost = os.Getenv("CIRCLECI_HOST")
	}
	if baseHost == "" {
		baseHost = "https://circleci.com"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := history.NewClient(baseHost, token)
	out, err := os.Create(*outPath)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter history fetch: cannot write %s: %v\n", *outPath, err)
		return 1
	}
	defer out.Close()

	opts := history.FetchOptions{
		ProjectSlug: *projectSlug,
		Workflow:    *workflow,
		Limit:       *limit,
		Branch:      *branch,
		RepoDir:     *repoDir,
		Concurrency: *concurrency,
	}
	if _, err := history.Fetch(ctx, client, opts, out, stderr); err != nil {
		if ctx.Err() != nil {
			return 130
		}
		fmt.Fprintf(stderr, "circleci-smarter history fetch: %v\n", err)
		return 1
	}
	return 0
}
