// Package guard holds the types shared by the planner (which runs the
// pre-flight and repair loop) and the policy package (which evaluates the
// Rego bundle). It keeps the two independent of each other (ADR 6).
package guard

import "context"

// Statuses, as CircleCI's policy service reports them.
const (
	Pass     = "PASS"
	SoftFail = "SOFT_FAIL"
	HardFail = "HARD_FAIL"
	Error    = "ERROR"
)

// Violation is one failed rule.
type Violation struct {
	Policy  string
	Rule    string
	Message string
	Hard    bool
	// RequireJobs lists the jobs named by [smarter:require-job=NAME] codes in
	// Message: the planner can repair the violation by selecting them.
	RequireJobs []string
}

// Result is the outcome of checking one config.
type Result struct {
	Status     string
	Violations []Violation
}

// Checker evaluates a generated config against the guardrails.
type Checker interface {
	Check(ctx context.Context, config []byte) (Result, error)
}
