package planner

import (
	"go.yaml.in/yaml/v3"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
)

// gateJob is the definition of the gate (step 6): a no-op job. It has no
// executor and no steps, so it costs no credits, and its value is in what its
// workflow entry requires. It is CircleCI's gatekeeper pattern for one required
// status check.
func gateJob() *yaml.Node {
	return config.Map("type", "no-op")
}

// addGate adds the gate job and its entry, which requires the given jobs.
func (p *planner) addGate(cfg *config.Config, wf *config.Workflow, requires []string) error {
	if err := cfg.AddJob(p.o.GateJob, gateJob()); err != nil {
		return err
	}
	return wf.AppendEntry(p.o.GateJob, requires)
}
