package backtest

import (
	"context"
	"fmt"
	"sort"

	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

// Options tune one backtest run.
type Options struct {
	Workflow      string
	DefaultBranch string
	Mandatory     []string
	FullRunPaths  []string
	IgnorePaths   []string
}

// DefaultOptions returns settings that match this repo's dogfood planner.
func DefaultOptions() Options {
	return Options{
		Workflow:      "ci",
		DefaultBranch: "main",
		Mandatory:     []string{"security-scan"},
		FullRunPaths:  []string{planner.DefaultFullRunPaths},
	}
}

// Run evaluates every commit in history with select-mode planning.
func Run(ctx context.Context, config []byte, m *impactmap.Map, history []CommitHistory, o Options) (*Report, error) {
	if m == nil {
		return nil, fmt.Errorf("impact map is required")
	}
	r := &Report{Version: 1, Commits: len(history)}
	missByJob := map[string]int{}
	ranByJob := map[string]float64{}
	savedByJob := map[string]float64{}

	for _, h := range history {
		planOpts := planner.DefaultOptions()
		planOpts.Mode = planner.ModeSelect
		planOpts.Workflow = o.Workflow
		planOpts.DefaultBranch = o.DefaultBranch
		planOpts.Branch, planOpts.Tag = h.Branch, h.Tag
		planOpts.Base, planOpts.Head = h.Base, h.Commit
		planOpts.Mandatory = append([]string(nil), o.Mandatory...)
		planOpts.FullRunPaths = append([]string(nil), o.FullRunPaths...)
		planOpts.IgnorePaths = append([]string(nil), o.IgnorePaths...)
		planOpts.Trace = planner.TraceOff
		if err := planOpts.Validate(); err != nil {
			return nil, fmt.Errorf("commit %s: %w", shortSHA(h.Commit), err)
		}

		in := planner.Input{
			Config: config,
			Map:    m,
			Repo:   newStaticRepo(h.Changed),
		}
		res, err := planner.Run(ctx, in, planOpts)
		if err != nil {
			return nil, fmt.Errorf("commit %s: %w", shortSHA(h.Commit), err)
		}

		wouldRun := planSelection(res)
		cr := CommitResult{
			Commit: h.Commit,
			Branch: h.Branch,
			Full:   res.Report.Full,
		}

		for _, j := range h.Jobs {
			if j.DurationS > 0 {
				r.Summary.RanDurationS += j.DurationS
				ranByJob[j.Name] += j.DurationS
			}
			run, known := wouldRun[j.Name]
			if known && !run && j.DurationS > 0 {
				r.Summary.SavedDurationS += j.DurationS
				savedByJob[j.Name] += j.DurationS
			}

			if j.Flaky {
				r.Summary.FlakyFailures++
				continue
			}
			if j.Status != "failed" {
				continue
			}
			r.Summary.TotalFailures++
			if !known {
				// A job that is not in the workflow is neither caught nor a miss.
				continue
			}
			if run {
				r.Summary.CaughtFailures++
			} else {
				r.Summary.Misses++
				missByJob[j.Name]++
				cr.Misses = append(cr.Misses, j.Name)
			}
		}
		if len(cr.Misses) > 0 {
			sort.Strings(cr.Misses)
			r.CommitsWithMisses = append(r.CommitsWithMisses, cr)
		}
	}

	if r.Summary.TotalFailures > 0 {
		r.Summary.Recall = float64(r.Summary.CaughtFailures) / float64(r.Summary.TotalFailures)
	}
	if r.Summary.RanDurationS > 0 {
		r.Summary.SavingsPercent = 100 * r.Summary.SavedDurationS / r.Summary.RanDurationS
	}

	for name, n := range missByJob {
		r.PerJobMisses = append(r.PerJobMisses, JobMiss{Name: name, Misses: n})
	}
	sort.Slice(r.PerJobMisses, func(i, j int) bool {
		if r.PerJobMisses[i].Misses == r.PerJobMisses[j].Misses {
			return r.PerJobMisses[i].Name < r.PerJobMisses[j].Name
		}
		return r.PerJobMisses[i].Misses > r.PerJobMisses[j].Misses
	})

	for name, saved := range savedByJob {
		r.PerJobSavings = append(r.PerJobSavings, JobSavings{Name: name, SavedDurationS: saved})
	}
	sort.Slice(r.PerJobSavings, func(i, j int) bool {
		if r.PerJobSavings[i].SavedDurationS == r.PerJobSavings[j].SavedDurationS {
			return r.PerJobSavings[i].Name < r.PerJobSavings[j].Name
		}
		return r.PerJobSavings[i].SavedDurationS > r.PerJobSavings[j].SavedDurationS
	})

	r.normalise()
	return r, nil
}

// planSelection maps workflow job name to whether select mode would run it.
func planSelection(res *planner.Result) map[string]bool {
	out := make(map[string]bool, len(res.Report.Jobs))
	for _, j := range res.Report.Jobs {
		if j.Name == planner.DefaultGateJob || j.Name == planner.ImpactMapJob {
			continue
		}
		out[j.Name] = j.Run
	}
	return out
}

func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}
