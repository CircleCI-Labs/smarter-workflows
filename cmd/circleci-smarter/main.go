// Command circleci-smarter is the Smarter Workflows planner. It runs on its
// own or as the `circleci smarter` CLI extension. See docs/spec/v0.1.md.
package main

import (
	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"

	"fmt"
	"os"
	"sort"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

type command struct {
	summary string
	run     func(args []string) int
}

var commands = map[string]command{}

// register adds a subcommand. Each command lives in its own file and calls
// register from init(), so parallel work never edits this file.
func register(name, summary string, run func(args []string) int) {
	commands[name] = command{summary: summary, run: run}
}

func init() {
	register("version", "print the version", func([]string) int {
		fmt.Println(version)
		return 0
	})
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: circleci-smarter <command> [flags]")
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(os.Stderr, "  %-12s %s\n", n, commands[n].summary)
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	c, ok := commands[os.Args[1]]
	if !ok {
		usage()
		os.Exit(2)
	}
	os.Exit(c.run(os.Args[2:]))
}

// loadGuardrails builds the guardrail checker for `plan --policy-bundle`. The
// policy command's file sets it, so plan.go needs no import of internal/policy.
var loadGuardrails func(bundleDir string, meta map[string]any) (guard.Checker, error)
