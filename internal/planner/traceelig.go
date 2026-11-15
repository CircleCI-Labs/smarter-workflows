package planner

import (
	"fmt"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
)

// traceSkipReason explains why a workflow entry does not get the trace steps.
type traceSkipReason string

const (
	traceReasonNone        traceSkipReason = ""
	traceReasonNoSteps     traceSkipReason = "no runnable steps"
	traceReasonJobGroup    traceSkipReason = "job group"
	traceReasonMatrix      traceSkipReason = "matrix job"
	traceReasonOrb         traceSkipReason = "orb job without --trace-orb-jobs"
	traceReasonNonLinux    traceSkipReason = "non-Linux executor"
	traceReasonUnknownExec traceSkipReason = "unknown executor"
	traceReasonNoCheckout  traceSkipReason = "no checkout step"
	traceReasonBadSteps    traceSkipReason = "invalid pre-steps or post-steps"
)

func matchTraceJob(globs []string, name string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(normGlob(g), name); ok {
			return true
		}
	}
	return false
}

func (p *planner) jobTracePlatform(cfg *config.Config, job string) config.Platform {
	plat := cfg.JobPlatform(job)
	if plat == config.PlatformLinux || plat == config.PlatformOther {
		return plat
	}
	if matchTraceJob(p.o.TraceJobs, job) {
		return config.PlatformLinux
	}
	return plat
}

// entryTraceEligibility decides whether an entry can be traced. For bad
// pre/post steps the entry is still returned so injectTrace can warn.
func (p *planner) entryTraceEligibility(cfg *config.Config, e *config.Entry) (traceSkipReason, *config.Entry) {
	if !e.Kind.HasSteps() || e.Kind == config.KindJobGroup {
		if e.Kind == config.KindJobGroup {
			return traceReasonJobGroup, nil
		}
		return traceReasonNoSteps, nil
	}
	if e.Matrix {
		return traceReasonMatrix, nil
	}
	local := cfg.HasJob(e.Ref)
	if !local {
		if !p.o.TraceOrbJobs {
			return traceReasonOrb, nil
		}
	} else {
		switch p.jobTracePlatform(cfg, e.Ref) {
		case config.PlatformOther:
			return traceReasonNonLinux, nil
		case config.PlatformUnknown:
			return traceReasonUnknownExec, nil
		}
		if !cfg.JobHasCheckout(e.Ref) {
			return traceReasonNoCheckout, nil
		}
	}
	if err := e.CheckSteps(); err != nil {
		return traceReasonBadSteps, e
	}
	return traceReasonNone, e
}

func collectEnv(workdir string) map[string]string {
	if workdir == "" {
		return nil
	}
	return map[string]string{"CIRCLE_WORKING_DIRECTORY": workdir}
}

func skipReasonText(r traceSkipReason, err error) string {
	if r == traceReasonBadSteps && err != nil {
		return fmt.Sprintf("%s: %v", r, err)
	}
	return string(r)
}
