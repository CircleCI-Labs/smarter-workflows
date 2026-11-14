package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
	"github.com/CircleCI-Labs/smarter-workflows/internal/policy"
)

// Exit codes of `policy check` (spec: "CLI contract").
const (
	policyExitFatal = 1
	policyExitUsage = 2
	policyExitHard  = 3
)

const policyUsage = `usage: circleci-smarter policy check --config PATH --bundle DIR [--meta PATH] [--format text|json]

Checks a continuation config against the guardrail bundle, the Rego files in DIR.

  --config PATH   the config to check (YAML)
  --bundle DIR    the guardrail bundle; *_test.rego files are ignored
  --meta PATH     JSON or YAML for data.meta (default: built from the CircleCI environment)
  --format FMT    text (default) or json

Exit status: 0 for a pass or soft failures only, 1 for an error (including a
bundle that cannot be evaluated), 2 for a usage error, 3 for a hard violation.
`

func init() {
	register("policy", "check a config against the guardrail bundle (policy check)", func(args []string) int {
		return runPolicy(args, os.Stdout, os.Stderr)
	})

	// plan --policy-bundle runs the same check through this hook, so that
	// plan.go does not import internal/policy.
	loadGuardrails = func(bundleDir string, meta map[string]any) (guard.Checker, error) {
		b, err := policy.Load(bundleDir, meta)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
}

func runPolicy(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, policyUsage)
		return policyExitUsage
	}
	switch args[0] {
	case "check":
		return runPolicyCheck(args[1:], stdout, stderr)
	case "-h", "-help", "--help", "help":
		fmt.Fprint(stdout, policyUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "policy: unknown subcommand %q\n\n%s", args[0], policyUsage)
		return policyExitUsage
	}
}

func runPolicyCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("policy check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, policyUsage) }
	configPath := fs.String("config", "", "the config to check")
	bundleDir := fs.String("bundle", "", "the guardrail bundle directory")
	metaPath := fs.String("meta", "", "JSON or YAML for data.meta")
	format := fs.String("format", "text", "text or json")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return policyExitUsage
	}

	switch {
	case fs.NArg() > 0:
		fmt.Fprintf(stderr, "policy check: unexpected argument %q\n\n%s", fs.Arg(0), policyUsage)
		return policyExitUsage
	case *configPath == "" || *bundleDir == "":
		fmt.Fprintf(stderr, "policy check: --config and --bundle are required\n\n%s", policyUsage)
		return policyExitUsage
	case *format != "text" && *format != "json":
		fmt.Fprintf(stderr, "policy check: --format must be text or json, not %q\n\n%s", *format, policyUsage)
		return policyExitUsage
	}

	config, err := os.ReadFile(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "policy check: %v\n", err)
		return policyExitFatal
	}
	var meta map[string]any
	if *metaPath != "" {
		data, err := os.ReadFile(*metaPath)
		if err != nil {
			fmt.Fprintf(stderr, "policy check: %v\n", err)
			return policyExitFatal
		}
		if meta, err = policy.ParseMeta(data); err != nil {
			fmt.Fprintf(stderr, "policy check: %s: %v\n", *metaPath, err)
			return policyExitFatal
		}
	}

	bundle, err := policy.Load(*bundleDir, meta)
	if err != nil {
		fmt.Fprintf(stderr, "policy check: %v\n", err)
		return policyExitFatal
	}
	res, err := bundle.Check(context.Background(), config)
	if err != nil {
		fmt.Fprintf(stderr, "policy check: %s: %v\n", *configPath, err)
		return policyExitFatal
	}

	if *format == "json" {
		err = writePolicyJSON(stdout, res)
	} else {
		err = writePolicyText(stdout, res)
	}
	if err != nil {
		fmt.Fprintf(stderr, "policy check: %v\n", err)
		return policyExitFatal
	}

	switch res.Status {
	case guard.HardFail:
		return policyExitHard
	case guard.Error:
		return policyExitFatal
	default:
		return 0
	}
}

// writePolicyText prints the status, then one line per violation.
func writePolicyText(w io.Writer, res guard.Result) error {
	if _, err := fmt.Fprintln(w, res.Status); err != nil {
		return err
	}
	for _, v := range res.Violations {
		level := "soft"
		if v.Hard {
			level = "hard"
		}
		if _, err := fmt.Fprintf(w, "  %s  %s: %s\n", level, v.Rule, v.Message); err != nil {
			return err
		}
	}
	return nil
}

// policyViolationJSON is the JSON form of a guard.Violation.
type policyViolationJSON struct {
	Policy      string   `json:"policy,omitempty"`
	Rule        string   `json:"rule"`
	Message     string   `json:"message"`
	Hard        bool     `json:"hard"`
	RequireJobs []string `json:"require_jobs,omitempty"`
}

// writePolicyJSON prints {"status": ..., "violations": [...]}.
func writePolicyJSON(w io.Writer, res guard.Result) error {
	out := struct {
		Status     string                `json:"status"`
		Violations []policyViolationJSON `json:"violations"`
	}{Status: res.Status, Violations: []policyViolationJSON{}}
	for _, v := range res.Violations {
		out.Violations = append(out.Violations, policyViolationJSON{
			Policy:      v.Policy,
			Rule:        v.Rule,
			Message:     v.Message,
			Hard:        v.Hard,
			RequireJobs: v.RequireJobs,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
