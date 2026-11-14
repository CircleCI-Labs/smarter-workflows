package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/catalog"
	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

// Exit codes of `layout check` (spec: "CLI contract").
const (
	layoutExitFatal  = 1
	layoutExitUsage  = 2
	layoutExitErrors = 3
)

const layoutUsage = `usage: circleci-smarter layout check [--config PATH] [--workflow NAME] [--catalog FILE]
                                     [--policy-bundle DIR] [--mandatory JOBS] [--restricted-contexts CONTEXTS]
                                     [--gate-job NAME] [--format json|text]

Checks the layout of one workflow, the way jobs wait for each other, before it
runs: a requirement that points nowhere, a cycle, a job that attaches a workspace
that no job before it persists, a restricted context used without an approval, a
mandatory job that is missing, a gate that is missing, is not a no-op job or does
not wait for every job that runs, and an approval that the filters can leave out.

  --config PATH               the config to check (default .circleci/continue.yml)
  --workflow NAME             the workflow to check (default: the only one)
  --catalog FILE              what 'circleci-smarter catalog' printed: it says what the jobs
                              that this config does not define persist and attach
  --policy-bundle DIR         also check the config against this guardrail bundle; each
                              violation is a finding with the code policy:<rule>
  --mandatory JOBS            comma-separated jobs that the workflow must hold
  --restricted-contexts LIST  comma-separated contexts that only a job downstream of an
                              approval may use
  --gate-job NAME             the gate job (default smarter-gate); "" skips the gate checks
  --format FMT                json (default) or text

Each finding has a code, a severity (error or warning), the job it is about, a
message and a fix hint.

Exit status: 0 when there are no errors (warnings are fine), 3 for errors, 1 for
a failure of the check itself, 2 for a usage error.
`

func init() {
	register("layout", "check the layout of a workflow (layout check)", func(args []string) int {
		return runLayout(args, os.Stdout, os.Stderr)
	})
}

func runLayout(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, layoutUsage)
		return layoutExitUsage
	}
	switch args[0] {
	case "check":
		return runLayoutCheck(args[1:], stdout, stderr)
	case "-h", "-help", "--help", "help":
		fmt.Fprint(stdout, layoutUsage)
		return 0
	default:
		fmt.Fprintf(stderr, "layout: unknown subcommand %q\n\n%s", args[0], layoutUsage)
		return layoutExitUsage
	}
}

func runLayoutCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("layout check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, layoutUsage) }
	configPath := fs.String("config", ".circleci/continue.yml", "the config to check")
	workflow := fs.String("workflow", "", "the workflow to check (default: the only one)")
	catalogPath := fs.String("catalog", "", "the catalog JSON that the catalog command printed")
	bundleDir := fs.String("policy-bundle", "", "a guardrail bundle to check the config against")
	mandatory := fs.String("mandatory", "", "comma-separated jobs that the workflow must hold")
	restricted := fs.String("restricted-contexts", "", "comma-separated contexts that need an approval upstream")
	gateJob := fs.String("gate-job", planner.DefaultGateJob, "the gate job (empty skips the gate checks)")
	format := fs.String("format", "json", "json or text")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return layoutExitUsage
	}
	switch {
	case fs.NArg() > 0:
		fmt.Fprintf(stderr, "layout check: unexpected argument %q\n\n%s", fs.Arg(0), layoutUsage)
		return layoutExitUsage
	case *format != "json" && *format != "text":
		fmt.Fprintf(stderr, "layout check: --format must be json or text, not %q\n\n%s", *format, layoutUsage)
		return layoutExitUsage
	}

	data, err := os.ReadFile(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "layout check: cannot read the config: %v\n", err)
		return layoutExitFatal
	}
	opts := planner.LayoutOptions{
		Workflow:           *workflow,
		Mandatory:          planner.SplitList(*mandatory),
		RestrictedContexts: planner.SplitList(*restricted),
		GateJob:            *gateJob,
	}
	if *catalogPath != "" {
		if opts.Catalog, err = readLayoutCatalog(*catalogPath); err != nil {
			fmt.Fprintf(stderr, "layout check: %v\n", err)
			return layoutExitFatal
		}
	}
	if *bundleDir != "" {
		if opts.Checker, err = layoutChecker(*bundleDir); err != nil {
			fmt.Fprintf(stderr, "layout check: %v\n", err)
			return layoutExitFatal
		}
	}

	res, err := planner.CheckLayout(context.Background(), data, opts)
	var ue *planner.UsageError
	switch {
	case errors.As(err, &ue):
		fmt.Fprintf(stderr, "layout check: %v\n", err)
		return layoutExitUsage
	case err != nil:
		fmt.Fprintf(stderr, "layout check: %s: %v\n", *configPath, err)
		return layoutExitFatal
	}

	if *format == "json" {
		err = writeLayoutJSON(stdout, res)
	} else {
		err = writeLayoutText(stdout, res)
	}
	if err != nil {
		fmt.Fprintf(stderr, "layout check: %v\n", err)
		return layoutExitFatal
	}
	if res.Errors() > 0 {
		return layoutExitErrors
	}
	return 0
}

// readLayoutCatalog reads the JSON that the catalog command printed.
func readLayoutCatalog(path string) (*catalog.Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the catalog: %v", err)
	}
	cat, err := catalog.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %v (it must be what `circleci-smarter catalog` printed)", path, err)
	}
	return cat, nil
}

// layoutChecker loads the guardrail bundle, with the metadata of the
// environment, as `policy check` does.
func layoutChecker(dir string) (guard.Checker, error) {
	if loadGuardrails == nil {
		return nil, errors.New("policy support is not built into this binary")
	}
	checker, err := loadGuardrails(dir, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot load the policy bundle %s: %v", dir, err)
	}
	return checker, nil
}

// layoutJSON is what `layout check --format json` prints.
type layoutJSON struct {
	Version  int    `json:"version"`
	Workflow string `json:"workflow"`
	// OK is true when there is no finding of severity error.
	OK       bool              `json:"ok"`
	Findings []planner.Finding `json:"findings"`
}

func writeLayoutJSON(w io.Writer, res *planner.LayoutResult) error {
	out := layoutJSON{Version: 1, Workflow: res.Workflow, OK: res.Errors() == 0, Findings: res.Findings}
	if out.Findings == nil {
		out.Findings = []planner.Finding{}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func writeLayoutText(w io.Writer, res *planner.LayoutResult) error {
	var b strings.Builder
	errs := res.Errors()
	warns := len(res.Findings) - errs
	if len(res.Findings) == 0 {
		fmt.Fprintf(&b, "workflow %s: no findings\n", res.Workflow)
	} else {
		fmt.Fprintf(&b, "workflow %s: %s, %s\n", res.Workflow, plural(errs, "error"), plural(warns, "warning"))
	}
	for _, f := range res.Findings {
		job := ""
		if f.Job != "" {
			job = " [" + f.Job + "]"
		}
		fmt.Fprintf(&b, "  %-7s %s%s: %s\n", f.Severity, f.Code, job, f.Message)
		if f.FixHint != "" {
			fmt.Fprintf(&b, "          fix: %s\n", f.FixHint)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
