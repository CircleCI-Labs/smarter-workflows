package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/CircleCI-Labs/smarter-workflows/internal/catalog"
	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

// Exit codes of `catalog` (spec: "CLI contract").
const (
	catalogExitFatal = 1
	catalogExitUsage = 2
)

const catalogUsage = `usage: circleci-smarter catalog [--config PATH] [--workflow NAME] [--map PATH]

Prints, as JSON, the jobs of one workflow of the config: what each one is, where
it runs, what it requires, what it does with workspaces and, with an impact
map, how much of the repository it read. It is what an agent needs to compose a
workflow, and what 'layout check --catalog' reads.

  --config PATH     the continuation config (default .circleci/continue.yml)
  --workflow NAME   the workflow to describe (default: the only one)
  --map PATH        an impact map: adds what each job read (default: none)

Exit status: 0 for a catalog, 1 for an error, 2 for a usage error.
`

func init() {
	register("catalog", "print the jobs of a workflow as JSON, for composing workflows", func(args []string) int {
		return runCatalog(args, os.Stdout, os.Stderr)
	})
}

func runCatalog(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("catalog", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, catalogUsage) }
	configPath := fs.String("config", ".circleci/continue.yml", "the continuation config")
	workflow := fs.String("workflow", "", "the workflow to describe (default: the only one)")
	mapPath := fs.String("map", "", "an impact map")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return catalogExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "circleci-smarter catalog: unexpected argument %q\n\n%s", fs.Arg(0), catalogUsage)
		return catalogExitUsage
	}

	data, err := os.ReadFile(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter catalog: cannot read the config: %v\n", err)
		return catalogExitFatal
	}
	cfg, err := config.Parse(data)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter catalog: %s: %v\n", *configPath, err)
		return catalogExitFatal
	}
	name, err := planner.ChooseWorkflow(cfg, *workflow)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter catalog: %v\n", err)
		return catalogExitUsage
	}

	var m *impactmap.Map
	if *mapPath != "" {
		switch m, err = impactmap.Load(*mapPath); {
		case errors.Is(err, os.ErrNotExist):
			// Not an error: a project that has not traced a full run yet has no map.
			fmt.Fprintf(stderr, "circleci-smarter catalog: warning: there is no impact map at %s, so no job has inputs\n", *mapPath)
			m = nil
		case err != nil:
			fmt.Fprintf(stderr, "circleci-smarter catalog: %s: %v\n", *mapPath, err)
			return catalogExitFatal
		}
	}

	cat, err := catalog.Build(cfg, name, m)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter catalog: %v\n", err)
		return catalogExitFatal
	}
	out, err := cat.JSON()
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter catalog: %v\n", err)
		return catalogExitFatal
	}
	if _, err := stdout.Write(out); err != nil {
		fmt.Fprintf(stderr, "circleci-smarter catalog: %v\n", err)
		return catalogExitFatal
	}
	return 0
}
