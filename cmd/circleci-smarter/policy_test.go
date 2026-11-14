package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
)

// policyBundle is the real guardrail bundle.
const policyBundle = "../../policies"

const policyPassConfig = `
version: 2.1
jobs:
  build:
    docker:
      - image: cimg/base:stable
    steps: [checkout]
  security-scan:
    docker:
      - image: cimg/base:stable
    steps: [checkout]
workflows:
  ci:
    jobs: [build, security-scan]
`

const policyMissingScanConfig = `
version: 2.1
jobs:
  build:
    docker:
      - image: cimg/base:stable
    steps: [checkout]
workflows:
  ci:
    jobs: [build]
`

// policyWriteFiles writes the files (name to content) into a new directory.
func policyWriteFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func policyRun(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = runPolicy(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestPolicyCheckPasses(t *testing.T) {
	dir := policyWriteFiles(t, map[string]string{"config.yml": policyPassConfig})
	code, stdout, stderr := policyRun("check", "--config", filepath.Join(dir, "config.yml"), "--bundle", policyBundle)
	if code != 0 || stdout != "PASS\n" || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want 0, PASS and nothing", code, stdout, stderr)
	}
}

func TestPolicyCheckHardFailureExitsWith3(t *testing.T) {
	dir := policyWriteFiles(t, map[string]string{"config.yml": policyMissingScanConfig})
	code, stdout, stderr := policyRun("check", "--config", filepath.Join(dir, "config.yml"), "--bundle", policyBundle)
	if code != 3 {
		t.Fatalf("code = %d, want 3 (stderr: %q)", code, stderr)
	}
	want := "HARD_FAIL\n" +
		"  hard  required_jobs: workflow \"ci\" is missing the required job \"security-scan\" [smarter:require-job=security-scan]\n"
	if stdout != want {
		t.Fatalf("stdout = %q\nwant %q", stdout, want)
	}
}

func TestPolicyCheckJSON(t *testing.T) {
	dir := policyWriteFiles(t, map[string]string{"pass.yml": policyPassConfig, "fail.yml": policyMissingScanConfig})

	t.Run("hard failure", func(t *testing.T) {
		code, stdout, _ := policyRun("check", "--config", filepath.Join(dir, "fail.yml"), "--bundle", policyBundle, "--format", "json")
		if code != 3 {
			t.Fatalf("code = %d, want 3", code)
		}
		var got struct {
			Status     string `json:"status"`
			Violations []struct {
				Policy      string   `json:"policy"`
				Rule        string   `json:"rule"`
				Message     string   `json:"message"`
				Hard        bool     `json:"hard"`
				RequireJobs []string `json:"require_jobs"`
			} `json:"violations"`
		}
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
		}
		if got.Status != "HARD_FAIL" || len(got.Violations) != 1 {
			t.Fatalf("got %+v", got)
		}
		v := got.Violations[0]
		if v.Policy != "smarter_guardrails" || v.Rule != "required_jobs" || !v.Hard ||
			len(v.RequireJobs) != 1 || v.RequireJobs[0] != "security-scan" ||
			!strings.Contains(v.Message, `missing the required job "security-scan"`) {
			t.Fatalf("violation = %+v", v)
		}
	})

	t.Run("pass has an empty list, not null", func(t *testing.T) {
		code, stdout, _ := policyRun("check", "--config", filepath.Join(dir, "pass.yml"), "--bundle", policyBundle, "--format", "json")
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		if !strings.Contains(stdout, `"violations": []`) || !strings.Contains(stdout, `"status": "PASS"`) {
			t.Fatalf("stdout = %s", stdout)
		}
	})
}

func TestPolicyCheckSoftFailureExitsWith0(t *testing.T) {
	bundle := policyWriteFiles(t, map[string]string{"soft.rego": `package org

policy_name["soft_probe"]

enable_rule["note"]

note[msg] { msg := "a soft problem" }
`})
	cfg := policyWriteFiles(t, map[string]string{"config.yml": "version: 2.1\n"})
	code, stdout, stderr := policyRun("check", "--config", filepath.Join(cfg, "config.yml"), "--bundle", bundle)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q, want 0", code, stderr)
	}
	if want := "SOFT_FAIL\n  soft  note: a soft problem\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestPolicyCheckErrorStatusExitsWith1(t *testing.T) {
	bundle := policyWriteFiles(t, map[string]string{"error.rego": `package org

policy_name["error_probe"]

enable_hard["conflict"]

conflict = "one"
conflict = "two" { true }
`})
	cfg := policyWriteFiles(t, map[string]string{"config.yml": "version: 2.1\n"})
	code, stdout, _ := policyRun("check", "--config", filepath.Join(cfg, "config.yml"), "--bundle", bundle)
	if code != 1 || !strings.HasPrefix(stdout, "ERROR\n") {
		t.Fatalf("code=%d stdout=%q, want 1 and an ERROR status", code, stdout)
	}
}

func TestPolicyCheckMeta(t *testing.T) {
	bundle := policyWriteFiles(t, map[string]string{"meta.rego": `package org

policy_name["meta_probe"]

enable_rule["meta"]

meta[msg] {
	msg := sprintf("project=%s branch=%s continuation=%v", [data.meta.project_id, data.meta.vcs.branch, data.meta.smarter.continuation])
}
`})
	cfg := policyWriteFiles(t, map[string]string{
		"config.yml": "version: 2.1\n",
		"meta.json":  `{"project_id": "from-file", "vcs": {"branch": "release"}}`,
	})

	t.Run("from a file", func(t *testing.T) {
		code, stdout, stderr := policyRun("check", "--config", filepath.Join(cfg, "config.yml"), "--bundle", bundle, "--meta", filepath.Join(cfg, "meta.json"))
		if code != 0 || !strings.Contains(stdout, "project=from-file branch=release continuation=true") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})

	t.Run("from the CircleCI environment", func(t *testing.T) {
		t.Setenv("CIRCLE_PROJECT_ID", "from-env")
		t.Setenv("CIRCLE_BRANCH", "topic")
		code, stdout, stderr := policyRun("check", "--config", filepath.Join(cfg, "config.yml"), "--bundle", bundle)
		if code != 0 || !strings.Contains(stdout, "project=from-env branch=topic continuation=true") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})
}

func TestPolicyCheckUsageErrors(t *testing.T) {
	cfg := policyWriteFiles(t, map[string]string{"config.yml": policyPassConfig})
	config := filepath.Join(cfg, "config.yml")
	tests := map[string][]string{
		"no arguments":          {},
		"unknown subcommand":    {"verify"},
		"no flags":              {"check"},
		"no bundle":             {"check", "--config", config},
		"no config":             {"check", "--bundle", policyBundle},
		"unknown format":        {"check", "--config", config, "--bundle", policyBundle, "--format", "xml"},
		"unknown flag":          {"check", "--config", config, "--bundle", policyBundle, "--strict"},
		"an extra argument":     {"check", "--config", config, "--bundle", policyBundle, "extra"},
		"a flag without value":  {"check", "--config"},
		"flags before the verb": {"--config", config, "check"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := policyRun(args...)
			if code != 2 {
				t.Fatalf("code = %d, want 2 (stdout %q, stderr %q)", code, stdout, stderr)
			}
			if stdout != "" || !strings.Contains(stderr, "usage: circleci-smarter policy check") {
				t.Fatalf("stdout = %q, stderr = %q; want the usage on stderr only", stdout, stderr)
			}
		})
	}

	for _, args := range [][]string{{"--help"}, {"help"}, {"check", "-h"}} {
		if code, _, _ := policyRun(args...); code != 0 {
			t.Errorf("policy %v: code = %d, want 0", args, code)
		}
	}
}

func TestPolicyCheckFatalErrors(t *testing.T) {
	dir := policyWriteFiles(t, map[string]string{
		"config.yml": policyPassConfig,
		"list.yml":   "- a\n- b\n",
		"meta.json":  `[1, 2]`,
		"empty":      "",
	})
	config := filepath.Join(dir, "config.yml")
	tests := map[string][]string{
		"missing config":        {"check", "--config", filepath.Join(dir, "nope.yml"), "--bundle", policyBundle},
		"config is not a map":   {"check", "--config", filepath.Join(dir, "list.yml"), "--bundle", policyBundle},
		"empty config":          {"check", "--config", filepath.Join(dir, "empty"), "--bundle", policyBundle},
		"missing bundle":        {"check", "--config", config, "--bundle", filepath.Join(dir, "nope")},
		"empty bundle":          {"check", "--config", config, "--bundle", t.TempDir()},
		"missing meta":          {"check", "--config", config, "--bundle", policyBundle, "--meta", filepath.Join(dir, "nope.json")},
		"meta is not an object": {"check", "--config", config, "--bundle", policyBundle, "--meta", filepath.Join(dir, "meta.json")},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := policyRun(args...)
			if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "policy check: ") {
				t.Fatalf("code=%d stdout=%q stderr=%q, want 1, nothing on stdout and an error on stderr", code, stdout, stderr)
			}
		})
	}
}

func TestLoadGuardrailsHook(t *testing.T) {
	if loadGuardrails == nil {
		t.Fatal("loadGuardrails is not set")
	}
	checker, err := loadGuardrails(policyBundle, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := checker.Check(context.Background(), []byte(policyMissingScanConfig))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != guard.HardFail || len(res.Violations) != 1 || res.Violations[0].RequireJobs[0] != "security-scan" {
		t.Fatalf("result = %+v", res)
	}

	// An error gives no checker, and not a typed nil that looks like one.
	checker, err = loadGuardrails(filepath.Join(t.TempDir(), "nope"), nil)
	if err == nil || checker != nil {
		t.Fatalf("loadGuardrails(nope) = %v, %v; want nil and an error", checker, err)
	}
}
