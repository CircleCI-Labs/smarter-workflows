package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
	"github.com/CircleCI-Labs/smarter-workflows/internal/report"
)

func init() {
	register("plan", "plan which jobs of a workflow a change can reach and write the continuation config", func(args []string) int {
		return runPlan(args, os.Stdout, os.Stderr)
	})
}

const (
	planExitOK         = 0
	planExitFatal      = 1
	planExitUsage      = 2
	planExitGuardrails = 3
)

// runPlan is the `plan` command (spec, "CLI contract").
//
//	0  the plan is written (a full plan included)
//	1  the config could not be read, or the outputs could not be written
//	2  bad flags, or a workflow that cannot be chosen
//	3  the guardrails fail even for the full plan: do not continue
func runPlan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", ".circleci/continue.yml", "the full continuation config (the catalog)")
	workflow := fs.String("workflow", "", "the workflow to plan (default: the only workflow)")
	mapPath := fs.String("map", "/tmp/smarter/impact-map.json", "the impact map; a missing or invalid map means a full run")
	base := fs.String("base", "", "the diff base (default: what the base command prints)")
	head := fs.String("head", "HEAD", "the head commit")
	branch := fs.String("branch", "", "the branch (default: $CIRCLE_BRANCH)")
	tag := fs.String("tag", "", "the tag (default: $CIRCLE_TAG)")
	defaultBranch := fs.String("default-branch", "main", "the default branch")
	trigger := fs.String("trigger-type", "", "the pipeline's trigger type; \"schedule\" means a full run")
	mode := fs.String("mode", "shadow", "shadow (plan and report, run everything) or select (run only the plan)")
	mandatory := fs.String("mandatory", "", "comma-separated jobs that run in every pipeline")
	fullRunPaths := fs.String("full-run-paths", planner.DefaultFullRunPaths, "comma-separated globs; a change to a matching path means a full run")
	extraInputs := fs.String("extra-inputs", "", "declared inputs, job=glob|glob,job2=glob")
	ignorePaths := fs.String("ignore-paths", "", "comma-separated globs that never select a job")
	forceFull := fs.Bool("force-full", false, "plan a full run")
	trace := fs.String("trace", "auto", "add the trace steps: auto (full runs on the default branch), on or off")
	traceOrb := fs.Bool("trace-orb-jobs", false, "also trace orb jobs (assumes their executor is Linux)")
	traceReset := fs.String("trace-reset", "on", "reset file access times after checkout in traced jobs: on or off")
	gateJob := fs.String("gate-job", planner.DefaultGateJob, "the name of the gate job")
	gateImage := fs.String("gate-image", planner.DefaultGateImage, "the docker image of the gate and map jobs")
	smarterBin := fs.String("smarter-bin", "", "where the map job finds this binary (default: the path it runs from)")
	policyBundle := fs.String("policy-bundle", "", "a policy bundle directory: check the plan against it, repair, or fall back to a full run")
	policyMeta := fs.String("policy-meta", "", "JSON (or the path of a JSON file) passed to the policies as data.meta (default: built from the environment)")
	out := fs.String("out", "/tmp/smarter/continue.yml", "where to write the continuation config")
	reportDir := fs.String("report-dir", "/tmp/smarter/plan", "where to write plan.json and plan.md")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return planExitOK
		}
		return planExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "circleci-smarter plan: unexpected argument %q\n", fs.Arg(0))
		return planExitUsage
	}
	if !flagWasGiven(fs, "branch") {
		*branch = os.Getenv("CIRCLE_BRANCH")
	}
	if !flagWasGiven(fs, "tag") {
		*tag = os.Getenv("CIRCLE_TAG")
	}

	usage := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "circleci-smarter plan: "+format+"\n", a...)
		return planExitUsage
	}
	switch *traceReset {
	case "on", "off":
	default:
		return usage("--trace-reset must be on or off, not %q", *traceReset)
	}
	extra, err := planner.ParseExtraInputs(*extraInputs)
	if err != nil {
		return usage("--extra-inputs: %v", err)
	}
	opts := planner.DefaultOptions()
	opts.Mode = planner.Mode(*mode)
	opts.Workflow = *workflow
	opts.Branch, opts.Tag, opts.DefaultBranch = *branch, *tag, *defaultBranch
	opts.TriggerType = *trigger
	opts.ForceFull = *forceFull
	opts.Mandatory = planner.SplitList(*mandatory)
	opts.FullRunPaths = planner.SplitList(*fullRunPaths)
	opts.ExtraInputs = extra
	opts.IgnorePaths = planner.SplitList(*ignorePaths)
	opts.Trace = planner.TraceMode(*trace)
	opts.TraceOrbJobs = *traceOrb
	opts.TraceReset = *traceReset == "on"
	opts.GateJob, opts.GateImage = *gateJob, *gateImage
	opts.Version = version
	opts.BinPath = *smarterBin
	if opts.BinPath == "" {
		opts.BinPath = planRunningBinary()
	}
	if err := opts.Validate(); err != nil {
		return usage("%v", err)
	}

	// From here on, the config is the one thing that must be usable.
	config, err := os.ReadFile(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter plan: cannot read the config: %v\n", err)
		return planExitFatal
	}

	ctx := context.Background()
	in := planner.Input{Config: config}
	in.Map, in.MapProblem = planLoadMap(*mapPath)

	git := diff.Git{Dir: "."}
	in.Repo = git
	if sha, err := git.ResolveCommit(ctx, *head); err == nil {
		opts.Head = sha
		if msg, err := git.HeadMessage(ctx, sha); err == nil {
			in.CommitMessage = msg
		}
	}
	if *base != "" {
		if sha, err := git.ResolveCommit(ctx, *base); err == nil {
			opts.Base = sha
		} else {
			opts.NoBaseReason = fmt.Sprintf("--base %q cannot be resolved", *base)
		}
	} else {
		res := git.Base(ctx, *defaultBranch, *branch, *tag)
		opts.Base, opts.NoBaseReason = res.SHA, res.Why
	}

	if *policyBundle != "" {
		in.Checker, in.GuardrailsProblem = planLoadGuardrails(*policyBundle, *policyMeta, *branch, *tag)
	}

	res, err := planner.Run(ctx, in, opts)
	var ue *planner.UsageError
	switch {
	case errors.As(err, &ue):
		return usage("%v", err)
	case err != nil:
		fmt.Fprintf(stderr, "circleci-smarter plan: %v\n", err)
		return planExitFatal
	}

	if err := report.Write(*reportDir, res.Report); err != nil {
		fmt.Fprintf(stderr, "circleci-smarter plan: cannot write the report: %v\n", err)
		return planExitFatal
	}
	for _, w := range res.Report.Warnings {
		fmt.Fprintf(stderr, "circleci-smarter plan: warning: %s\n", w)
	}
	summary := fmt.Sprintf("%s mode, %d of %d jobs run", res.Report.Mode, res.Report.RunCount(), len(res.Report.Jobs))
	if res.Report.Full {
		summary += " (full run: " + res.Report.FullReason + ")"
	}

	if res.GuardrailsFailed {
		fmt.Fprintf(stderr, "circleci-smarter plan: the guardrails reject even the full plan, so the pipeline must not continue (report: %s)\n", *reportDir)
		for _, v := range res.Report.Guardrails.Violations {
			if v.Hard {
				fmt.Fprintf(stderr, "  - %s: %s\n", v.Rule, v.Message)
			}
		}
		return planExitGuardrails
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(stderr, "circleci-smarter plan: cannot write the config: %v\n", err)
		return planExitFatal
	}
	if err := os.WriteFile(*out, res.Config, 0o644); err != nil {
		fmt.Fprintf(stderr, "circleci-smarter plan: cannot write the config: %v\n", err)
		return planExitFatal
	}
	fmt.Fprintf(stdout, "circleci-smarter plan: %s\n  config: %s\n  report: %s\n", summary, *out, *reportDir)
	return planExitOK
}

// planRunningBinary is the path this process runs from, with symlinks resolved.
func planRunningBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return "circleci-smarter"
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		return real
	}
	return exe
}

// planLoadMap reads the impact map. A map that is missing or unusable is not
// an error: it means a full run, and the reason goes in the report.
func planLoadMap(path string) (*impactmap.Map, string) {
	m, err := impactmap.Load(path)
	switch {
	case err == nil:
		return m, ""
	case errors.Is(err, os.ErrNotExist):
		return nil, "there is no impact map at " + path
	default:
		return nil, fmt.Sprintf("%s: %v", path, err)
	}
}

// planLoadGuardrails builds the guardrail checker. When it cannot (no policy
// support in this binary, a bad bundle or metadata) it returns a problem
// instead, and the planner falls back to a full plan.
func planLoadGuardrails(bundle, metaFlag, branch, tag string) (guard.Checker, string) {
	if loadGuardrails == nil {
		return nil, "policy support is not built into this binary"
	}
	meta, err := planPolicyMeta(metaFlag, branch, tag)
	if err != nil {
		return nil, err.Error()
	}
	checker, err := loadGuardrails(bundle, meta)
	if err != nil {
		return nil, fmt.Sprintf("cannot load the policy bundle %s: %v", bundle, err)
	}
	return checker, ""
}

// planPolicyMeta is the data.meta document of the policies: the --policy-meta
// JSON (or the file it names), or a document built from the CircleCI
// environment. Only a few well-known variables are read, and nothing is printed.
func planPolicyMeta(flagValue, branch, tag string) (map[string]any, error) {
	if v := strings.TrimSpace(flagValue); v != "" {
		raw := []byte(v)
		if !strings.HasPrefix(v, "{") {
			b, err := os.ReadFile(v)
			if err != nil {
				return nil, fmt.Errorf("--policy-meta: %v", err)
			}
			raw = b
		}
		var meta map[string]any
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, fmt.Errorf("--policy-meta is not a JSON object: %v", err)
		}
		return meta, nil
	}

	vcs := map[string]any{}
	set := func(m map[string]any, key, value string) {
		if value != "" {
			m[key] = value
		}
	}
	set(vcs, "branch", branch)
	set(vcs, "tag", tag)
	set(vcs, "revision", os.Getenv("CIRCLE_SHA1"))
	set(vcs, "origin_repository_url", os.Getenv("CIRCLE_REPOSITORY_URL"))
	set(vcs, "target_repository_url", os.Getenv("CIRCLE_REPOSITORY_URL"))
	set(vcs, "review_url", os.Getenv("CIRCLE_PULL_REQUEST"))

	meta := map[string]any{"vcs": vcs}
	set(meta, "project_id", os.Getenv("CIRCLE_PROJECT_ID"))
	set(meta, "branch", branch)
	if n, err := strconv.Atoi(os.Getenv("CIRCLE_BUILD_NUM")); err == nil {
		meta["build_number"] = n
	}
	return meta, nil
}
