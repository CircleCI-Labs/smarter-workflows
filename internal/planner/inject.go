package planner

import (
	"fmt"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/report"
	"github.com/CircleCI-Labs/smarter-workflows/internal/tracescript"
)

// gateJob is the definition of the gate (step 6): a no-op job. It has no
// executor and no steps, so it costs no credits, and its value is in what its
// workflow entry requires. It is CircleCI's gatekeeper pattern for one required
// status check.
func gateJob() *yaml.Node {
	return config.Map("type", "no-op")
}

func resetStep() *yaml.Node {
	return config.RunStep("smarter: reset atime", "/bin/sh", "", tracescript.Reset)
}

func collectSteps() []*yaml.Node {
	return []*yaml.Node{
		config.RunStep("smarter: collect trace", "/bin/sh", "always", tracescript.Script),
		config.Map("persist_to_workspace", config.Map("root", TraceDir, "paths", config.Strs("traces"))),
	}
}

var safeShellWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(s string) string {
	if safeShellWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// impactMapJob is the job that turns the traces of a full run into the impact
// map and caches it (step 8.3). Everything in it is a literal: no pipeline
// parameters.
func (p *planner) impactMapJob() *yaml.Node {
	return config.Map(
		"docker", config.Seq(config.Map("image", p.o.MapImage)),
		"resource_class", "small",
		"steps", config.Seq(
			config.RunStep("smarter: write version", "", "",
				"printf '%s\\n' "+shellQuote(p.o.Version)+" > .smarter-version\n"),
			config.Map("restore_cache", config.Map(
				"key", `smarter-bin-v1-{{ checksum ".smarter-version" }}-{{ arch }}`)),
			config.Map("attach_workspace", config.Map("at", TraceDir)),
			config.RunStep("smarter: build impact map", "", "",
				shellQuote(p.o.BinPath)+" map build --traces "+TraceDir+"/traces --revision \"$CIRCLE_SHA1\" --out "+TraceDir+"/impact-map.json\n"),
			config.RunStep("smarter: write cache key", "", "",
				"printf '%s\\n' \"$CIRCLE_SHA1\" > .smarter-key\n"),
			config.Map("save_cache", config.Map(
				"key", p.o.MapKeyPrefix+`-{{ checksum ".smarter-key" }}`,
				"paths", config.Strs(TraceDir+"/impact-map.json"))),
			config.Map("store_artifacts", config.Map("path", TraceDir+"/impact-map.json")),
		),
	)
}

// addGate adds the gate job and its entry, which requires the given jobs.
func (p *planner) addGate(cfg *config.Config, wf *config.Workflow, requires []string) error {
	if err := cfg.AddJob(p.o.GateJob, gateJob()); err != nil {
		return err
	}
	return wf.AppendEntry(p.o.GateJob, requires)
}

// traceOn says whether this plan gets the trace steps (step 8).
func (p *planner) traceOn(full bool) bool {
	switch p.o.Trace {
	case TraceOn:
		return true
	case TraceAuto:
		return full && p.o.Tag == "" && p.o.Branch != "" && p.o.Branch == p.o.DefaultBranch
	}
	return false
}

// injectTrace is step 8: trace steps on the entries that can be traced, and the
// job that builds the map from their traces. It returns nil when nothing can be
// traced, with the reason in warnings. An entry that cannot take the steps (its
// pre-steps or post-steps is not a list) is left as it is, with a warning, and
// does not stop the others: it has no trace, and so no map entry, and later
// plans always run it.
func (p *planner) injectTrace(cfg *config.Config, wf *config.Workflow, an *analysis) (*report.Trace, []string, error) {
	if v := cfg.Version(); v != "2.1" {
		// pre-steps and post-steps exist from version 2.1 on.
		return nil, []string{fmt.Sprintf("trace: the config is version %q, and the trace steps need version 2.1", v)}, nil
	}
	type target struct {
		e     *config.Entry
		local bool
	}
	var targets []target
	var requires, notes []string
	for _, e := range wf.Entries {
		if e.Approval || e.Matrix {
			// An approval is not a job. A matrix entry always runs, so its trace
			// would never be looked up.
			continue
		}
		local := cfg.HasJob(e.Ref)
		switch {
		case local && cfg.JobPlatform(e.Ref) == config.PlatformLinux:
		case !local && p.o.TraceOrbJobs:
		default:
			continue
		}
		if err := e.CheckSteps(); err != nil {
			notes = append(notes, fmt.Sprintf("trace: %s is not traced, so later plans always run it: %v", e.Name, err))
			continue
		}
		targets = append(targets, target{e, local})
		// The map job waits for jobs that are certain to run, and not for ones
		// behind an approval, which would never let it start.
		if i, ok := an.byName[e.Name]; ok && an.will[i] == triYes && !an.afterApproval[i] {
			requires = append(requires, e.RequireName())
		}
	}
	if len(targets) == 0 {
		return nil, append(notes, "trace: no job of the workflow can be traced (only docker and Linux machine jobs defined in the config can; orb jobs need --trace-orb-jobs)"), nil
	}
	if len(requires) == 0 {
		return nil, append(notes, "trace: none of the traceable jobs is certain to run on this branch, so there is nothing for the map job to wait for"), nil
	}

	tr := &report.Trace{MapJob: ImpactMapJob, Requires: requires}
	resetDone := map[string]bool{}
	for _, t := range targets {
		tr.Jobs = append(tr.Jobs, t.e.Name)
		if p.o.TraceReset {
			if t.local {
				if !resetDone[t.e.Ref] {
					resetDone[t.e.Ref] = true
					if _, err := cfg.InsertAfterCheckout(t.e.Ref, resetStep()); err != nil {
						return nil, nil, err
					}
				}
			} else if err := wf.AddPreSteps(t.e, config.Str("checkout"), resetStep()); err != nil {
				return nil, nil, err
			}
		}
		if err := wf.AddPostSteps(t.e, collectSteps()...); err != nil {
			return nil, nil, err
		}
	}
	if err := cfg.AddJob(ImpactMapJob, p.impactMapJob()); err != nil {
		return nil, nil, err
	}
	if err := wf.AppendEntry(ImpactMapJob, requires); err != nil {
		return nil, nil, fmt.Errorf("adding the map job: %w", err)
	}
	return tr, notes, nil
}
