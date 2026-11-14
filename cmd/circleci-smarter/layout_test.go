package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

const layoutOK = `version: 2.1
jobs:
  security-scan:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
  build:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - persist_to_workspace: {root: ., paths: [dist]}
  test:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - attach_workspace: {at: .}
  smarter-gate:
    type: no-op
workflows:
  ci:
    jobs:
      - security-scan
      - build
      - test:
          requires: [build]
      - smarter-gate:
          requires: [security-scan, test]
`

// layoutBroken has a dangling requirement, and no gate.
const layoutBroken = `version: 2.1
jobs:
  build:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
workflows:
  ci:
    jobs:
      - build:
          requires: [lint]
`

func doLayout(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return run(t, func(a []string, o, e *bytes.Buffer) int { return runLayout(a, o, e) }, args...)
}

func decodeLayout(t *testing.T, stdout string) layoutJSON {
	t.Helper()
	var r layoutJSON
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	return r
}

func TestLayoutCheckPassesAGoodWorkflow(t *testing.T) {
	cfg := writeTemp(t, "continue.yml", layoutOK)
	code, stdout, stderr := doLayout(t, "check", "--config", cfg, "--mandatory", "security-scan")
	if code != 0 || stderr != "" {
		t.Fatalf("layout check = %d, %q\n%s", code, stderr, stdout)
	}
	r := decodeLayout(t, stdout)
	if r.Version != 1 || r.Workflow != "ci" || !r.OK || r.Findings == nil || len(r.Findings) != 0 {
		t.Errorf("reply = %+v", r)
	}
	if !strings.Contains(stdout, `"findings": []`) {
		t.Errorf("no findings must be an empty list, not null:\n%s", stdout)
	}
}

func TestLayoutCheckExitsWithThreeOnErrors(t *testing.T) {
	cfg := writeTemp(t, "continue.yml", layoutBroken)
	code, stdout, stderr := doLayout(t, "check", "--config", cfg, "--mandatory", "security-scan")
	if code != 3 || stderr != "" {
		t.Fatalf("layout check = %d, %q", code, stderr)
	}
	r := decodeLayout(t, stdout)
	if r.OK {
		t.Error("ok is true")
	}
	var got []string
	for _, f := range r.Findings {
		got = append(got, f.Code+" "+f.Job+" "+f.Severity)
		if f.Message == "" || f.FixHint == "" {
			t.Errorf("a finding without a message or a hint: %+v", f)
		}
	}
	want := []string{"dangling-requires build error", "gate-missing smarter-gate error", "mandatory-missing security-scan error"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("findings = %v, want %v", got, want)
	}
	// The same findings, as text.
	code, text, _ := doLayout(t, "check", "--config", cfg, "--mandatory", "security-scan", "--format", "text")
	if code != 3 || !strings.HasPrefix(text, "workflow ci: 3 errors, 0 warnings\n") ||
		!strings.Contains(text, "  error   dangling-requires [build]: build requires lint, which no job of workflow ci answers to\n          fix: ") {
		t.Errorf("text (exit %d):\n%s", code, text)
	}
}

func TestLayoutCheckWarningsDoNotFail(t *testing.T) {
	cfg := writeTemp(t, "continue.yml", `version: 2.1
jobs:
  build:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
  test:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
  deploy:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
workflows:
  ci:
    jobs:
      - build
      - test
      - hold:
          type: approval
          requires: [build]
          filters:
            branches:
              only: main
      - deploy:
          requires: [hold, test]
`)
	code, stdout, _ := doLayout(t, "check", "--config", cfg, "--gate-job", "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for a warning\n%s", code, stdout)
	}
	r := decodeLayout(t, stdout)
	if !r.OK || len(r.Findings) != 1 || r.Findings[0].Code != "filtered-approval-footgun" || r.Findings[0].Severity != "warning" {
		t.Errorf("reply = %+v", r)
	}
	code, text, _ := doLayout(t, "check", "--config", cfg, "--gate-job", "", "--format", "text")
	if code != 0 || !strings.HasPrefix(text, "workflow ci: 0 errors, 1 warning\n") || !strings.Contains(text, "  warning filtered-approval-footgun [deploy]:") {
		t.Errorf("text (exit %d):\n%s", code, text)
	}
}

func TestLayoutCheckWithThePolicyBundle(t *testing.T) {
	cfg := writeTemp(t, "continue.yml", layoutBroken)
	code, stdout, stderr := doLayout(t, "check", "--config", cfg, "--gate-job", "", "--policy-bundle", policyBundle)
	if code != 3 {
		t.Fatalf("exit = %d, %q\n%s", code, stderr, stdout)
	}
	var policyFinding *planner.Finding
	for _, f := range decodeLayout(t, stdout).Findings {
		if f.Code == "policy:required_jobs" {
			policyFinding = &f
		}
	}
	if policyFinding == nil || policyFinding.Severity != "error" || policyFinding.Job != "security-scan" || !strings.Contains(policyFinding.FixHint, "add the job security-scan") {
		t.Errorf("policy finding = %+v", policyFinding)
	}

	// A config that passes the bundle has no policy finding.
	good := writeTemp(t, "good.yml", layoutOK)
	code, stdout, stderr = doLayout(t, "check", "--config", good, "--policy-bundle", policyBundle)
	if code != 0 {
		t.Fatalf("exit = %d, %q\n%s", code, stderr, stdout)
	}
}

func TestLayoutCheckWithACatalog(t *testing.T) {
	good := writeTemp(t, "continue.yml", layoutOK)
	var catJSON bytes.Buffer
	if code, stdout, _ := doCatalog(t, "--config", good); code != 0 {
		t.Fatalf("catalog = %d", code)
	} else {
		catJSON.WriteString(stdout)
	}
	cat := writeTemp(t, "catalog.json", catJSON.String())

	// A layout that names the jobs of that file, and defines none.
	layout := writeTemp(t, "layout.yml", `version: 2.1
workflows:
  ci:
    jobs:
      - security-scan
      - test:
          requires: [security-scan]
      - smarter-gate:
          requires: [test]
`)
	// Without the catalog nothing is known of the jobs, and the gate is a name
	// that nothing in the file defines.
	code, stdout, _ := doLayout(t, "check", "--config", layout)
	if got := findingCodes(t, stdout); code != 3 || got != "gate-not-noop smarter-gate" {
		t.Errorf("without a catalog: exit %d, findings %v", code, got)
	}
	// With it, the gate is a no-op job, and test is known to attach a workspace
	// that security-scan does not persist.
	code, stdout, _ = doLayout(t, "check", "--config", layout, "--catalog", cat)
	if got := findingCodes(t, stdout); code != 3 || got != "consumer-before-producer test" {
		t.Errorf("with the catalog: exit %d, findings %v", code, got)
	}
}

// findingCodes lists the findings of a JSON reply as "code job".
func findingCodes(t *testing.T, stdout string) string {
	t.Helper()
	var got []string
	for _, f := range decodeLayout(t, stdout).Findings {
		got = append(got, f.Code+" "+f.Job)
	}
	return strings.Join(got, "|")
}

func TestLayoutCheckUsageAndFailures(t *testing.T) {
	good := writeTemp(t, "continue.yml", layoutOK)
	two := writeTemp(t, "two.yml", layoutOK+"  nightly:\n    jobs:\n      - security-scan\n")
	notCatalog := writeTemp(t, "catalog.json", layoutOK)
	tests := []struct {
		name      string
		args      []string
		code      int
		stderrHas string
	}{
		{"no subcommand", nil, 2, "usage: circleci-smarter layout check"},
		{"an unknown subcommand", []string{"lint"}, 2, `unknown subcommand "lint"`},
		{"an unknown flag", []string{"check", "--nope"}, 2, ""},
		{"a stray argument", []string{"check", "--config", good, "extra"}, 2, `unexpected argument "extra"`},
		{"a bad format", []string{"check", "--config", good, "--format", "yaml"}, 2, "--format must be json or text"},
		{"two workflows and none named", []string{"check", "--config", two}, 2, "2 workflows (ci, nightly)"},
		{"an unknown workflow", []string{"check", "--config", good, "--workflow", "nope"}, 2, `no workflow "nope"`},
		{"no config", []string{"check", "--config", filepath.Join(t.TempDir(), "missing.yml")}, 1, "cannot read the config"},
		{"a config that is not YAML", []string{"check", "--config", writeTemp(t, "bad.yml", "a: [")}, 1, "parse config"},
		{"a catalog that is a config", []string{"check", "--config", good, "--catalog", notCatalog}, 1, "it must be what `circleci-smarter catalog` printed"},
		{"a catalog that is missing", []string{"check", "--config", good, "--catalog", filepath.Join(t.TempDir(), "none.json")}, 1, "cannot read the catalog"},
		{"a bundle that is missing", []string{"check", "--config", good, "--policy-bundle", filepath.Join(t.TempDir(), "none")}, 1, "cannot load the policy bundle"},
		{"help", []string{"-h"}, 0, ""},
		{"check help", []string{"check", "-h"}, 0, "usage: circleci-smarter layout check"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := doLayout(t, tc.args...)
			if code != tc.code {
				t.Fatalf("exit = %d, want %d (stderr %q)", code, tc.code, stderr)
			}
			if !strings.Contains(stderr, tc.stderrHas) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tc.stderrHas)
			}
			if tc.code != 0 && stdout != "" {
				t.Errorf("a failure printed on stdout: %q", stdout)
			}
		})
	}
}

// A plan of the planner passes the layout check: that is what the check is for.
func TestLayoutCheckPassesWhatThePlannerEmits(t *testing.T) {
	c := newCLIRepo(t)
	c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
	c.Write("a.txt", "a changed\n")
	c.Commit("change a")
	if code, _, stderr := doPlan(t, c.planArgs()...); code != 0 {
		t.Fatalf("plan = %d, %q", code, stderr)
	}
	if _, err := os.Stat(c.path("continue.yml")); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := doLayout(t, "check", "--config", c.path("continue.yml"), "--format", "text")
	if code != 0 {
		t.Fatalf("layout check of the plan = %d, %q\n%s", code, stderr, stdout)
	}
}
