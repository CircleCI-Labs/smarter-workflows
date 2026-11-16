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
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/ai"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/pipelineparams"
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
	mapPath := fs.String("map", "", "impact map file (default: run in-process analyze)")
	repoDir := fs.String("repo", ".", "repository root for analyze")
	learned := fs.String("learned", "", "learned layer JSON to merge into analyze")
	analyzeReport := fs.String("analyze-report", "", "write analyze Markdown report here")
	aiReview := fs.String("ai-review", "off", "plan-time AI review: off, shadow, or on")
	aiReviewModel := fs.String("ai-review-model", "claude-haiku-5-5", "model for plan-time AI review")
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
	gateJob := fs.String("gate-job", planner.DefaultGateJob, "the name of the gate job (a no-op job: no executor, no credits)")
	policyBundle := fs.String("policy-bundle", "", "a policy bundle directory: check the plan against it, repair, or fall back to a full run")
	policyMeta := fs.String("policy-meta", "", "JSON (or the path of a JSON file) passed to the policies as data.meta (default: built from the CircleCI environment)")
	out := fs.String("out", "/tmp/smarter/continue.yml", "where to write the continuation config")
	reportDir := fs.String("report-dir", "/tmp/smarter/plan", "where to write plan.json and plan.md")
	paramsPath := fs.String("parameters", "", "pipeline parameter values, as a JSON object (for when/unless and continue)")
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
	opts.GateJob = *gateJob
	if err := opts.Validate(); err != nil {
		return usage("%v", err)
	}
	if *paramsPath != "" {
		params, err := pipelineparams.Load(*paramsPath)
		if err != nil {
			fmt.Fprintf(stderr, "circleci-smarter plan: %v\n", err)
			return planExitFatal
		}
		opts.Parameters = params
	}

	// From here on, the config is the one thing that must be usable.
	config, err := os.ReadFile(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter plan: cannot read the config: %v\n", err)
		return planExitFatal
	}

	ctx := context.Background()
	in := planner.Input{Config: config}
	var analyzeMs float64
	git := diff.Git{Dir: *repoDir}
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
		in.Checker, in.GuardrailsProblem = planLoadGuardrails(*policyBundle, *policyMeta)
	}

	wfName, err := planWorkflowName(config, *workflow)
	if err != nil {
		var ue *planner.UsageError
		if errors.As(err, &ue) {
			return usage("%v", err)
		}
		fmt.Fprintf(stderr, "circleci-smarter plan: %v\n", err)
		return planExitFatal
	}
	if opts.Workflow == "" {
		opts.Workflow = wfName
	}
	if flagWasGiven(fs, "map") {
		in.Map, in.MapProblem = planLoadMap(*mapPath)
	} else {
		headRev := opts.Head
		if headRev == "" {
			headRev = *head
		}
		ar := planRunAnalyze(ctx, *repoDir, absConfig(*repoDir, *cfgPath), wfName, headRev, *learned, *analyzeReport, stderr)
		in.Map, in.MapProblem, in.MapJobSummaries = ar.Map, ar.MapProblem, ar.Summaries
		in.MapFromAnalyze = ar.Map != nil
		analyzeMs = ar.Elapsed.Seconds() * 1000
		for _, w := range ar.Warnings {
			fmt.Fprintf(stderr, "circleci-smarter plan: warning: %s\n", w)
		}
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

	reviewMode := strings.ToLower(strings.TrimSpace(*aiReview))
	switch reviewMode {
	case "off", "shadow", "on":
	default:
		return usage("--ai-review must be off, shadow, or on, not %q", *aiReview)
	}
	var reviewer *ai.Reviewer
	if reviewMode != "off" {
		reviewer = ai.NewReviewer(ai.ReviewerConfig{
			Model:   *aiReviewModel,
			BaseURL: os.Getenv("ANTHROPIC_BASE_URL"),
		})
	}
	aiOut := planAIReview(ctx, reviewMode, config, wfName, opts.Base, opts.Head, *branch, *tag, git, res.Report, in.MapJobSummaries, reviewer)
	res.Report.Warnings = append(res.Report.Warnings, aiOut.Warnings...)
	if aiOut.Report != nil {
		res.Report.AIReview = aiOut.Report
	}
	if len(aiOut.Added) > 0 {
		in.AIAdded = aiOut.Added
		res, err = planner.Run(ctx, in, opts)
		if err != nil {
			var ue *planner.UsageError
			if errors.As(err, &ue) {
				return usage("%v", err)
			}
			fmt.Fprintf(stderr, "circleci-smarter plan: %v\n", err)
			return planExitFatal
		}
		if aiOut.Report != nil {
			res.Report.AIReview = aiOut.Report
		}
		res.Report.Warnings = append(res.Report.Warnings, aiOut.Warnings...)
	}

	if err := validOutputPath(*reportDir); err != nil {
		fmt.Fprintf(stderr, "circleci-smarter plan: cannot write the report: output path: %v\n", err)
		return planExitFatal
	}
	if err := report.Write(*reportDir, res.Report); err != nil {
		fmt.Fprintf(stderr, "circleci-smarter plan: cannot write the report: %v\n", err)
		return planExitFatal
	}
	if res.Parameters != nil {
		data, err := json.Marshal(res.Parameters)
		if err != nil {
			fmt.Fprintf(stderr, "circleci-smarter plan: cannot encode parameters: %v\n", err)
			return planExitFatal
		}
		if err := os.WriteFile(filepath.Join(*reportDir, "parameters.json"), data, 0o644); err != nil {
			fmt.Fprintf(stderr, "circleci-smarter plan: cannot write parameters: %v\n", err)
			return planExitFatal
		}
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
	if err := writeOutputFile(*out, res.Config, 0o600); err != nil {
		fmt.Fprintf(stderr, "circleci-smarter plan: cannot write the config: %v\n", err)
		return planExitFatal
	}
	if analyzeMs > 0 {
		summary += fmt.Sprintf("; analyze %.0fms", analyzeMs)
	}
	fmt.Fprintf(stdout, "circleci-smarter plan: %s\n  config: %s\n  report: %s\n", summary, *out, *reportDir)
	return planExitOK
}

// planLoadMap reads the impact map. A map that is missing or unusable is not
// an error: it means a full run, and the reason goes in the report.
func planLoadMap(path string) (*impactmap.Map, string) {
	var (
		lastErr error
		missing = true
	)
	for _, p := range impactmap.LoadCandidates(path) {
		m, err := impactmap.Load(p)
		switch {
		case err == nil:
			return m, ""
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			missing = false
			lastErr = err
		}
	}
	if missing {
		return nil, "there is no impact map at " + path
	}
	return nil, fmt.Sprintf("%s: %v", path, lastErr)
}

// planLoadGuardrails builds the guardrail checker. When it cannot (no policy
// support in this binary, a bad bundle or metadata) it returns a problem
// instead, and the planner falls back to a full plan.
func planLoadGuardrails(bundle, metaFlag string) (guard.Checker, string) {
	if loadGuardrails == nil {
		return nil, "policy support is not built into this binary"
	}
	meta, err := planPolicyMeta(metaFlag)
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
// JSON object, or the file that holds one. Without the flag it is nil, and the
// policy package builds the document from the CircleCI environment, as
// `policy check` does.
func planPolicyMeta(flagValue string) (map[string]any, error) {
	v := strings.TrimSpace(flagValue)
	if v == "" {
		return nil, nil
	}
	raw := []byte(v)
	if !strings.HasPrefix(v, "{") {
		b, err := os.ReadFile(v)
		if err != nil {
			return nil, fmt.Errorf("--policy-meta: %v", err)
		}
		raw = b
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil || meta == nil {
		return nil, fmt.Errorf("--policy-meta is not a JSON object: %v", err)
	}
	return meta, nil
}
