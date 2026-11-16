package backtest

import (
	"context"
	"fmt"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

// Warning is a non-fatal backtest condition the user should notice.
type Warning struct {
	Message string
}

// CheckWarnings reports misconfigurations that skew recall or savings.
func CheckWarnings(ctx context.Context, configYAML []byte, history []CommitHistory, o Options) ([]Warning, error) {
	var warns []Warning
	cfg, err := config.Parse(configYAML)
	if err != nil {
		return nil, err
	}
	wfName, err := planner.ChooseWorkflow(cfg, o.Workflow)
	if err != nil {
		return nil, err
	}
	wf, err := cfg.Workflow(wfName)
	if err != nil {
		return nil, err
	}

	branch := o.DefaultBranch
	if len(history) > 0 && history[0].Branch != "" {
		branch = history[0].Branch
	}
	planOpts := planner.DefaultOptions()
	planOpts.Workflow = o.Workflow
	planOpts.DefaultBranch = o.DefaultBranch
	planOpts.Branch = branch
	planOpts.Parameters = o.Parameters
	planOpts.Trace = planner.TraceOff
	in := planner.Input{Config: configYAML, MapProblem: "backtest warning check"}
	res, err := planner.Run(ctx, in, planOpts)
	if err != nil {
		return nil, err
	}
	if !res.Report.WorkflowRuns {
		warns = append(warns, Warning{
			Message: fmt.Sprintf("workflow %s does not run under these parameters; every commit is counted as not run", o.Workflow),
		})
	}

	configJobs := map[string]bool{}
	for _, e := range wf.Entries {
		configJobs[e.Name] = true
	}
	var total, unmatched int
	for _, h := range history {
		for _, j := range h.Jobs {
			total++
			if !configJobs[j.Name] {
				unmatched++
			}
		}
	}
	if total > 0 && unmatched*2 > total {
		warns = append(warns, Warning{
			Message: fmt.Sprintf("more than half of history job outcomes (%d of %d) match no job in workflow %s", unmatched, total, o.Workflow),
		})
	}
	return warns, nil
}

// FormatWarnings joins warnings for stderr or report headers.
func FormatWarnings(warns []Warning) string {
	if len(warns) == 0 {
		return ""
	}
	var b strings.Builder
	for _, w := range warns {
		fmt.Fprintf(&b, "warning: %s\n", w.Message)
	}
	return b.String()
}
