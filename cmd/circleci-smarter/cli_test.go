package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/diff/difftest"
	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
	"github.com/CircleCI-Labs/smarter-workflows/internal/report"
)

// cleanEnv keeps the tests independent of the CI they may run in.
func cleanEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"CIRCLE_BRANCH", "CIRCLE_TAG", "CIRCLE_SHA1", "CIRCLE_PROJECT_ID", "CIRCLE_BUILD_NUM", "CIRCLE_REPOSITORY_URL", "CIRCLE_PULL_REQUEST"} {
		t.Setenv(k, "")
	}
}

const cliConfig = `version: 2.1
jobs:
  ta:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - run: echo a
  tb:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - run: echo b
workflows:
  w:
    jobs:
      - ta
      - tb
`

// cliRepo is a checkout on branch "feature", forked from main, with origin.
type cliRepo struct {
	*difftest.Repo
	base string
	dir  string // scratch directory for outputs
}

func newCLIRepo(t *testing.T) *cliRepo {
	t.Helper()
	cleanEnv(t)
	r := difftest.New(t)
	r.Write(".circleci/continue.yml", cliConfig)
	r.Write("a.txt", "a\n")
	r.Write("b.txt", "b\n")
	base := r.Commit("base")
	remote := filepath.Join(t.TempDir(), "origin.git")
	difftest.RunGit(t, r.Dir, nil, "init", "-q", "--bare", remote)
	r.Git("remote", "add", "origin", remote)
	r.Git("push", "-q", "origin", "main")
	r.Git("checkout", "-q", "-b", "feature")
	t.Chdir(r.Dir)
	return &cliRepo{Repo: r, base: base, dir: t.TempDir()}
}

func (c *cliRepo) path(name string) string { return filepath.Join(c.dir, name) }

func (c *cliRepo) writeMap(t *testing.T, jobs map[string][]string) {
	t.Helper()
	m := &impactmap.Map{Version: 1, Revision: c.base, CreatedAt: "2026-11-12T18:30:00Z", Method: "atime", Jobs: map[string]*impactmap.Job{}}
	for name, files := range jobs {
		m.Jobs[name] = &impactmap.Job{Files: files, Complete: true, Nodes: 1}
	}
	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.path("map.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (c *cliRepo) planArgs(extra ...string) []string {
	return append([]string{
		"--config", ".circleci/continue.yml", "--map", c.path("map.json"),
		"--out", c.path("continue.yml"), "--report-dir", c.path("plan"),
		"--branch", "feature", "--mode", "select",
	}, extra...)
}

func run(t *testing.T, fn func([]string, *bytes.Buffer, *bytes.Buffer) int, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = fn(args, &out, &errb)
	return code, out.String(), errb.String()
}

func doPlan(t *testing.T, args ...string) (int, string, string) {
	return run(t, func(a []string, o, e *bytes.Buffer) int { return runPlan(a, o, e) }, args...)
}

func doBase(t *testing.T, args ...string) (int, string, string) {
	return run(t, func(a []string, o, e *bytes.Buffer) int { return runBase(a, o, e) }, args...)
}

func doMap(t *testing.T, args ...string) (int, string, string) {
	return run(t, func(a []string, o, e *bytes.Buffer) int { return runMap(a, o, e) }, args...)
}

func readReport(t *testing.T, c *cliRepo) *report.Plan {
	t.Helper()
	b, err := os.ReadFile(c.path("plan/plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p report.Plan
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func TestBaseCommand(t *testing.T) {
	c := newCLIRepo(t)
	c.Write("a.txt", "changed\n")
	c.Commit("on the branch")

	code, out, errOut := doBase(t, "--branch", "feature")
	if code != 0 || strings.TrimSpace(out) != c.base || errOut != "" {
		t.Fatalf("base = %d, %q, %q; want %s", code, out, errOut, c.base)
	}

	// $CIRCLE_BRANCH is the default for --branch.
	t.Setenv("CIRCLE_BRANCH", "main")
	code, out, errOut = doBase(t)
	if code != 0 || out != "" || !strings.Contains(errOut, "default branch main") {
		t.Fatalf("on the default branch: %d, %q, %q", code, out, errOut)
	}
	t.Setenv("CIRCLE_BRANCH", "feature")
	if code, out, _ = doBase(t); code != 0 || strings.TrimSpace(out) != c.base {
		t.Fatalf("branch from the environment: %d, %q", code, out)
	}
	// An explicit --branch beats the environment.
	if code, out, _ = doBase(t, "--branch", "main"); code != 0 || out != "" {
		t.Fatalf("explicit branch: %d, %q", code, out)
	}
	// A tag means a full run.
	t.Setenv("CIRCLE_TAG", "v1.0.0")
	if code, out, errOut = doBase(t); code != 0 || out != "" || !strings.Contains(errOut, "tag v1.0.0") {
		t.Fatalf("on a tag: %d, %q, %q", code, out, errOut)
	}
	t.Setenv("CIRCLE_TAG", "")
	// Another default branch name.
	if code, out, errOut = doBase(t, "--default-branch", "trunk"); code != 0 || out != "" || !strings.Contains(errOut, "origin/trunk") {
		t.Fatalf("unknown default branch: %d, %q, %q", code, out, errOut)
	}
}

func TestBaseCommandUsage(t *testing.T) {
	cleanEnv(t)
	if code, _, errOut := doBase(t, "--nope"); code != 2 || !strings.Contains(errOut, "flag provided but not defined") {
		t.Fatalf("bad flag: %d, %q", code, errOut)
	}
	if code, _, _ := doBase(t, "extra"); code != 2 {
		t.Fatalf("an argument: %d", code)
	}
	if code, _, errOut := doBase(t, "-h"); code != 0 || !strings.Contains(errOut, "usage: circleci-smarter base") {
		t.Fatalf("-h: %d, %q", code, errOut)
	}
	// Not a repository: no base, exit 0.
	t.Chdir(t.TempDir())
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(t.TempDir()))
	if code, out, _ := doBase(t, "--branch", "x"); code != 0 || out != "" {
		t.Fatalf("outside a repository: %d, %q", code, out)
	}
}

func TestMapBuildCommand(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("aaaa.0.tsv", "# smarter-trace v1\njob\tlint\nnode\t0\nmethod\tatime\nsupported\ttrue\nF\ta.go\nD\t.\n")
	write("aaaa.1.tsv", "# smarter-trace v1\njob\tlint\nnode\t1\nmethod\tatime\nsupported\ttrue\nF\tb.go\n")
	write("bbbb.0.tsv", "# smarter-trace v1\njob\tdocs\nnode\t0\nmethod\tatime\nsupported\tfalse\nreason\tnoatime\n")
	out := filepath.Join(t.TempDir(), "nested", "impact-map.json")

	code, stdout, stderr := doMap(t, "build", "--traces", dir, "--revision", "abc123", "--out", out)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "3 traces, 2 jobs (1 complete)") {
		t.Fatalf("map build = %d, %q, %q", code, stdout, stderr)
	}
	m, err := impactmap.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if m.Revision != "abc123" || m.Method != "atime" || m.CreatedAt == "" {
		t.Fatalf("map = %+v", m)
	}
	lint := m.Jobs["lint"]
	if !lint.Complete || lint.Nodes != 2 || !lint.ReadsFile("a.go") || !lint.ReadsFile("b.go") || !lint.ListsDir(".") {
		t.Fatalf("lint = %+v", lint)
	}
	if m.Jobs["docs"].Complete {
		t.Fatalf("docs = %+v", m.Jobs["docs"])
	}
}

func TestMapBuildCommandErrors(t *testing.T) {
	empty := t.TempDir()
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "x.tsv"), []byte("not a trace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "m.json")
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no subcommand", nil, 2, "usage: circleci-smarter map build"},
		{"unknown subcommand", []string{"merge"}, 2, "usage: circleci-smarter map build"},
		{"help", []string{"--help"}, 0, "usage: circleci-smarter map build"},
		{"missing traces", []string{"build", "--revision", "r", "--out", out}, 2, "--traces is required"},
		{"missing revision", []string{"build", "--traces", empty, "--out", out}, 2, "--revision is required"},
		{"missing out", []string{"build", "--traces", empty, "--revision", "r"}, 2, "--out is required"},
		{"a stray argument", []string{"build", "--traces", empty, "--revision", "r", "--out", out, "x"}, 2, "unexpected argument"},
		{"a bad flag", []string{"build", "--wat"}, 2, "not defined"},
		{"no traces", []string{"build", "--traces", empty, "--revision", "r", "--out", out}, 1, "no traces"},
		{"a bad trace", []string{"build", "--traces", bad, "--revision", "r", "--out", out}, 1, "x.tsv"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := doMap(t, tc.args...)
			if code != tc.code || !strings.Contains(stderr, tc.want) {
				t.Fatalf("= %d, %q; want %d, %q", code, stderr, tc.code, tc.want)
			}
			if _, err := os.Stat(out); err == nil {
				t.Fatal("no map may be written when the build fails")
			}
		})
	}
}

func TestPlanCommandSelectsFromTheDiff(t *testing.T) {
	c := newCLIRepo(t)
	c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
	c.Write("a.txt", "changed\n")
	head := c.Commit("change a")

	code, stdout, stderr := doPlan(t, c.planArgs()...)
	if code != 0 {
		t.Fatalf("plan = %d, %q", code, stderr)
	}
	if !strings.Contains(stdout, "select mode, 1 of 2 jobs run") || !strings.Contains(stdout, c.path("continue.yml")) {
		t.Fatalf("stdout = %q", stdout)
	}
	rep := readReport(t, c)
	if rep.Full || rep.Base != c.base || rep.Head != head || rep.Branch != "feature" || rep.MapRevision != c.base {
		t.Fatalf("report = %+v", rep)
	}
	if len(rep.Changed) != 1 || rep.Changed[0].Path != "a.txt" || rep.Changed[0].Status != "M" {
		t.Fatalf("changed = %+v", rep.Changed)
	}
	if len(rep.Jobs) != 2 || !rep.Jobs[0].Run || rep.Jobs[1].Run || rep.Jobs[0].Reasons[0] != "reads a.txt" {
		t.Fatalf("jobs = %+v", rep.Jobs)
	}
	cfg, err := os.ReadFile(c.path("continue.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), "- tb") || !strings.Contains(string(cfg), "smarter-gate") {
		t.Fatalf("config:\n%s", cfg)
	}
	md, err := os.ReadFile(c.path("plan/plan.md"))
	if err != nil || !strings.Contains(string(md), "select mode") {
		t.Fatalf("plan.md: %v %q", err, md)
	}
}

func TestPlanCommandDefaultsToShadow(t *testing.T) {
	c := newCLIRepo(t)
	c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
	c.Write("a.txt", "changed\n")
	c.Commit("change a")
	args := c.planArgs()
	for i, a := range args {
		if a == "--mode" {
			args = append(args[:i], args[i+2:]...) // the default is shadow
			break
		}
	}
	if code, _, stderr := doPlan(t, args...); code != 0 {
		t.Fatalf("plan = %d, %q", code, stderr)
	}
	rep := readReport(t, c)
	if rep.Mode != "shadow" || rep.Jobs[1].Run {
		t.Fatalf("report = %+v", rep)
	}
	cfg, _ := os.ReadFile(c.path("continue.yml"))
	if !strings.Contains(string(cfg), "- tb") {
		t.Fatalf("shadow mode must keep every job:\n%s", cfg)
	}
}

func TestPlanCommandTakesBranchAndTagFromTheEnvironment(t *testing.T) {
	c := newCLIRepo(t)
	c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
	c.Write("a.txt", "changed\n")
	c.Commit("change a")
	t.Setenv("CIRCLE_BRANCH", "main")
	args := []string{"--config", ".circleci/continue.yml", "--map", c.path("map.json"), "--out", c.path("continue.yml"), "--report-dir", c.path("plan"), "--mode", "select"}
	if code, _, stderr := doPlan(t, args...); code != 0 {
		t.Fatalf("plan = %d, %q", code, stderr)
	}
	if rep := readReport(t, c); !rep.Full || !strings.Contains(rep.FullReason, "default branch main") {
		t.Fatalf("report = %+v", rep)
	}
	t.Setenv("CIRCLE_BRANCH", "")
	t.Setenv("CIRCLE_TAG", "v2.0.0")
	if code, _, stderr := doPlan(t, args...); code != 0 {
		t.Fatalf("plan = %d, %q", code, stderr)
	}
	if rep := readReport(t, c); !rep.Full || !strings.Contains(rep.FullReason, "tag v2.0.0") {
		t.Fatalf("report = %+v", rep)
	}
}

func TestPlanCommandFullRuns(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, c *cliRepo) []string
		want  string
	}{
		{"no map", func(t *testing.T, c *cliRepo) []string { return c.planArgs("--map", c.path("missing.json")) }, "there is no impact map at"},
		{"an invalid map", func(t *testing.T, c *cliRepo) []string {
			if err := os.WriteFile(c.path("map.json"), []byte(`{"version":9}`), 0o644); err != nil {
				t.Fatal(err)
			}
			return c.planArgs()
		}, "unsupported version 9"},
		{"the pipeline definition changed", func(t *testing.T, c *cliRepo) []string {
			c.Write(".circleci/continue.yml", cliConfig+"# touched\n")
			c.Commit("touch the pipeline")
			return c.planArgs()
		}, "matches the full-run path .circleci/**"},
		{"forced", func(t *testing.T, c *cliRepo) []string { return c.planArgs("--force-full") }, "forced by --force-full"},
		{"a scheduled pipeline", func(t *testing.T, c *cliRepo) []string { return c.planArgs("--trigger-type", "schedule") }, "scheduled pipeline"},
		{"the commit asks for it", func(t *testing.T, c *cliRepo) []string {
			c.Write("a.txt", "x\n")
			c.Commit("tweak [smarter full]")
			return c.planArgs()
		}, "the head commit asks for a full run"},
		{"no base", func(t *testing.T, c *cliRepo) []string { return c.planArgs("--default-branch", "trunk") }, "no diff base"},
		{"an unknown --base", func(t *testing.T, c *cliRepo) []string { return c.planArgs("--base", "no-such-rev") }, `--base "no-such-rev" cannot be resolved`},
		{"full-run paths given with braces", func(t *testing.T, c *cliRepo) []string {
			c.Write("a.txt", "x\n")
			c.Commit("a")
			return c.planArgs("--full-run-paths", "**/*.{txt,md}")
		}, "matches the full-run path **/*.{txt,md}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newCLIRepo(t)
			c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
			args := tc.setup(t, c)
			code, _, stderr := doPlan(t, args...)
			if code != 0 {
				t.Fatalf("plan = %d, %q: a full run is not an error", code, stderr)
			}
			if rep := readReport(t, c); !rep.Full || !strings.Contains(rep.FullReason, tc.want) {
				t.Fatalf("full = %v, reason = %q, want %q", rep.Full, rep.FullReason, tc.want)
			}
			if cfg, err := os.ReadFile(c.path("continue.yml")); err != nil || !strings.Contains(string(cfg), "- tb") {
				t.Fatalf("a full plan keeps every job: %v\n%s", err, cfg)
			}
		})
	}
}

func TestPlanCommandTracesFullRunsOnTheDefaultBranch(t *testing.T) {
	c := newCLIRepo(t)
	c.Git("checkout", "-q", "main")
	bin := "/opt/smarter/circleci-smarter"
	args := c.planArgs("--branch", "main", "--smarter-bin", bin)
	if code, _, stderr := doPlan(t, args...); code != 0 {
		t.Fatalf("plan = %d, %q", code, stderr)
	}
	cfg, _ := os.ReadFile(c.path("continue.yml"))
	for _, want := range []string{"smarter-impact-map", "smarter: collect trace", "smarter: reset atime", bin + " map build"} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("the traced config lacks %q:\n%s", want, cfg)
		}
	}
	// The reset is a setting.
	args = c.planArgs("--branch", "main", "--smarter-bin", bin, "--trace-reset", "off")
	if code, _, stderr := doPlan(t, args...); code != 0 {
		t.Fatalf("plan = %d, %q", code, stderr)
	}
	cfg, _ = os.ReadFile(c.path("continue.yml"))
	if strings.Contains(string(cfg), "reset atime") || !strings.Contains(string(cfg), "smarter: collect trace") {
		t.Fatalf("--trace-reset off:\n%s", cfg)
	}
	args = c.planArgs("--branch", "main", "--trace", "off")
	if code, _, _ := doPlan(t, args...); code != 0 {
		t.Fatal("plan failed")
	}
	cfg, _ = os.ReadFile(c.path("continue.yml"))
	if strings.Contains(string(cfg), "smarter-impact-map") {
		t.Fatalf("--trace off:\n%s", cfg)
	}
	// By default the map job runs the binary this process runs from.
	args = c.planArgs("--branch", "main")
	if code, _, _ := doPlan(t, args...); code != 0 {
		t.Fatal("plan failed")
	}
	cfg, _ = os.ReadFile(c.path("continue.yml"))
	exe, _ := os.Executable()
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if !strings.Contains(string(cfg), exe+" map build") && !strings.Contains(string(cfg), "'"+exe+"' map build") {
		t.Fatalf("the map job must run %s:\n%s", exe, cfg)
	}
}

func TestPlanCommandExitCodes(t *testing.T) {
	c := newCLIRepo(t)
	c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"an unreadable config", c.planArgs("--config", c.path("nope.yml")), 1, "cannot read the config"},
		{"a config that is not yaml", func() []string {
			p := c.path("bad.yml")
			os.WriteFile(p, []byte("a: ["), 0o644)
			return c.planArgs("--config", p)
		}(), 1, "parse config"},
		{"an unwritable report dir", c.planArgs("--report-dir", filepath.Join(c.path("map.json"), "plan")), 1, "cannot write the report"},
		{"a bad flag", c.planArgs("--nope"), 2, "not defined"},
		{"a stray argument", c.planArgs("extra"), 2, "unexpected argument"},
		{"a bad mode", c.planArgs("--mode", "fast"), 2, "mode must be shadow or select"},
		{"a bad trace", c.planArgs("--trace", "sometimes"), 2, "trace must be"},
		{"a bad trace-reset", c.planArgs("--trace-reset", "maybe"), 2, "--trace-reset must be on or off"},
		{"a bad glob", c.planArgs("--ignore-paths", "["), 2, "ignore paths"},
		{"bad extra inputs", c.planArgs("--extra-inputs", "nojob"), 2, "--extra-inputs"},
		{"an unknown workflow", c.planArgs("--workflow", "zzz"), 2, `no workflow "zzz"`},
		{"help", []string{"-h"}, 0, "Usage of plan"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := doPlan(t, tc.args...)
			if code != tc.code || !strings.Contains(stderr, tc.want) {
				t.Fatalf("= %d, %q; want %d, %q", code, stderr, tc.code, tc.want)
			}
		})
	}
}

func TestPlanCommandSeveralWorkflowsNeedAName(t *testing.T) {
	c := newCLIRepo(t)
	c.Write(".circleci/continue.yml", cliConfig+"  other:\n    jobs:\n      - ta\n")
	c.Commit("two workflows")
	c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
	if code, _, stderr := doPlan(t, c.planArgs("--force-full")...); code != 2 || !strings.Contains(stderr, "2 workflows (w, other)") {
		t.Fatalf("= %d, %q", code, stderr)
	}
	if code, _, stderr := doPlan(t, c.planArgs("--force-full", "--workflow", "w")...); code != 0 {
		t.Fatalf("= %d, %q", code, stderr)
	}
}

func TestPlanCommandExtraInputsAndMandatory(t *testing.T) {
	c := newCLIRepo(t)
	c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
	c.Write("infra/main.tf", "x\n")
	c.Commit("infra")
	args := c.planArgs("--extra-inputs", "tb=infra/**|other/**", "--mandatory", "ta")
	if code, _, stderr := doPlan(t, args...); code != 0 {
		t.Fatalf("= %d, %q", code, stderr)
	}
	rep := readReport(t, c)
	if !rep.Jobs[0].Run || !rep.Jobs[1].Run || rep.Jobs[0].Reasons[0] != "mandatory job" || !strings.Contains(rep.Jobs[1].Reasons[0], "extra input infra/** matches infra/main.tf") {
		t.Fatalf("jobs = %+v", rep.Jobs)
	}
}

// fakeBundle is a guardrail checker for the command tests.
type fakeBundle struct {
	result guard.Result
	err    error
	meta   map[string]any
	dir    string
}

func (f *fakeBundle) Check(context.Context, []byte) (guard.Result, error) { return f.result, f.err }

func withGuardrails(t *testing.T, f func(dir string, meta map[string]any) (guard.Checker, error)) {
	t.Helper()
	old := loadGuardrails
	loadGuardrails = f
	t.Cleanup(func() { loadGuardrails = old })
}

func TestPlanCommandGuardrails(t *testing.T) {
	newRepo := func(t *testing.T) *cliRepo {
		c := newCLIRepo(t)
		c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
		c.Write("a.txt", "changed\n")
		c.Commit("change a")
		return c
	}
	t.Run("a bundle with no policy support built in is a full plan, not an error", func(t *testing.T) {
		c := newRepo(t)
		withGuardrails(t, nil)
		code, _, stderr := doPlan(t, c.planArgs("--policy-bundle", "policies")...)
		if code != 0 {
			t.Fatalf("= %d, %q", code, stderr)
		}
		rep := readReport(t, c)
		if !rep.Full || rep.Guardrails.Status != "ERROR" || !strings.Contains(rep.FullReason, "policy support is not built into this binary") {
			t.Fatalf("report = %+v", rep)
		}
		if !strings.Contains(stderr, "warning: guardrails") {
			t.Fatalf("stderr = %q", stderr)
		}
	})
	t.Run("a bundle that cannot be loaded is a full plan", func(t *testing.T) {
		c := newRepo(t)
		withGuardrails(t, func(string, map[string]any) (guard.Checker, error) { return nil, errors.New("no such bundle") })
		code, _, _ := doPlan(t, c.planArgs("--policy-bundle", "policies")...)
		rep := readReport(t, c)
		if code != 0 || !rep.Full || rep.Guardrails.Status != "ERROR" || !strings.Contains(rep.FullReason, "cannot load the policy bundle policies: no such bundle") {
			t.Fatalf("code = %d, report = %+v", code, rep)
		}
	})
	t.Run("the bundle and its meta reach the loader, and a passing plan continues", func(t *testing.T) {
		c := newRepo(t)
		var gotDir string
		var gotMeta map[string]any
		withGuardrails(t, func(dir string, meta map[string]any) (guard.Checker, error) {
			gotDir, gotMeta = dir, meta
			return &fakeBundle{result: guard.Result{Status: guard.Pass}}, nil
		})
		t.Setenv("CIRCLE_PROJECT_ID", "proj-1")
		t.Setenv("CIRCLE_BUILD_NUM", "42")
		t.Setenv("CIRCLE_SHA1", "abc")
		code, _, stderr := doPlan(t, c.planArgs("--policy-bundle", "policies")...)
		if code != 0 {
			t.Fatalf("= %d, %q", code, stderr)
		}
		if gotDir != "policies" || gotMeta["project_id"] != "proj-1" || gotMeta["build_number"] != 42 || gotMeta["branch"] != "feature" {
			t.Fatalf("dir = %q, meta = %v", gotDir, gotMeta)
		}
		if vcs, _ := gotMeta["vcs"].(map[string]any); vcs["revision"] != "abc" || vcs["branch"] != "feature" {
			t.Fatalf("vcs = %v", gotMeta["vcs"])
		}
		if rep := readReport(t, c); rep.Guardrails.Status != "PASS" || rep.Full {
			t.Fatalf("report = %+v", rep)
		}
	})
	t.Run("--policy-meta as JSON or as a file", func(t *testing.T) {
		c := newRepo(t)
		var gotMeta map[string]any
		withGuardrails(t, func(dir string, meta map[string]any) (guard.Checker, error) {
			gotMeta = meta
			return &fakeBundle{result: guard.Result{Status: guard.Pass}}, nil
		})
		if code, _, stderr := doPlan(t, c.planArgs("--policy-bundle", "p", "--policy-meta", `{"branch":"x","n":1}`)...); code != 0 {
			t.Fatalf("= %d, %q", code, stderr)
		}
		if gotMeta["branch"] != "x" || gotMeta["n"] != float64(1) || gotMeta["vcs"] != nil {
			t.Fatalf("meta = %v", gotMeta)
		}
		metaFile := c.path("meta.json")
		os.WriteFile(metaFile, []byte(`{"project_id":"from-file"}`), 0o644)
		if code, _, _ := doPlan(t, c.planArgs("--policy-bundle", "p", "--policy-meta", metaFile)...); code != 0 || gotMeta["project_id"] != "from-file" {
			t.Fatalf("meta = %v", gotMeta)
		}
		// A bad meta is a recorded problem, not a crash.
		code, _, _ := doPlan(t, c.planArgs("--policy-bundle", "p", "--policy-meta", `{"x":`)...)
		rep := readReport(t, c)
		if code != 0 || !rep.Full || !strings.Contains(rep.FullReason, "--policy-meta is not a JSON object") {
			t.Fatalf("code = %d, report = %+v", code, rep)
		}
	})
	t.Run("a plan the guardrails reject even in full exits 3 and writes the report but not the config", func(t *testing.T) {
		c := newRepo(t)
		bundle := &fakeBundle{result: guard.Result{Status: guard.HardFail, Violations: []guard.Violation{{Policy: "org", Rule: "required_jobs", Message: "security-scan is missing", Hard: true}}}}
		withGuardrails(t, func(string, map[string]any) (guard.Checker, error) { return bundle, nil })
		code, stdout, stderr := doPlan(t, c.planArgs("--policy-bundle", "policies")...)
		if code != 3 || stdout != "" || !strings.Contains(stderr, "must not continue") || !strings.Contains(stderr, "required_jobs: security-scan is missing") {
			t.Fatalf("= %d, %q, %q", code, stdout, stderr)
		}
		if rep := readReport(t, c); rep.Guardrails.Status != "HARD_FAIL" || len(rep.Guardrails.Violations) != 1 {
			t.Fatalf("report = %+v", rep)
		}
		if _, err := os.Stat(c.path("continue.yml")); err == nil {
			t.Fatal("the config must not be written when the guardrails fail")
		}
	})
}

func TestVersionAndRegistry(t *testing.T) {
	for _, name := range []string{"base", "plan", "map", "version"} {
		if _, ok := commands[name]; !ok {
			t.Errorf("command %q is not registered", name)
		}
	}
}
