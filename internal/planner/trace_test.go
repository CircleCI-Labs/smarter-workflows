package planner_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
	"github.com/CircleCI-Labs/smarter-workflows/internal/tracescript"
)

// traceConfig has one of each kind of job the trace step must tell apart.
const traceConfig = `version: 2.1
orbs:
  o: acme/tools@1.2.3
executors:
  base:
    docker: [{image: "cimg/base:2026.11"}]
jobs:
  build:
    executor: base
    steps:
      - run: echo before
      - checkout
      - run: make
      - persist_to_workspace:
          root: .
          paths: [dist]
  test:
    machine:
      image: ubuntu-2204:current
    steps:
      - checkout
      - run: make test
  nocheckout:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - run: echo no checkout
  mac:
    macos:
      xcode: "16"
    steps: [checkout]
  win:
    machine:
      image: windows-server-2022-gui:current
    steps: [checkout]
  mat:
    parameters:
      v: {type: string}
    docker: [{image: "cimg/base:2026.11"}]
    steps: [checkout]
  late:
    docker: [{image: "cimg/base:2026.11"}]
    steps: [checkout]
workflows:
  w:
    jobs:
      - build
      - test:
          requires: [build]
      - nocheckout
      - mac
      - win
      - o/orbjob
      - mat:
          matrix:
            parameters:
              v: [a, b]
      - hold:
          type: approval
          requires: [test]
      - late:
          requires: [hold]
  other:
    jobs: [mac]
`

func traceInput() planner.Input {
	return planner.Input{Config: []byte(traceConfig), Map: &impactmap.Map{Version: 1}, Repo: &fakeRepo{}}
}

func traceOpts() planner.Options {
	o := opts()
	o.Workflow = "w"
	o.Branch = "main"
	o.Base = ""
	return o
}

func decodeConfig(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// workflowEntry returns the arguments of a workflow entry from decoded YAML.
func workflowEntry(t *testing.T, cfg map[string]any, workflow, name string) map[string]any {
	t.Helper()
	jobs := cfg["workflows"].(map[string]any)[workflow].(map[string]any)["jobs"].([]any)
	for _, j := range jobs {
		switch v := j.(type) {
		case string:
			if v == name {
				return map[string]any{}
			}
		case map[string]any:
			if args, ok := v[name]; ok {
				if args == nil {
					return map[string]any{}
				}
				return args.(map[string]any)
			}
		}
	}
	t.Fatalf("no entry %q in workflow %q", name, workflow)
	return nil
}

func jobSteps(t *testing.T, cfg map[string]any, job string) []any {
	t.Helper()
	def, ok := cfg["jobs"].(map[string]any)[job].(map[string]any)
	if !ok {
		t.Fatalf("no job %q", job)
	}
	return def["steps"].([]any)
}

func runStep(step any) map[string]any {
	m, ok := step.(map[string]any)
	if !ok {
		return nil
	}
	r, _ := m["run"].(map[string]any)
	return r
}

func TestTraceOfAFullRunOnTheDefaultBranch(t *testing.T) {
	res := plan(t, traceInput(), traceOpts())
	if !res.Report.Full || !strings.Contains(res.Report.FullReason, "default branch main") {
		t.Fatalf("full = %v, %q", res.Report.Full, res.Report.FullReason)
	}
	cfg := decodeConfig(t, res.Config)

	// Traced: local Linux jobs, through a docker executor, a Linux machine and
	// an executor reference. Not traced: macOS, Windows, the matrix entry, the
	// approval and the orb job.
	wantTraced := []string{"build", "test", "nocheckout", "late"}
	if res.Report.Trace == nil || !slices.Equal(res.Report.Trace.Jobs, wantTraced) {
		t.Fatalf("trace = %+v, want jobs %v", res.Report.Trace, wantTraced)
	}
	// The map job waits for the traced jobs that run for sure, and not for
	// what only runs after an approval.
	wantRequires := []string{"build", "test", "nocheckout"}
	if got := res.Report.Trace.Requires; !slices.Equal(got, wantRequires) {
		t.Fatalf("map job requires %v, want %v", got, wantRequires)
	}
	if res.Report.Trace.MapJob != "smarter-impact-map" {
		t.Fatalf("map job = %q", res.Report.Trace.MapJob)
	}
	if got := emittedEntry(t, res, "w", "smarter-impact-map").Requires; !slices.Equal(got, wantRequires) {
		t.Fatalf("emitted map job requires %v", got)
	}

	for _, name := range wantTraced {
		args := workflowEntry(t, cfg, "w", name)
		post, ok := args["post-steps"].([]any)
		if !ok || len(post) != 2 {
			t.Fatalf("%s: post-steps = %v", name, args["post-steps"])
		}
		collect := runStep(post[0])
		if collect["name"] != "smarter: collect trace" || collect["when"] != "always" || collect["shell"] != "/bin/sh" || collect["command"] != tracescript.Script {
			t.Errorf("%s: collect step = %v", name, collect)
		}
		persist := post[1].(map[string]any)["persist_to_workspace"].(map[string]any)
		if persist["root"] != "/tmp/smarter" || !slices.Equal(toStrings(persist["paths"]), []string{"traces"}) {
			t.Errorf("%s: persist step = %v", name, persist)
		}
		if _, has := args["pre-steps"]; has {
			t.Errorf("%s: a local job gets its reset inside its definition, not as a pre-step", name)
		}
	}
	for _, name := range []string{"mac", "win", "mat", "hold", "o/orbjob"} {
		if args := workflowEntry(t, cfg, "w", name); args["post-steps"] != nil || args["pre-steps"] != nil {
			t.Errorf("%s must not be traced: %v", name, args)
		}
	}

	// The reset follows the first checkout of each traced job definition.
	for _, job := range []string{"build", "test", "late"} {
		steps := jobSteps(t, cfg, job)
		i := slices.IndexFunc(steps, func(s any) bool { return s == "checkout" })
		if i < 0 || i+1 >= len(steps) {
			t.Fatalf("%s: steps = %v", job, steps)
		}
		reset := runStep(steps[i+1])
		if reset["name"] != "smarter: reset atime" || reset["shell"] != "/bin/sh" || reset["command"] != tracescript.Reset || reset["when"] != nil {
			t.Errorf("%s: step after checkout = %v", job, steps[i+1])
		}
	}
	if steps := jobSteps(t, cfg, "nocheckout"); len(steps) != 1 {
		t.Errorf("a job without checkout gets no reset: %v", steps)
	}
	if steps := jobSteps(t, cfg, "mac"); len(steps) != 1 {
		t.Errorf("an untraced job is not edited: %v", steps)
	}
	// Reset once, even when two entries share a definition: "build" has only one.
	if n := strings.Count(string(res.Config), "name: 'smarter: reset atime'"); n != 3 {
		t.Errorf("%d reset steps, want 3", n)
	}

	// The map job, step by step (step 8.3).
	mapJob := cfg["jobs"].(map[string]any)["smarter-impact-map"].(map[string]any)
	if mapJob["resource_class"] != "small" {
		t.Errorf("map job resource class = %v", mapJob["resource_class"])
	}
	steps := mapJob["steps"].([]any)
	if len(steps) != 7 {
		t.Fatalf("map job has %d steps: %v", len(steps), steps)
	}
	if c := runStep(steps[0])["command"]; c != "printf '%s\\n' dev-test > .smarter-version\n" {
		t.Errorf("version step = %q", c)
	}
	if k := steps[1].(map[string]any)["restore_cache"].(map[string]any)["key"]; k != `smarter-bin-v1-{{ checksum ".smarter-version" }}-{{ arch }}` {
		t.Errorf("restore_cache key = %q", k)
	}
	if at := steps[2].(map[string]any)["attach_workspace"].(map[string]any)["at"]; at != "/tmp/smarter" {
		t.Errorf("attach_workspace at = %v", at)
	}
	if c := runStep(steps[3])["command"]; c != "/usr/local/bin/circleci-smarter map build --traces /tmp/smarter/traces --revision \"$CIRCLE_SHA1\" --out /tmp/smarter/impact-map.json\n" {
		t.Errorf("map build command = %q", c)
	}
	if c := runStep(steps[4])["command"]; c != "printf '%s\\n' \"$CIRCLE_SHA1\" > .smarter-key\n" {
		t.Errorf("key step = %q", c)
	}
	save := steps[5].(map[string]any)["save_cache"].(map[string]any)
	if save["key"] != `smarter-impact-v1-{{ checksum ".smarter-key" }}` || !slices.Equal(toStrings(save["paths"]), []string{"/tmp/smarter/impact-map.json"}) {
		t.Errorf("save_cache = %v", save)
	}
	if p := steps[6].(map[string]any)["store_artifacts"].(map[string]any)["path"]; p != "/tmp/smarter/impact-map.json" {
		t.Errorf("store_artifacts path = %v", p)
	}

	// The gate is still last, and the other workflow is untouched.
	if got := emittedJobs(t, res, "w"); got[len(got)-1] != "smarter-gate" || got[len(got)-2] != "smarter-impact-map" {
		t.Errorf("emitted jobs = %v", got)
	}
	if got := emittedJobs(t, res, "other"); !slices.Equal(got, []string{"mac"}) {
		t.Errorf("the other workflow = %v", got)
	}
	// No pipeline parameters, no expressions.
	noNewExpressions(t, []byte(traceConfig), res.Config)
	if _, has := cfg["parameters"]; has {
		t.Error("the plan added pipeline parameters")
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestTraceOrbJobs(t *testing.T) {
	o := traceOpts()
	o.TraceOrbJobs = true
	res := plan(t, traceInput(), o)
	cfg := decodeConfig(t, res.Config)
	args := workflowEntry(t, cfg, "w", "o/orbjob")
	pre, ok := args["pre-steps"].([]any)
	if !ok || len(pre) != 2 || pre[0] != "checkout" {
		t.Fatalf("pre-steps = %v: want checkout, then the reset", args["pre-steps"])
	}
	if reset := runStep(pre[1]); reset["name"] != "smarter: reset atime" || reset["command"] != tracescript.Reset || reset["shell"] != "/bin/sh" {
		t.Fatalf("reset step = %v", pre[1])
	}
	if post, ok := args["post-steps"].([]any); !ok || len(post) != 2 {
		t.Fatalf("post-steps = %v", args["post-steps"])
	}
	if got := res.Report.Trace.Requires; !slices.Equal(got, []string{"build", "test", "nocheckout", "o/orbjob"}) {
		t.Fatalf("map job requires %v", got)
	}
}

func TestTraceWithoutTheReset(t *testing.T) {
	o := traceOpts()
	o.TraceOrbJobs = true
	o.TraceReset = false
	res := plan(t, traceInput(), o)
	if strings.Contains(string(res.Config), "smarter: reset atime") {
		t.Fatalf("the reset is off, but the plan has it:\n%s", res.Config)
	}
	cfg := decodeConfig(t, res.Config)
	if args := workflowEntry(t, cfg, "w", "o/orbjob"); args["pre-steps"] != nil || args["post-steps"] == nil {
		t.Fatalf("orb job = %v", args)
	}
	if args := workflowEntry(t, cfg, "w", "build"); args["post-steps"] == nil {
		t.Fatalf("build is still traced: %v", args)
	}
}

func TestTraceModes(t *testing.T) {
	tests := []struct {
		name      string
		mod       func(*planner.Options)
		wantTrace bool
	}{
		{"auto, default branch", func(o *planner.Options) {}, true},
		{"auto, a scheduled run on the default branch", func(o *planner.Options) { o.TriggerType = "scheduled_pipeline" }, true},
		{"auto, a forced full run on a feature branch", func(o *planner.Options) { o.Branch, o.ForceFull = "feature", true }, false},
		{"auto, a tag", func(o *planner.Options) { o.Branch, o.Tag = "", "v1" }, false},
		{"off, default branch", func(o *planner.Options) { o.Trace = planner.TraceOff }, false},
		{"on, a forced full run on a feature branch", func(o *planner.Options) { o.Branch, o.ForceFull, o.Trace = "feature", true, planner.TraceOn }, true},
		// On a tag, jobs without a tags filter do not run, so nothing is certain to run.
		{"on, a tag", func(o *planner.Options) { o.Branch, o.Tag, o.Trace = "", "v1", planner.TraceOn }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := traceOpts()
			tc.mod(&o)
			res := plan(t, traceInput(), o)
			got := res.Report.Trace != nil
			has := strings.Contains(string(res.Config), "smarter-impact-map")
			if got != tc.wantTrace || has != tc.wantTrace {
				t.Fatalf("trace in report = %v, in config = %v, want %v\n%v", got, has, tc.wantTrace, res.Report.Warnings)
			}
		})
	}
}

func TestTraceOnAPrunedPlanTracesWhatIsLeft(t *testing.T) {
	// --trace on, in select mode on a branch: only the jobs the plan keeps are
	// traced, and the map job waits for them.
	cfg := `version: 2.1
jobs:
  a: {docker: [{image: x}], steps: [checkout]}
  b: {docker: [{image: x}], steps: [checkout]}
workflows:
  w:
    jobs: [a, b]
`
	m := &impactmap.Map{Version: 1, Jobs: map[string]*impactmap.Job{
		"a": {Files: []string{"a.txt"}, Complete: true},
		"b": {Files: []string{"b.txt"}, Complete: true},
	}}
	o := opts()
	o.Workflow, o.Trace = "w", planner.TraceOn
	res := plan(t, planner.Input{Config: []byte(cfg), Map: m, Repo: &fakeRepo{changes: changes("M a.txt")}}, o)
	if res.Report.Full {
		t.Fatalf("full run: %s", res.Report.FullReason)
	}
	if got := res.Report.Trace.Jobs; !slices.Equal(got, []string{"a"}) {
		t.Fatalf("traced = %v", got)
	}
	if got := emittedJobs(t, res, "w"); !slices.Equal(got, []string{"a", "smarter-impact-map", "smarter-gate"}) {
		t.Fatalf("jobs = %v", got)
	}
}

// An entry whose pre-steps or post-steps is not a list (CircleCI would refuse
// the config) cannot take the trace steps. It is left as it is, with a warning,
// and the other entries are traced: its map entry will be missing, so later
// plans always run it. A key with no value is an empty list, and is traced.
func TestTraceSkipsAnEntryThatCannotTakeTheSteps(t *testing.T) {
	const cfg = `version: 2.1
orbs:
  o: acme/tools@1.2.3
jobs:
  good: {docker: [{image: x}], steps: [checkout]}
  empty: {docker: [{image: x}], steps: [checkout]}
  bad-post: {docker: [{image: x}], steps: [checkout]}
  bad-pre: {docker: [{image: x}], steps: [checkout]}
workflows:
  w:
    jobs:
      - good
      - empty:
          pre-steps:
          post-steps: ~
      - bad-post:
          post-steps: not a list
      - bad-pre:
          pre-steps: {run: not a list}
      - o/orbjob:
          name: orb-bad
          pre-steps: 3
      - o/orbjob:
          name: orb-good
          pre-steps:
`
	o := traceOpts()
	o.TraceOrbJobs = true
	res := plan(t, planner.Input{Config: []byte(cfg), Map: &impactmap.Map{Version: 1}, Repo: &fakeRepo{}}, o)
	if strings.Contains(strings.Join(res.Report.Warnings, "\n"), "internal error") {
		t.Fatalf("an entry's shape must not turn the plan into a fallback: %v", res.Report.Warnings)
	}
	wantTraced := []string{"good", "empty", "orb-good"}
	if res.Report.Trace == nil || !slices.Equal(res.Report.Trace.Jobs, wantTraced) || !slices.Equal(res.Report.Trace.Requires, wantTraced) {
		t.Fatalf("trace = %+v, want the jobs %v", res.Report.Trace, wantTraced)
	}
	joined := strings.Join(res.Report.Warnings, "\n")
	for _, want := range []string{
		`trace: bad-post is not traced, so later plans always run it: post-steps of "bad-post" is not a list`,
		`trace: bad-pre is not traced, so later plans always run it: pre-steps of "bad-pre" is not a list`,
		`trace: orb-bad is not traced, so later plans always run it: pre-steps of "orb-bad" is not a list`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings lack %q:\n%s", want, joined)
		}
	}

	out := rbDoc(t, res.Config)
	for _, name := range []string{"good", "empty", "orb-good"} {
		post, _ := rbEntryBody(t, out, "w", name)["post-steps"].([]any)
		if len(post) != 2 {
			t.Errorf("%s post-steps = %v, want the collect and persist steps", name, post)
		}
	}
	if pre, _ := rbEntryBody(t, out, "w", "orb-good")["pre-steps"].([]any); len(pre) != 2 || pre[0] != "checkout" {
		t.Errorf("orb-good pre-steps = %v, want checkout and the reset", pre)
	}
	// The ones that cannot take steps are as they were, and their jobs have no reset.
	if got := rbEntryBody(t, out, "w", "bad-post")["post-steps"]; got != "not a list" {
		t.Errorf("bad-post post-steps = %v", got)
	}
	if got := rbEntryBody(t, out, "w", "bad-pre")["pre-steps"]; !reflect.DeepEqual(got, map[string]any{"run": "not a list"}) {
		t.Errorf("bad-pre pre-steps = %v", got)
	}
	if got := rbEntryBody(t, out, "w", "orb-bad"); got["pre-steps"] != 3 || got["post-steps"] != nil {
		t.Errorf("orb-bad = %v", got)
	}
	for job, wantSteps := range map[string]int{"good": 2, "empty": 2, "bad-post": 1, "bad-pre": 1} {
		if steps := jobSteps(t, out, job); len(steps) != wantSteps {
			t.Errorf("job %s has %d steps, want %d (a reset only after the checkout of a traced job)", job, len(steps), wantSteps)
		}
	}
}

func TestTraceNothingToTrace(t *testing.T) {
	cfg := `version: 2.1
jobs:
  mac: {macos: {xcode: "16"}, steps: [checkout]}
  late: {docker: [{image: x}], steps: [checkout]}
workflows:
  w:
    jobs:
      - mac
      - hold:
          type: approval
      - late:
          requires: [hold]
`
	o := traceOpts()
	res := plan(t, planner.Input{Config: []byte(cfg), Map: &impactmap.Map{Version: 1}, Repo: &fakeRepo{}}, o)
	if res.Report.Trace != nil || strings.Contains(string(res.Config), "smarter-impact-map") || strings.Contains(string(res.Config), "smarter: collect trace") {
		t.Fatalf("nothing certain to run can be traced, but:\n%s", res.Config)
	}
	found := false
	for _, w := range res.Report.Warnings {
		if strings.Contains(w, "trace:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want one about the trace", res.Report.Warnings)
	}
	// A config with no traceable job at all says so too.
	res = plan(t, planner.Input{Config: []byte(strings.ReplaceAll(cfg, "      - late:\n          requires: [hold]\n", "")), Map: &impactmap.Map{Version: 1}, Repo: &fakeRepo{}}, o)
	if res.Report.Trace != nil || !strings.Contains(strings.Join(res.Report.Warnings, "\n"), "no job of the workflow can be traced") {
		t.Fatalf("warnings = %v", res.Report.Warnings)
	}
}

func TestTraceVersionAndBinaryAreQuoted(t *testing.T) {
	o := traceOpts()
	o.Version = "dev; rm -rf /"
	o.BinPath = "/opt/my tools/circleci-smarter"
	res := plan(t, traceInput(), o)
	out := string(res.Config)
	if !strings.Contains(out, "printf '%s\\n' 'dev; rm -rf /' > .smarter-version") {
		t.Errorf("the version is not quoted:\n%s", out)
	}
	if !strings.Contains(out, "'/opt/my tools/circleci-smarter' map build") {
		t.Errorf("the binary path is not quoted:\n%s", out)
	}
}

func TestTraceNameCollisionFallsBackToAFullPlanWithoutTrace(t *testing.T) {
	cfg := strings.Replace(traceConfig, "jobs:\n  build:", "jobs:\n  smarter-impact-map:\n    docker: [{image: x}]\n    steps: [checkout]\n  build:", 1)
	res := plan(t, planner.Input{Config: []byte(cfg), Map: &impactmap.Map{Version: 1}, Repo: &fakeRepo{}}, traceOpts())
	if !res.Report.Full || !strings.Contains(res.Report.FullReason, "internal error") || !strings.Contains(res.Report.FullReason, "smarter-impact-map") {
		t.Fatalf("full = %v, reason = %q", res.Report.Full, res.Report.FullReason)
	}
	if res.Report.Trace != nil || strings.Contains(string(res.Config), "smarter: collect trace") {
		t.Fatalf("the fallback must not trace:\n%s", res.Config)
	}
	if res.Report.Gate == nil {
		t.Fatal("the fallback keeps the gate")
	}
	if !strings.Contains(strings.Join(res.Report.Warnings, "\n"), "internal error, planned a full run") {
		t.Fatalf("warnings = %v", res.Report.Warnings)
	}
}

func TestTraceNeedsConfigVersion21(t *testing.T) {
	cfg := strings.Replace(traceConfig, "version: 2.1", "version: 2", 1)
	res := plan(t, planner.Input{Config: []byte(cfg), Map: &impactmap.Map{Version: 1}, Repo: &fakeRepo{}}, traceOpts())
	if res.Report.Trace != nil || strings.Contains(string(res.Config), "post-steps") {
		t.Fatalf("a version 2 config cannot take post-steps:\n%s", res.Config)
	}
	if !strings.Contains(strings.Join(res.Report.Warnings, "\n"), `the config is version "2"`) {
		t.Fatalf("warnings = %v", res.Report.Warnings)
	}
	if res.Report.Gate == nil {
		t.Fatal("the gate is still added")
	}
}
