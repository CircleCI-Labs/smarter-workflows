package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

func init() {
	register("map", "work with the impact map (map build)", func(args []string) int {
		return runMap(args, os.Stdout, os.Stderr)
	})
}

const mapUsage = "usage: circleci-smarter map build --traces DIR --revision SHA --out PATH"

func runMap(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "build" {
		fmt.Fprintln(stderr, mapUsage)
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
			return 0
		}
		return 2
	}
	fs := flag.NewFlagSet("map build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, mapUsage)
		fmt.Fprintln(stderr, "Merges every *.tsv trace in DIR into one impact map.")
		fs.PrintDefaults()
	}
	traces := fs.String("traces", "", "directory with the trace TSV files (required)")
	revision := fs.String("revision", "", "the commit the traces were taken at (required)")
	out := fs.String("out", "", "where to write the impact map JSON (required)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "circleci-smarter map build: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	for _, req := range []struct{ flag, value string }{{"--traces", *traces}, {"--revision", *revision}, {"--out", *out}} {
		if req.value == "" {
			fmt.Fprintf(stderr, "circleci-smarter map build: %s is required\n%s\n", req.flag, mapUsage)
			return 2
		}
	}

	list, err := impactmap.ReadTraceDir(*traces)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter map build: %v\n", err)
		return 1
	}
	m, err := impactmap.Merge(list, *revision, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter map build: %s: %v\n", *traces, err)
		return 1
	}
	data, err := m.Marshal()
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter map build: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(stderr, "circleci-smarter map build: %v\n", err)
		return 1
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "circleci-smarter map build: %v\n", err)
		return 1
	}
	complete := 0
	for _, j := range m.Jobs {
		if j.Complete {
			complete++
		}
	}
	fmt.Fprintf(stdout, "circleci-smarter map build: %d traces, %d jobs (%d complete), written to %s\n", len(list), len(m.Jobs), complete, *out)
	return 0
}
