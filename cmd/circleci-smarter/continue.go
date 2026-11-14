package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/CircleCI-Labs/smarter-workflows/internal/continuation"
)

// Exit codes of `continue` (spec: "CLI contract").
const (
	continueExitFatal = 1
	continueExitUsage = 2
)

const continueUsage = `usage: circleci-smarter continue --config PATH [--parameters PATH]

Ends a setup workflow: sends the continuation config to CircleCI.

  --config PATH      the continuation config (YAML)
  --parameters PATH  pipeline parameters, as a JSON object (default: none)

Reads the continuation key from CIRCLE_CONTINUATION_KEY and the API host from
CIRCLECI_API_HOST (default https://circleci.com). Retries three times, after 1,
2 and 4 seconds, when the API answers 429 or 5xx.

Prints the HTTP status and nothing else: never the key, the config or the
answer of the API.

Exit status: 0 on success, 1 on an error, 2 for a usage error.
`

// continueBackoff, if set, replaces the waits between retries. Tests set it.
var continueBackoff []time.Duration

func init() {
	register("continue", "send the config to CircleCI and continue the pipeline", func(args []string) int {
		return runContinue(args, os.Stdout, os.Stderr, os.Getenv)
	})
}

func runContinue(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("continue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, continueUsage) }
	configPath := fs.String("config", "", "the continuation config")
	paramsPath := fs.String("parameters", "", "pipeline parameters, as a JSON object")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return continueExitUsage
	}
	switch {
	case fs.NArg() > 0:
		fmt.Fprintf(stderr, "continue: unexpected argument %q\n\n%s", fs.Arg(0), continueUsage)
		return continueExitUsage
	case *configPath == "":
		fmt.Fprintf(stderr, "continue: --config is required\n\n%s", continueUsage)
		return continueExitUsage
	}

	key := getenv("CIRCLE_CONTINUATION_KEY")
	if key == "" {
		fmt.Fprintln(stderr, "continue: CIRCLE_CONTINUATION_KEY is not set")
		return continueExitFatal
	}
	config, err := os.ReadFile(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "continue: %v\n", err)
		return continueExitFatal
	}
	var params map[string]any
	if *paramsPath != "" {
		if params, err = readContinueParameters(*paramsPath); err != nil {
			fmt.Fprintf(stderr, "continue: %v\n", err)
			return continueExitFatal
		}
	}

	client := &continuation.Client{
		Host:      strings.TrimSpace(getenv("CIRCLECI_API_HOST")),
		Key:       key,
		Backoff:   continueBackoff,
		UserAgent: "circleci-smarter/" + version,
	}
	status, err := client.Continue(context.Background(), config, params)
	if err != nil {
		// The client's errors name the HTTP status and never the key or what the
		// API said.
		fmt.Fprintf(stderr, "continue: %v\n", err)
		return continueExitFatal
	}
	fmt.Fprintf(stdout, "continue: HTTP %d\n", status)
	return 0
}

// readContinueParameters reads a JSON object. Numbers are kept as they are
// written.
func readContinueParameters(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var params map[string]any
	if err := dec.Decode(&params); err != nil {
		return nil, fmt.Errorf("%s: pipeline parameters must be a JSON object: %v", path, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%s: pipeline parameters must be a single JSON object", path)
	}
	return params, nil
}
