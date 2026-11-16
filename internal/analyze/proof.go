package analyze

import "fmt"

// StepRecord is one understood or opaque step in the proof trace.
type StepRecord struct {
	Num        int    `json:"num"`
	Key        string `json:"key"`
	Understood bool   `json:"understood"`
	Summary    string `json:"summary,omitempty"`
	Opaque     string `json:"opaque,omitempty"`
}

// JobProof is the step-by-step static completeness proof for one job.
type JobProof struct {
	Steps              []StepRecord `json:"steps"`
	DroppedTokens      []string     `json:"dropped_tokens,omitempty"`
	IncompleteReason   string       `json:"incomplete_reason,omitempty"`
	requiresRepoInputs bool
}

func (p *JobProof) StaticComplete() bool {
	if p == nil {
		return false
	}
	return p.IncompleteReason == ""
}

func (p *JobProof) failStep(num int, msg string) {
	p.IncompleteReason = fmt.Sprintf("step %d not understood: %s", num, msg)
}

func (p *JobProof) failNoInputs() {
	p.IncompleteReason = "no inputs found"
}
