// Package policy evaluates the guardrail bundle (policies/) against a config.
//
// It runs the bundle with circle-policy-agent, the library behind CircleCI's
// config policies, and evaluates it the way the policy service does, so the
// planner's pre-flight check (ADR 6) gives the decision the service would.
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/CircleCI-Public/circle-policy-agent/cpa"
	"go.yaml.in/yaml/v3"

	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
)

// helperPrefix starts the names of the modules that circle-policy-agent adds
// for the data.circleci helper library. They are not part of the bundle.
const helperPrefix = "circleci/"

// requireJobCode is the repair code of a violation the planner can fix by
// selecting a job: [smarter:require-job=NAME].
var requireJobCode = regexp.MustCompile(`\[smarter:require-job=([^\]]*)\]`)

// Bundle is a parsed guardrail bundle and the metadata it is evaluated with.
// It implements guard.Checker, and is safe for concurrent use.
type Bundle struct {
	policy *cpa.Policy
	meta   map[string]any
	// owners maps a rule name to the policy that declares it.
	owners map[string]string
}

var _ guard.Checker = (*Bundle)(nil)

// Load parses the Rego files under bundleDir, recursively, and ignores the
// *_test.rego files. A path to one .rego file loads that file alone.
//
// meta becomes data.meta. A nil meta means MetaFromEnv(os.Getenv). Either way
// Load adds data.meta.smarter.continuation = true, because the bundle checks
// continuation configs, and the rule that bans `setup: true` runs only when it
// is set (see policies/guardrails.rego).
func Load(bundleDir string, meta map[string]any) (*Bundle, error) {
	files, err := readBundle(bundleDir)
	if err != nil {
		return nil, err
	}
	pol, err := cpa.ParseBundle(files)
	if err != nil {
		return nil, fmt.Errorf("policy bundle %s: %w", bundleDir, err)
	}
	if meta == nil {
		meta = MetaFromEnv(os.Getenv)
	}
	meta, err = continuationMeta(meta)
	if err != nil {
		return nil, fmt.Errorf("policy meta: %w", err)
	}
	return &Bundle{policy: pol, meta: meta, owners: ruleOwners(pol)}, nil
}

// readBundle reads the policy files the way `circleci policy push` does (every
// .rego file under root), except for the test files.
func readBundle(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".rego" || strings.HasSuffix(path, "_test.rego") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(data)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read policy bundle: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("policy bundle %s: no .rego policy files found", root)
	}
	return files, nil
}

// continuationMeta returns a copy of meta, normalized to JSON types, with
// smarter.continuation set to true.
func continuationMeta(meta map[string]any) (map[string]any, error) {
	data, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	smarter, _ := out["smarter"].(map[string]any)
	if smarter == nil {
		smarter = map[string]any{}
	}
	smarter["continuation"] = true
	out["smarter"] = smarter
	return out, nil
}

// ruleOwners maps each rule name to the policy_name of the module that
// declares it (the first by name, if several do).
func ruleOwners(pol *cpa.Policy) map[string]string {
	owners := map[string]string{}
	for name, mod := range pol.Modules() {
		if strings.HasPrefix(name, helperPrefix) {
			continue
		}
		for _, rule := range mod.Rules {
			rn := rule.Head.Name.String()
			if owner, ok := owners[rn]; !ok || name < owner {
				owners[rn] = name
			}
		}
	}
	return owners
}

// Check evaluates a continuation config, as YAML, against the bundle.
//
// The result's status is PASS, SOFT_FAIL, HARD_FAIL or ERROR, as the policy
// service reports it. ERROR means the bundle could not be evaluated (the
// result then has one hard violation with the reason); it is not an error of
// Check. Check returns an error when the config is not a YAML mapping or when
// ctx ends.
func (b *Bundle) Check(ctx context.Context, config []byte) (guard.Result, error) {
	if err := ctx.Err(); err != nil {
		return guard.Result{}, err
	}
	var input any
	if err := yaml.Unmarshal(config, &input); err != nil {
		return guard.Result{}, fmt.Errorf("parse config: %w", err)
	}
	switch input.(type) {
	case map[string]any, map[any]any:
	default:
		return guard.Result{}, errors.New("parse config: the config is not a YAML mapping")
	}

	decision, err := b.policy.Decide(ctx, input, cpa.Meta(b.meta))
	if err != nil {
		return guard.Result{}, fmt.Errorf("evaluate policies: %w", err)
	}
	return b.result(decision), nil
}

func (b *Bundle) result(d *cpa.Decision) guard.Result {
	res := guard.Result{Status: status(d.Status)}
	for _, v := range d.HardFailures {
		res.Violations = append(res.Violations, b.violation(v, true))
	}
	for _, v := range d.SoftFailures {
		res.Violations = append(res.Violations, b.violation(v, false))
	}
	if d.Status == cpa.StatusError {
		res.Violations = append(res.Violations, guard.Violation{
			Rule:    "error",
			Message: d.Reason,
			Hard:    true,
		})
	}
	return res
}

func (b *Bundle) violation(v cpa.Violation, hard bool) guard.Violation {
	return guard.Violation{
		Policy:      b.owners[v.Rule],
		Rule:        v.Rule,
		Message:     v.Reason,
		Hard:        hard,
		RequireJobs: requireJobs(v.Reason),
	}
}

func status(s cpa.Status) string {
	switch s {
	case cpa.StatusPass:
		return guard.Pass
	case cpa.StatusSoftFail:
		return guard.SoftFail
	case cpa.StatusHardFail:
		return guard.HardFail
	default:
		return guard.Error
	}
}

// requireJobs returns the job names in the [smarter:require-job=NAME] codes of
// a message, in order and without duplicates.
func requireJobs(message string) []string {
	var jobs []string
	seen := map[string]bool{}
	for _, m := range requireJobCode.FindAllStringSubmatch(message, -1) {
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		jobs = append(jobs, name)
	}
	return jobs
}
