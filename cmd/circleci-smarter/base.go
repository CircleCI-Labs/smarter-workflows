package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
)

func init() {
	register("base", "print the diff base commit of this checkout, or nothing for a full run", func(args []string) int {
		return runBase(args, os.Stdout, os.Stderr)
	})
}

// flagWasGiven reports whether the flag was set on the command line, as
// opposed to left at its default.
func flagWasGiven(fs *flag.FlagSet, name string) bool {
	given := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			given = true
		}
	})
	return given
}

// runBase prints the diff base SHA, or nothing when the branch gets a full run
// (the default branch, a tag, any git failure). It exits 0 in all of those
// cases: a missing base is not an error, it is how a full run is asked for.
func runBase(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("base", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: circleci-smarter base [--default-branch main] [--branch $CIRCLE_BRANCH]")
		fmt.Fprintln(stderr, "Prints the merge base of origin/<default-branch> and HEAD, or nothing when the branch gets a full run.")
		fs.PrintDefaults()
	}
	defaultBranch := fs.String("default-branch", "main", "the default branch")
	branch := fs.String("branch", "", "the branch being built (default: $CIRCLE_BRANCH)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "circleci-smarter base: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if !flagWasGiven(fs, "branch") {
		*branch = os.Getenv("CIRCLE_BRANCH")
	}

	res := diff.Git{Dir: "."}.Base(context.Background(), *defaultBranch, *branch, os.Getenv("CIRCLE_TAG"))
	if res.SHA == "" {
		// Stdout stays empty: callers capture it. The reason is for the log.
		fmt.Fprintf(stderr, "circleci-smarter base: no diff base (%s); the branch gets a full run\n", res.Why)
		return 0
	}
	fmt.Fprintln(stdout, res.SHA)
	return 0
}
