package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests run `plan --policy-bundle` against the real guardrail bundle of
// policies/, through the checker that policy.go registers.

// realBundle is the absolute path of policies/. Call it before newCLIRepo,
// which changes into a temp repository.
func realBundle(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../../policies")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

const cliScanConfig = `version: 2.1
jobs:
  ta:
    docker: [{image: "cimg/base:2026.11"}]
    resource_class: small
    steps:
      - checkout
      - run: echo a
  tb:
    docker: [{image: "cimg/base:2026.11"}]
    resource_class: small
    steps:
      - checkout
      - run: echo b
  security-scan:
    docker: [{image: "cimg/base:2026.11"}]
    resource_class: small
    steps:
      - checkout
      - run: echo scan
workflows:
  w:
    jobs:
      - ta
      - tb
      - security-scan
`

func TestPlanCommandRepairsAMissingRequiredJobWithTheRealBundle(t *testing.T) {
	if loadGuardrails == nil {
		t.Skip("this build has no policy support")
	}
	bundle := realBundle(t)
	c := newCLIRepoWith(t, cliScanConfig)
	c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}, "security-scan": {"go.sum"}})
	c.Write("a.txt", "changed\n")
	c.Commit("change a")

	// security-scan is not mandatory, so a change to a.txt leaves it out, and the
	// bundle requires it in every workflow: the planner puts it back.
	code, _, stderr := doPlan(t, c.planArgs("--policy-bundle", bundle)...)
	if code != 0 {
		t.Fatalf("plan = %d, %q", code, stderr)
	}
	rep := readReport(t, c)
	if rep.Full || rep.Guardrails.Status != "PASS" || len(rep.Guardrails.Repairs) != 1 || rep.Guardrails.Repairs[0] != "require-job=security-scan" {
		t.Fatalf("report = %+v", rep)
	}
	if !rep.Jobs[2].Run || !strings.Contains(rep.Jobs[2].Reasons[0], "required by the guardrails") {
		t.Fatalf("jobs = %+v", rep.Jobs)
	}
	cfg, err := os.ReadFile(c.path("continue.yml"))
	if err != nil || !strings.Contains(string(cfg), "- security-scan") || strings.Contains(string(cfg), "- tb") {
		t.Fatalf("config (%v):\n%s", err, cfg)
	}
}

func TestPlanCommandExitsWithThreeWhenTheRealBundleRejectsEvenTheFullPlan(t *testing.T) {
	if loadGuardrails == nil {
		t.Skip("this build has no policy support")
	}
	bundle := realBundle(t)
	c := newCLIRepo(t) // its config has no security-scan job at all
	c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
	c.Write("a.txt", "changed\n")
	c.Commit("change a")

	code, stdout, stderr := doPlan(t, c.planArgs("--policy-bundle", bundle)...)
	if code != 3 || stdout != "" || !strings.Contains(stderr, "security-scan") || !strings.Contains(stderr, "must not continue") {
		t.Fatalf("plan = %d, %q, %q", code, stdout, stderr)
	}
	rep := readReport(t, c)
	if rep.Guardrails.Status != "HARD_FAIL" || !rep.Full || len(rep.Guardrails.Violations) == 0 {
		t.Fatalf("report = %+v", rep)
	}
	if _, err := os.Stat(c.path("continue.yml")); err == nil {
		t.Fatal("the config must not be written when the guardrails fail")
	}
	if _, err := os.Stat(filepath.Join(c.path("plan"), "plan.json")); err != nil {
		t.Fatalf("the report must be written: %v", err)
	}
}
