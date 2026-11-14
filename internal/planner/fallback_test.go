package planner_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

// Once the config has loaded, an internal error never fails the planner: it
// plans a full run, and records why.

func TestInternalErrorsGiveAFullPlan(t *testing.T) {
	dirs := map[string]map[string]bool{"base1": sourceDirs(), "head1": sourceDirs()}
	tests := []struct {
		name string
		repo *fakeRepo
		want string
	}{
		{"the directory check fails", &fakeRepo{changes: changes("A services/web/src/new.ts"), dirs: dirs, dirsErr: errBoom}, "internal error: checking directories at the base: boom"},
		{"the repo panics", &fakeRepo{panicMsg: "kaboom", dirs: dirs}, "internal error: planner panic: kaboom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := plan(t, input(tc.repo), opts())
			if !res.Report.Full || !strings.Contains(res.Report.FullReason, tc.want) {
				t.Fatalf("full = %v, reason = %q, want %q", res.Report.Full, res.Report.FullReason, tc.want)
			}
			// The plan is the user's whole pipeline plus the gate.
			got := emittedJobs(t, res, "ci")
			want := []string{"lint", "security-scan", "api-test", "web-test", "build", "e2e", "docs", "hold", "deploy", "smarter-gate"}
			if !slices.Equal(got, want) {
				t.Fatalf("emitted jobs = %v", got)
			}
			if !strings.Contains(strings.Join(res.Report.Warnings, "\n"), "internal error, planned a full run") {
				t.Fatalf("warnings = %v", res.Report.Warnings)
			}
			if res.GuardrailsFailed {
				t.Fatal("an internal error must not fail the guardrails")
			}
		})
	}
}

func TestADuplicateJobNameFallsBackToTheConfigAsItCame(t *testing.T) {
	const cfg = `version: 2.1
jobs:
  a: {docker: [{image: x}], steps: [checkout]}
workflows:
  w:
    # two jobs with the same name: CircleCI will say so
    jobs:
      - a
      - a
`
	o := opts()
	o.Workflow = "w"
	res := plan(t, planner.Input{Config: []byte(cfg), Map: monorepoMap(), Repo: &fakeRepo{changes: changes("M x")}}, o)
	if string(res.Config) != cfg {
		t.Fatalf("the config must pass through untouched:\n%s", res.Config)
	}
	if !res.Report.Full || !strings.Contains(res.Report.FullReason, `share the name "a"`) || res.Report.Gate != nil {
		t.Fatalf("full = %v, reason = %q, gate = %+v", res.Report.Full, res.Report.FullReason, res.Report.Gate)
	}
	if len(res.Report.Jobs) != 2 || !res.Report.Jobs[0].Run {
		t.Fatalf("jobs = %+v", res.Report.Jobs)
	}
	if !strings.Contains(strings.Join(res.Report.Warnings, "\n"), "without the gate job") {
		t.Fatalf("warnings = %v", res.Report.Warnings)
	}
}

func TestAGateNameThatIsTakenFallsBackToTheConfigAsItCame(t *testing.T) {
	o := opts()
	o.GateJob = "lint"
	res := plan(t, input(&fakeRepo{changes: changes("M docs/index.md")}), o)
	if string(res.Config) != monorepo || res.Report.Gate != nil || !res.Report.Full {
		t.Fatalf("gate = %+v, full = %v", res.Report.Gate, res.Report.Full)
	}
}

func TestNoGitAtAll(t *testing.T) {
	// No base, no head, no repo: a full plan, not an error.
	in := planner.Input{Config: []byte(monorepo), Map: monorepoMap()}
	o := opts()
	o.Base, o.Head, o.NoBaseReason = "", "", "not a git repository"
	res := plan(t, in, o)
	if !res.Report.Full || res.Report.FullReason != "no diff base: not a git repository" {
		t.Fatalf("full = %v, reason = %q", res.Report.Full, res.Report.FullReason)
	}
}

func TestAnInvalidMapPathIsReportedAndTheJobAlwaysRuns(t *testing.T) {
	m, err := impactmap.Parse([]byte(`{"version":1,"revision":"base1","jobs":{"docs":{"files":["/abs/path"],"complete":true,"nodes":1},"lint":{"files":["README.md"],"complete":true,"nodes":1}}}`))
	if err != nil {
		t.Fatal(err)
	}
	in := planner.Input{Config: []byte(monorepo), Map: m, Repo: &fakeRepo{changes: changes("M CHANGELOG.md")}}
	res := plan(t, in, opts())
	if !slices.Contains(running(res), "docs") {
		t.Fatalf("running = %v: a job with an unusable map entry must run", running(res))
	}
	found := false
	for _, w := range res.Report.Warnings {
		if strings.Contains(w, `job "docs" has 1 unusable paths`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v", res.Report.Warnings)
	}
}

func TestPlanWarnings(t *testing.T) {
	repo := &fakeRepo{changes: changes("M docs/index.md"), dirs: map[string]map[string]bool{"base1": sourceDirs(), "head1": sourceDirs()}}
	in := input(repo)
	in.Map.Revision = "0123456789abcdef"
	o := opts()
	o.Mandatory = []string{"nope"}
	o.ExtraInputs = map[string][]string{"ghost": {"x/**"}}
	res := plan(t, in, o)
	joined := strings.Join(res.Report.Warnings, "\n")
	for _, want := range []string{
		"mandatory job nope is not in workflow ci",
		"extra inputs name job ghost, which is not in workflow ci",
		"the impact map was built at 0123456, and the diff base is base1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings lack %q:\n%s", want, joined)
		}
	}
}

func TestReportBasics(t *testing.T) {
	repo := &fakeRepo{changes: changes("M services/api/main.go", "A new.txt"), dirs: map[string]map[string]bool{"base1": sourceDirs(), "head1": sourceDirs()}}
	res := plan(t, input(repo), opts())
	r := res.Report
	if r.Version != 1 || r.Mode != "select" || r.Workflow != "ci" || r.Base != "base1" || r.Head != "head1" || r.Branch != "feature" ||
		r.MapRevision != "base1" || r.MapCreatedAt != "2026-11-12T18:30:00Z" {
		t.Fatalf("report header = %+v", r)
	}
	if len(r.Changed) != 2 || r.Changed[0].Path != "new.txt" || r.Changed[0].Status != "A" || r.Changed[1].Path != "services/api/main.go" {
		t.Fatalf("changed = %+v", r.Changed)
	}
	if r.Full || r.FullReason != "" {
		t.Fatalf("full = %v %q", r.Full, r.FullReason)
	}
	data, err := r.JSON()
	if err != nil || !strings.Contains(string(data), `"jobs"`) {
		t.Fatalf("JSON: %v", err)
	}
}
