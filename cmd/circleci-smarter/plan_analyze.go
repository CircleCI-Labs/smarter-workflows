package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/CircleCI-Labs/smarter-workflows/internal/ai"
	"github.com/CircleCI-Labs/smarter-workflows/internal/analyze"
	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
	"github.com/CircleCI-Labs/smarter-workflows/internal/report"
)

type planAnalyzeResult struct {
	Map        *impactmap.Map
	MapProblem string
	Summaries  map[string]string
	Warnings   []string
	Elapsed    time.Duration
}

func planRunAnalyze(ctx context.Context, repo, cfgPath, workflow, head, learned, reportPath string, stderr io.Writer) planAnalyzeResult {
	var warns []string
	learnedPath := ""
	if learned != "" {
		_, w, err := analyze.TryLoadLearned(learned)
		if w != "" {
			warns = append(warns, w)
		} else if err != nil {
			return planAnalyzeResult{MapProblem: fmt.Sprintf("learned layer: %v", err)}
		} else {
			learnedPath = learned
		}
	}
	start := time.Now()
	workflows := []string(nil)
	if workflow != "" {
		workflows = []string{workflow}
	}
	m, rep, _, err := analyze.Run(ctx, analyze.RunOptions{
		RepoRoot:    repo,
		ConfigPath:  cfgPath,
		Workflows:   workflows,
		Revision:    head,
		LearnedPath: learnedPath,
		AIMode:      analyze.AIModeOff,
		ReportMD:    reportPath,
	})
	elapsed := time.Since(start)
	if err != nil {
		fmt.Fprintf(stderr, "circleci-smarter plan: analysis failed: %v\n", err)
		return planAnalyzeResult{MapProblem: fmt.Sprintf("analysis failed: %v", err), Warnings: warns, Elapsed: elapsed}
	}
	var summaries map[string]string
	if rep != nil {
		summaries = analyze.JobSummaries(rep)
	}
	return planAnalyzeResult{Map: m, Summaries: summaries, Warnings: warns, Elapsed: elapsed}
}

type aiReviewOutcome struct {
	Added    map[string]string
	Shadow   []report.AIReviewJob
	Report   *report.AIReview
	Warnings []string
}

func planAIReview(ctx context.Context, reviewMode string, cfgData []byte, workflow string, base, head, branch, tag string, repo diff.Git, rep *report.Plan, summaries map[string]string, reviewer *ai.Reviewer) aiReviewOutcome {
	out := aiReviewOutcome{}
	if reviewMode == "off" || reviewer == nil || !ai.APIKeyPresent() {
		return out
	}
	out.Report = &report.AIReview{Mode: reviewMode}
	if rep.Full {
		return out
	}
	skipped := 0
	for _, j := range rep.Jobs {
		if !j.Run {
			skipped++
		}
	}
	if skipped == 0 {
		return out
	}
	cfg, err := config.Parse(cfgData)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("ai review: %v", err))
		return out
	}
	wf, err := cfg.Workflow(workflow)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("ai review: %v", err))
		return out
	}
	changes, err := repo.Changes(ctx, base, head)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("ai review: cannot read changes: %v", err))
		return out
	}
	var paths []string
	for _, c := range changes {
		if !ai.DenyContent(c.Path) {
			paths = append(paths, c.Path)
		}
	}
	patches, err := repo.Patches(ctx, base, head, changes)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("ai review: diff: %v", err))
	}
	var hunk strings.Builder
	for _, p := range patches {
		if ai.DenyContent(p.Path) {
			continue
		}
		fmt.Fprintf(&hunk, "### %s\n%s\n\n", p.Path, p.Patch)
	}

	allowed := map[string]struct{}{}
	for _, e := range wf.Entries {
		allowed[e.Name] = struct{}{}
	}
	var skippedJobs, selected []ai.ReviewJob
	for _, j := range rep.Jobs {
		if j.Run {
			selected = append(selected, ai.ReviewJob{
				Name:    j.Name,
				Summary: stepSummary(cfg, wf, j.Name),
				Inputs:  summaries[j.Name],
			})
			continue
		}
		if _, ok := allowed[j.Name]; !ok {
			continue
		}
		skippedJobs = append(skippedJobs, ai.ReviewJob{
			Name:    j.Name,
			Summary: stepSummary(cfg, wf, j.Name),
			Inputs:  summaries[j.Name],
		})
	}
	res, err := reviewer.Review(ctx, ai.ReviewRequest{
		ChangedFiles: paths,
		DiffHunks:    hunk.String(),
		Skipped:      skippedJobs,
		Selected:     selected,
	}, allowed)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("ai review: %v", err))
		out.Report.Warnings = out.Warnings
		return out
	}
	out.Warnings = append(out.Warnings, res.Warnings...)
	for _, a := range res.Add {
		job := report.AIReviewJob{Name: a.Name, Why: a.Why}
		switch reviewMode {
		case "on":
			if out.Added == nil {
				out.Added = map[string]string{}
			}
			out.Added[a.Name] = a.Why
			out.Report.Added = append(out.Report.Added, job)
		case "shadow":
			out.Shadow = append(out.Shadow, job)
			out.Report.Shadow = append(out.Report.Shadow, job)
		}
	}
	out.Report.Warnings = res.Warnings
	return out
}

func stepSummary(cfg *config.Config, wf *config.Workflow, name string) string {
	for _, e := range wf.Entries {
		if e.Name != name {
			continue
		}
		steps := cfg.CollectRunSteps(e.Ref, e.EntryArgs())
		var bits []string
		for i, st := range steps {
			if i >= 3 {
				bits = append(bits, "...")
				break
			}
			cmd := st.Command
			if len(cmd) > 80 {
				cmd = cmd[:77] + "..."
			}
			bits = append(bits, cmd)
		}
		return strings.Join(bits, "; ")
	}
	return ""
}

func absConfig(repo, cfg string) string {
	if filepath.IsAbs(cfg) {
		return cfg
	}
	return filepath.Join(repo, cfg)
}

func planWorkflowName(cfgData []byte, want string) (string, error) {
	cfg, err := config.Parse(cfgData)
	if err != nil {
		return "", err
	}
	return planner.ChooseWorkflow(cfg, want)
}
