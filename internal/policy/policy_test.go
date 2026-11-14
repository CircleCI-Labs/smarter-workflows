package policy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
)

// bundleDir is the real guardrail bundle.
const bundleDir = "../../policies"

const policyName = "smarter_guardrails"

func loadReal(t *testing.T) *Bundle {
	t.Helper()
	b, err := Load(bundleDir, map[string]any{})
	if err != nil {
		t.Fatalf("Load(%s): %v", bundleDir, err)
	}
	return b
}

func checkFile(t *testing.T, b *Bundle, file string) guard.Result {
	t.Helper()
	config, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.Check(context.Background(), config)
	if err != nil {
		t.Fatalf("Check(%s): %v", file, err)
	}
	return res
}

// writeBundle writes files (name to content) into a new directory.
func writeBundle(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func rulesOf(res guard.Result) []string {
	var rules []string
	for _, v := range res.Violations {
		rules = append(rules, v.Rule)
	}
	sort.Strings(rules)
	return rules
}

func TestCheckPassingConfigs(t *testing.T) {
	b := loadReal(t)
	for _, file := range []string{"good.yml", "good_release_on_tags.yml"} {
		t.Run(file, func(t *testing.T) {
			res := checkFile(t, b, file)
			if res.Status != guard.Pass || len(res.Violations) != 0 {
				t.Fatalf("got %s with %+v, want PASS", res.Status, res.Violations)
			}
		})
	}
}

func TestCheckFailingConfigs(t *testing.T) {
	b := loadReal(t)
	tests := []struct {
		file string
		// rules is the rule of every violation, sorted.
		rules []string
		// messages must each appear in some violation's message.
		messages []string
	}{
		{
			file:  "required_job_missing.yml",
			rules: []string{"required_jobs", "required_jobs"},
			messages: []string{
				`workflow "ci" is missing the required job "security-scan"`,
				`workflow "nightly" is missing the required job "security-scan"`,
			},
		},
		{
			file:     "context_wrong_job.yml",
			rules:    []string{"restricted_contexts"},
			messages: []string{`job "publish" uses the restricted context "deploy"; allowed jobs: deploy`},
		},
		{
			file:     "context_any_branch.yml",
			rules:    []string{"restricted_contexts"},
			messages: []string{`job "deploy" uses the restricted context "deploy" and must be limited to the branches [main]`},
		},
		{
			file:     "context_no_approval.yml",
			rules:    []string{"restricted_contexts"},
			messages: []string{`job "deploy" uses the restricted context "deploy" and must run downstream of an approval job`},
		},
		{
			file:  "resource_class_too_big.yml",
			rules: []string{"resource_classes", "resource_classes"},
			messages: []string{
				`job "build" uses the resource_class "2xlarge"`,
				`executor "big" uses the resource_class "xlarge"`,
			},
		},
		{
			file:     "resource_class_compiled.yml",
			rules:    []string{"resource_classes"},
			messages: []string{`job "build" uses the resource_class "2xlarge"`},
		},
		{
			file:  "orb_not_pinned.yml",
			rules: []string{"registry_orbs_pinned", "registry_orbs_pinned"},
			messages: []string{
				`orb "node" (circleci/node@5) is not pinned to an exact x.y.z version`,
				`orb "tools" (example/tools@volatile) is not pinned to an exact x.y.z version`,
			},
		},
		{
			file:  "url_orb_not_pinned.yml",
			rules: []string{"url_orbs_pinned", "url_orbs_pinned"},
			messages: []string{
				`URL orb "shared" is not pinned to a 40-character commit SHA`,
				`URL orb "private" is not pinned to a 40-character commit SHA`,
			},
		},
		{
			file:     "continuation_declares_setup.yml",
			rules:    []string{"no_setup_in_continuation"},
			messages: []string{"a continuation config must not declare setup: true"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			res := checkFile(t, b, tt.file)
			if res.Status != guard.HardFail {
				t.Fatalf("status = %s, want HARD_FAIL; violations: %+v", res.Status, res.Violations)
			}
			if got := rulesOf(res); !slices.Equal(got, tt.rules) {
				t.Fatalf("rules = %v, want %v; violations: %+v", got, tt.rules, res.Violations)
			}
			for _, v := range res.Violations {
				if !v.Hard {
					t.Errorf("violation %q is not hard", v.Message)
				}
				if v.Policy != policyName {
					t.Errorf("violation %q has policy %q, want %q", v.Rule, v.Policy, policyName)
				}
			}
			for _, want := range tt.messages {
				if !anyMessageContains(res, want) {
					t.Errorf("no violation message contains %q; got %+v", want, res.Violations)
				}
			}
		})
	}
}

func anyMessageContains(res guard.Result, fragment string) bool {
	for _, v := range res.Violations {
		if strings.Contains(v.Message, fragment) {
			return true
		}
	}
	return false
}

func TestMissingRequiredJobCarriesTheRepairCode(t *testing.T) {
	res := checkFile(t, loadReal(t), "required_job_missing.yml")
	if len(res.Violations) != 2 {
		t.Fatalf("got %d violations, want 2: %+v", len(res.Violations), res.Violations)
	}
	for _, v := range res.Violations {
		if want := []string{"security-scan"}; !reflect.DeepEqual(v.RequireJobs, want) {
			t.Errorf("RequireJobs of %q = %v, want %v", v.Message, v.RequireJobs, want)
		}
		if !strings.HasSuffix(v.Message, "[smarter:require-job=security-scan]") {
			t.Errorf("message %q does not end with the repair code", v.Message)
		}
	}
}

func TestOtherViolationsHaveNoRepairCode(t *testing.T) {
	res := checkFile(t, loadReal(t), "resource_class_too_big.yml")
	for _, v := range res.Violations {
		if len(v.RequireJobs) != 0 {
			t.Errorf("RequireJobs of %q = %v, want none", v.Message, v.RequireJobs)
		}
	}
}

func TestURLOrbMessagesDoNotRepeatTheURL(t *testing.T) {
	res := checkFile(t, loadReal(t), "url_orb_not_pinned.yml")
	for _, v := range res.Violations {
		for _, secret := range []string{"example-password", "example-token", "example.com"} {
			if strings.Contains(v.Message, secret) {
				t.Errorf("message %q repeats %q from the URL", v.Message, secret)
			}
		}
	}
}

// The test bundles below are small policies of their own.
const (
	headerRego = "package org\n\nimport future.keywords\n\n"

	softAndHardRego = headerRego + `
policy_name["status_probe"]

enable_rule["soft_rule"]
enable_hard["hard_rule"]
enable_hard["quiet_rule"]

soft_rule[msg] { msg := "soft problem" }
hard_rule[msg] { msg := "hard problem [smarter:require-job=lint] [smarter:require-job=unit] [smarter:require-job=lint]" }
quiet_rule[msg] { false; msg := "never" }
`

	softOnlyRego = headerRego + `
policy_name["soft_only"]

enable_rule["soft_rule"]

soft_rule = "soft problem [smarter:require-job=docs]"
`

	passRego = headerRego + `
policy_name["pass_only"]

enable_hard["hard_rule"]

hard_rule[msg] { input.nonexistent; msg := "never" }
`

	errorRego = headerRego + `
policy_name["error_probe"]

enable_hard["conflict"]

conflict = "one"
conflict = "two" { true }
`

	metaProbeRego = headerRego + `
policy_name["meta_probe"]

enable_rule["meta"]

meta = msg {
	msg := sprintf("branch=%s tag=%s build=%v continuation=%v", [data.meta.vcs.branch, data.meta.vcs.release_tag, data.meta.build_number, data.meta.smarter.continuation])
}
`
)

func checkYAML(t *testing.T, b *Bundle, config string) guard.Result {
	t.Helper()
	res, err := b.Check(context.Background(), []byte(config))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return res
}

func TestStatusAndHardFlag(t *testing.T) {
	t.Run("hard and soft violations", func(t *testing.T) {
		b, err := Load(writeBundle(t, map[string]string{"p.rego": softAndHardRego}), map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		res := checkYAML(t, b, "version: 2.1\n")
		if res.Status != guard.HardFail {
			t.Fatalf("status = %s, want HARD_FAIL", res.Status)
		}
		want := []guard.Violation{
			{Policy: "status_probe", Rule: "hard_rule", Hard: true, RequireJobs: []string{"lint", "unit"},
				Message: "hard problem [smarter:require-job=lint] [smarter:require-job=unit] [smarter:require-job=lint]"},
			{Policy: "status_probe", Rule: "soft_rule", Hard: false, Message: "soft problem"},
		}
		if !reflect.DeepEqual(res.Violations, want) {
			t.Fatalf("violations = %+v\nwant %+v", res.Violations, want)
		}
	})

	t.Run("soft only", func(t *testing.T) {
		b, err := Load(writeBundle(t, map[string]string{"p.rego": softOnlyRego}), map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		res := checkYAML(t, b, "version: 2.1\n")
		want := guard.Result{Status: guard.SoftFail, Violations: []guard.Violation{
			{Policy: "soft_only", Rule: "soft_rule", Hard: false, RequireJobs: []string{"docs"},
				Message: "soft problem [smarter:require-job=docs]"},
		}}
		if !reflect.DeepEqual(res, want) {
			t.Fatalf("result = %+v\nwant %+v", res, want)
		}
	})

	t.Run("pass", func(t *testing.T) {
		b, err := Load(writeBundle(t, map[string]string{"p.rego": passRego}), map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		res := checkYAML(t, b, "version: 2.1\n")
		if res.Status != guard.Pass || len(res.Violations) != 0 {
			t.Fatalf("result = %+v, want PASS", res)
		}
	})

	t.Run("error", func(t *testing.T) {
		b, err := Load(writeBundle(t, map[string]string{"p.rego": errorRego}), map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		res := checkYAML(t, b, "version: 2.1\n")
		if res.Status != guard.Error {
			t.Fatalf("status = %s, want ERROR", res.Status)
		}
		if len(res.Violations) != 1 || !res.Violations[0].Hard || res.Violations[0].Message == "" {
			t.Fatalf("want one hard violation with the reason, got %+v", res.Violations)
		}
	})
}

func TestPolicyOfEachRuleComesFromItsOwnFile(t *testing.T) {
	dir := writeBundle(t, map[string]string{
		"a.rego": softOnlyRego,
		"b.rego": strings.Replace(softAndHardRego, "soft_rule", "other_soft_rule", -1),
	})
	b, err := Load(dir, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	res := checkYAML(t, b, "version: 2.1\n")
	got := map[string]string{}
	for _, v := range res.Violations {
		got[v.Rule] = v.Policy
	}
	want := map[string]string{"soft_rule": "soft_only", "other_soft_rule": "status_probe", "hard_rule": "status_probe"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("policies = %v, want %v", got, want)
	}
}

func TestLoadIgnoresTestFiles(t *testing.T) {
	dir := writeBundle(t, map[string]string{
		"rules.rego":          passRego,
		"rules_test.rego":     "this is not rego at all",
		"sub/more.rego":       softOnlyRego,
		"sub/more_test.rego":  passRego, // would be a duplicate policy name
		"sub/notes.txt":       "ignored",
		"sub/deeper/x.rego.k": "ignored",
	})
	b, err := Load(dir, map[string]any{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The soft_only policy in the subdirectory is part of the bundle.
	res := checkYAML(t, b, "version: 2.1\n")
	if res.Status != guard.SoftFail {
		t.Fatalf("status = %s, want SOFT_FAIL from sub/more.rego", res.Status)
	}
}

func TestLoadASingleFile(t *testing.T) {
	dir := writeBundle(t, map[string]string{"one.rego": softOnlyRego, "two.rego": softAndHardRego})
	b, err := Load(filepath.Join(dir, "one.rego"), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if res := checkYAML(t, b, "version: 2.1\n"); res.Status != guard.SoftFail || len(res.Violations) != 1 {
		t.Fatalf("result = %+v, want the one soft violation of one.rego", res)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name  string
		dir   func(t *testing.T) string
		wants string
	}{
		{"missing directory", func(t *testing.T) string { return filepath.Join(t.TempDir(), "nope") }, "read policy bundle"},
		{"no rego files", func(t *testing.T) string {
			return writeBundle(t, map[string]string{"x_test.rego": passRego, "readme.md": "x"})
		}, "no .rego policy files"},
		{"invalid rego", func(t *testing.T) string {
			return writeBundle(t, map[string]string{"x.rego": "package org\n\npolicy_name[\"x\"]\n\nrule {{"})
		}, "failed to parse"},
		{"wrong package", func(t *testing.T) string {
			return writeBundle(t, map[string]string{"x.rego": "package other\n\npolicy_name[\"x\"]\n"})
		}, "invalid package name"},
		{"no policy_name", func(t *testing.T) string {
			return writeBundle(t, map[string]string{"x.rego": "package org\n\nenable_rule[\"x\"]\n"})
		}, "policy_name"},
		{"duplicate policy_name", func(t *testing.T) string {
			return writeBundle(t, map[string]string{"x.rego": passRego, "y.rego": passRego})
		}, "declared 2 times"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.dir(t), map[string]any{})
			if err == nil {
				t.Fatal("Load succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.wants) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wants)
			}
		})
	}
}

func TestCheckRejectsABadConfig(t *testing.T) {
	b := loadReal(t)
	for name, config := range map[string]string{
		"empty":        "",
		"comment only": "# nothing\n",
		"a list":       "- a\n- b\n",
		"a scalar":     "hello\n",
		"invalid yaml": "a: [unclosed\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := b.Check(context.Background(), []byte(config)); err == nil {
				t.Fatal("Check succeeded, want an error")
			}
		})
	}
}

func TestCheckStopsWhenTheContextEnds(t *testing.T) {
	b := loadReal(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := b.Check(ctx, []byte("version: 2.1\n"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestCheckAcceptsYAMLFeatures(t *testing.T) {
	// Anchors, merge keys and non-string keys must not break the evaluation.
	config := `
version: 2.1
x-docker: &docker
  docker:
    - image: cimg/base:stable
jobs:
  security-scan:
    <<: *docker
    steps: [checkout]
  numbers:
    <<: *docker
    steps: [checkout]
    1: one
workflows:
  ci:
    jobs: [security-scan, numbers]
`
	res := checkYAML(t, loadReal(t), config)
	if res.Status != guard.Pass {
		t.Fatalf("result = %+v, want PASS", res)
	}
}

// mustReplace replaces one occurrence of old in s, and fails if there is none.
func mustReplace(t *testing.T, s, old, replacement string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("policies/guardrails.rego no longer contains %q: update this test", old)
	}
	return strings.Replace(s, old, replacement, 1)
}

func TestARuleMovesFromHardToSoft(t *testing.T) {
	// The way a project does it: move the name from hard_rules to soft_rules,
	// on a copy of the bundle.
	src, err := os.ReadFile(filepath.Join(bundleDir, "guardrails.rego"))
	if err != nil {
		t.Fatal(err)
	}
	soft := string(src)
	soft = mustReplace(t, soft, "soft_rules := set()", `soft_rules := {"required_jobs"}`)
	soft = mustReplace(t, soft, "\t\"required_jobs\",\n\t\"restricted_contexts\",", "\t\"restricted_contexts\",")
	b, err := Load(writeBundle(t, map[string]string{"guardrails.rego": soft}), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	res := checkFile(t, b, "required_job_missing.yml")
	if res.Status != guard.SoftFail {
		t.Fatalf("status = %s, want SOFT_FAIL", res.Status)
	}
	for _, v := range res.Violations {
		if v.Hard {
			t.Errorf("violation %q is hard", v.Message)
		}
		if !reflect.DeepEqual(v.RequireJobs, []string{"security-scan"}) {
			t.Errorf("RequireJobs = %v, want [security-scan]", v.RequireJobs)
		}
	}
	// The rules that stay in hard_rules still block.
	if res := checkFile(t, b, "context_wrong_job.yml"); res.Status != guard.HardFail {
		t.Errorf("status = %s, want HARD_FAIL", res.Status)
	}
}

func TestMetaReachesThePolicies(t *testing.T) {
	dir := writeBundle(t, map[string]string{"p.rego": metaProbeRego})
	messageOf := func(t *testing.T, b *Bundle) string {
		t.Helper()
		res := checkYAML(t, b, "version: 2.1\n")
		if len(res.Violations) != 1 {
			t.Fatalf("violations = %+v", res.Violations)
		}
		return res.Violations[0].Message
	}

	t.Run("given meta", func(t *testing.T) {
		meta := map[string]any{
			"build_number": 42,
			"vcs":          map[string]any{"branch": "feature", "release_tag": "v1"},
			"smarter":      map[string]any{"other": "kept"},
		}
		b, err := Load(dir, meta)
		if err != nil {
			t.Fatal(err)
		}
		want := "branch=feature tag=v1 build=42 continuation=true"
		if got := messageOf(t, b); got != want {
			t.Fatalf("message = %q, want %q", got, want)
		}
		// Load leaves the caller's map alone.
		if _, ok := meta["smarter"].(map[string]any)["continuation"]; ok {
			t.Fatal("Load changed the meta it was given")
		}
	})

	t.Run("meta from the environment", func(t *testing.T) {
		t.Setenv("CIRCLE_BRANCH", "from-env")
		t.Setenv("CIRCLE_TAG", "")
		t.Setenv("CIRCLE_BUILD_NUM", "7")
		b, err := Load(dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := "branch=from-env tag= build=7 continuation=true"
		if got := messageOf(t, b); got != want {
			t.Fatalf("message = %q, want %q", got, want)
		}
	})

	t.Run("meta that json cannot hold", func(t *testing.T) {
		if _, err := Load(dir, map[string]any{"bad": make(chan int)}); err == nil {
			t.Fatal("Load succeeded, want an error")
		}
	})
}

func TestLoadLeavesTheTestsOfTheRealBundleOut(t *testing.T) {
	b := loadReal(t)
	for rule := range b.owners {
		if strings.HasPrefix(rule, "test_") {
			t.Errorf("the rule %s of a test file is in the bundle", rule)
		}
	}
	if b.owners["required_jobs"] != policyName {
		t.Errorf("owner of required_jobs = %q, want %q", b.owners["required_jobs"], policyName)
	}
}

func TestCheckIsSafeForConcurrentUse(t *testing.T) {
	b := loadReal(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				res := checkFile(t, b, "required_job_missing.yml")
				if res.Status != guard.HardFail || len(res.Violations) != 2 {
					t.Errorf("result = %+v", res)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestRequireJobs(t *testing.T) {
	tests := []struct {
		message string
		want    []string
	}{
		{"nothing to repair", nil},
		{"missing [smarter:require-job=security-scan]", []string{"security-scan"}},
		{"[smarter:require-job=a][smarter:require-job=b]", []string{"a", "b"}},
		{"twice [smarter:require-job=a] and [smarter:require-job=a]", []string{"a"}},
		{"spaces [smarter:require-job= build and test ]", []string{"build and test"}},
		{"empty [smarter:require-job=]", nil},
		{"other code [smarter:skip-job=a]", nil},
		{"not a code smarter:require-job=a", nil},
		{"unterminated [smarter:require-job=a", nil},
		{"upper case [SMARTER:REQUIRE-JOB=a]", nil},
		{"slashes and dots [smarter:require-job=orb/job.v2]", []string{"orb/job.v2"}},
	}
	for _, tt := range tests {
		if got := requireJobs(tt.message); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("requireJobs(%q) = %v, want %v", tt.message, got, tt.want)
		}
	}
}
