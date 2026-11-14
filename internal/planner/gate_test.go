package planner_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

func TestGateRequiresTheJobsThatWillRun(t *testing.T) {
	repo := &fakeRepo{changes: changes("M docs/index.md"), dirs: map[string]map[string]bool{"base1": sourceDirs(), "head1": sourceDirs()}}

	t.Run("select mode", func(t *testing.T) {
		o := opts()
		o.Mandatory = []string{"security-scan"}
		res := plan(t, input(repo), o)
		want := []string{"lint", "security-scan", "docs"}
		if got := res.Report.Gate.Requires; !slices.Equal(got, want) {
			t.Fatalf("gate requires %v, want %v", got, want)
		}
		if res.Report.Gate.Name != "smarter-gate" {
			t.Fatalf("gate = %+v", res.Report.Gate)
		}
		if got := emittedEntry(t, res, "ci", "smarter-gate").Requires; !slices.Equal(got, want) {
			t.Fatalf("emitted gate requires %v, want %v", got, want)
		}
	})

	t.Run("shadow mode keeps every job, so the gate waits for every job that will run", func(t *testing.T) {
		o := opts()
		o.Mode = planner.ModeShadow
		o.Mandatory = []string{"security-scan"}
		res := plan(t, input(repo), o)
		wantGate := []string{"lint", "security-scan", "api-test", "web-test", "build", "e2e", "docs"}
		if got := res.Report.Gate.Requires; !slices.Equal(got, wantGate) {
			t.Fatalf("gate requires %v, want %v", got, wantGate)
		}
		// The report says what select mode would run, and the config keeps all.
		if got := running(res); !sameSet(got, []string{"lint", "security-scan", "docs"}) {
			t.Fatalf("running = %v", got)
		}
		wantJobs := []string{"lint", "security-scan", "api-test", "web-test", "build", "e2e", "docs", "hold", "deploy", "smarter-gate"}
		if got := emittedJobs(t, res, "ci"); !slices.Equal(got, wantJobs) {
			t.Fatalf("emitted jobs = %v, want %v", got, wantJobs)
		}
		// Nothing but the gate was added: every other entry is as it was.
		for _, name := range []string{"api-test", "build", "e2e", "hold"} {
			orig := entryOf(t, monorepo, name)
			if got := emittedEntry(t, res, "ci", name); !slices.Equal(got.Requires, orig.Requires) || got.Approval != orig.Approval {
				t.Errorf("%s changed in shadow mode", name)
			}
		}
	})

	t.Run("when nothing else is selected the workflow holds only the gate", func(t *testing.T) {
		repo := &fakeRepo{changes: changes("M CHANGELOG.md"), dirs: map[string]map[string]bool{"base1": sourceDirs(), "head1": sourceDirs()}}
		res := plan(t, input(repo), opts())
		if got := emittedJobs(t, res, "ci"); !slices.Equal(got, []string{"smarter-gate"}) {
			t.Fatalf("emitted jobs = %v", got)
		}
		if len(res.Report.Gate.Requires) != 0 {
			t.Fatalf("gate requires %v", res.Report.Gate.Requires)
		}
		// A gate with nothing to wait for is a bare entry.
		if !strings.Contains(string(res.Config), "ci:\n    # the pipeline under test\n") && !strings.Contains(string(res.Config), "      - smarter-gate\n") {
			t.Errorf("want a bare gate entry:\n%s", res.Config)
		}
		if strings.Contains(string(res.Config), "requires: []") {
			t.Errorf("the gate has an empty requires:\n%s", res.Config)
		}
	})

	t.Run("approvals and what follows them are not awaited", func(t *testing.T) {
		o := opts()
		o.Branch = "main" // a full run, where hold and deploy do run
		res := plan(t, input(repo), o)
		want := []string{"lint", "security-scan", "api-test", "web-test", "build", "e2e", "docs"}
		if got := res.Report.Gate.Requires; !slices.Equal(got, want) {
			t.Fatalf("gate requires %v, want %v", got, want)
		}
	})

	t.Run("the gate job is named and imaged as asked, and small", func(t *testing.T) {
		o := opts()
		o.GateJob, o.GateImage = "pr-gate", "cimg/base:2026.10"
		res := plan(t, input(repo), o)
		out := string(res.Config)
		want := "  pr-gate:\n    docker:\n      - image: cimg/base:2026.10\n    resource_class: small\n    steps:\n      - run:\n          name: gate\n          command: |\n            echo \"every job this plan selected has passed\"\n"
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks the gate definition:\n%s", out)
		}
		if res.Report.Gate.Name != "pr-gate" || emittedJobs(t, res, "ci")[len(emittedJobs(t, res, "ci"))-1] != "pr-gate" {
			t.Fatalf("gate = %+v", res.Report.Gate)
		}
	})
}

func TestInertJobsAreRemovedWithTheirChain(t *testing.T) {
	repo := &fakeRepo{changes: changes("M services/api/main.go"), dirs: map[string]map[string]bool{"base1": sourceDirs(), "head1": sourceDirs()}}
	res := plan(t, input(repo), opts())
	for _, gone := range []string{"hold", "deploy"} {
		if slices.Contains(emittedJobs(t, res, "ci"), gone) {
			t.Errorf("%s stays in the plan although it cannot run on this branch", gone)
		}
	}
	// And its absence does not drag the jobs it would have required.
	if slices.Contains(running(res), "web-test") {
		t.Errorf("web-test runs only because the deploy chain wanted it: %v", running(res))
	}
}

func TestMandatoryJobBehindFiltersKeepsItsAncestors(t *testing.T) {
	const cfg = `version: 2.1
jobs:
  prep: {docker: [{image: x}], steps: [checkout]}
  scan: {docker: [{image: x}], steps: [checkout]}
  other: {docker: [{image: x}], steps: [checkout]}
workflows:
  w:
    jobs:
      - prep
      - scan:
          requires: [prep]
          filters:
            branches:
              only: main
      - other
`
	m := &impactmap.Map{Version: 1, Jobs: map[string]*impactmap.Job{
		"prep":  {Files: []string{"p"}, Complete: true},
		"scan":  {Files: []string{"s"}, Complete: true},
		"other": {Files: []string{"o"}, Complete: true},
	}}
	in := planner.Input{Config: []byte(cfg), Map: m, Repo: &fakeRepo{changes: changes("M o")}}
	o := opts()
	o.Workflow = "w"

	// Left alone, scan cannot run here and is dropped, with no effect on prep.
	res := plan(t, in, o)
	if got := emittedJobs(t, res, "w"); !slices.Equal(got, []string{"other", "smarter-gate"}) {
		t.Fatalf("emitted jobs = %v", got)
	}

	// Mandatory means the job stays in the plan, and so do the jobs it needs,
	// so that the config is never left with a requirement it cannot meet. It
	// still does not run on this branch, so the gate does not wait for it.
	o.Mandatory = []string{"scan"}
	res = plan(t, in, o)
	if got := emittedJobs(t, res, "w"); !slices.Equal(got, []string{"prep", "scan", "other", "smarter-gate"}) {
		t.Fatalf("emitted jobs = %v", got)
	}
	if got := res.Report.Gate.Requires; !slices.Equal(got, []string{"prep", "other"}) {
		t.Fatalf("gate requires %v", got)
	}
	if got := reasons(t, res, "scan"); got[0] != "mandatory job" || !strings.Contains(got[1], "does not run") {
		t.Fatalf("scan reasons = %v", got)
	}
	if got := reasons(t, res, "prep"); got[0] != "required by scan" {
		t.Fatalf("prep reasons = %v", got)
	}
	if got := running(res); !sameSet(got, []string{"prep", "other"}) {
		t.Fatalf("running = %v", got)
	}
}

func TestExpressionFiltersAreReportedAndLeftOutOfTheGate(t *testing.T) {
	const cfg = `version: 2.1
jobs:
  a: {docker: [{image: x}], steps: [checkout]}
  b: {docker: [{image: x}], steps: [checkout]}
  c: {docker: [{image: x}], steps: [checkout]}
workflows:
  w:
    jobs:
      - a
      - b:
          filters: pipeline.git.branch == "feature"
      - c:
          requires: [b]
`
	o := opts()
	o.Workflow = "w"
	o.Mode = planner.ModeShadow
	res := plan(t, planner.Input{Config: []byte(cfg), Map: monorepoMap(), Repo: &fakeRepo{changes: changes("M x")}}, o)
	if got := res.Report.Gate.Requires; !slices.Equal(got, []string{"a"}) {
		t.Fatalf("gate requires %v: b's filter is an expression, and c depends on b", got)
	}
	found := false
	for _, w := range res.Report.Warnings {
		if strings.Contains(w, "job b") && strings.Contains(w, "left out of the gate") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want one about job b", res.Report.Warnings)
	}
}
