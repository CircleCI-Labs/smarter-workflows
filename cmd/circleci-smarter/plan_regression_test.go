package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/analyze"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff/difftest"
	"github.com/CircleCI-Labs/smarter-workflows/internal/report"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && strings.Contains(string(b), "smarter-workflows") {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("module root not found")
		}
		dir = parent
	}
}

func planRegressionWorktree(t *testing.T, origin string) (dir, base string) {
	t.Helper()
	difftest.InstallGitEnv(t)
	wt := filepath.Join(t.TempDir(), "wt")
	branch := fmt.Sprintf("plan-reg-%d", os.Getpid())
	difftest.RunGit(t, origin, nil, "worktree", "add", "-b", branch, wt, "HEAD")
	t.Cleanup(func() {
		difftest.RunGitQuiet(origin, "worktree", "remove", "--force", wt)
		difftest.RunGitQuiet(origin, "branch", "-D", branch)
	})
	base = difftest.RunGit(t, wt, nil, "rev-parse", "HEAD")
	return wt, base
}

func patchFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runPlanSelect(t *testing.T, dir string, args ...string) map[string]bool {
	t.Helper()
	cleanCIEnv(t)
	t.Chdir(dir)
	outDir := filepath.Join(t.TempDir(), "plan")
	all := append([]string{
		"--config", ".circleci/continue.yml",
		"--workflow", "ci",
		"--mode", "select",
		"--branch", "feature/plan-regression",
		"--default-branch", "main",
		"--mandatory", "security-scan",
		"--full-run-paths", ".circleci/**",
		"--report-dir", outDir,
		"--out", filepath.Join(outDir, "continue.yml"),
	}, args...)
	var stdout, stderr bytes.Buffer
	code := runPlan(all, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("plan exit %d stderr=%s", code, stderr.String())
	}
	rep := readReportAt(t, outDir)
	runs := map[string]bool{}
	for _, j := range rep.Jobs {
		if j.Run {
			runs[j.Name] = true
		}
	}
	return runs
}

func runPlanDemo(t *testing.T, dir string, base, head string) map[string]bool {
	t.Helper()
	cleanCIEnv(t)
	t.Chdir(dir)
	outDir := filepath.Join(t.TempDir(), "plan")
	args := []string{
		"--repo", dir,
		"--config", "examples/demo/.circleci/continue.yml",
		"--workflow", "demo",
		"--mode", "select",
		"--branch", "feature/plan-regression",
		"--default-branch", "main",
		"--base", base,
		"--head", head,
		"--report-dir", outDir,
		"--out", filepath.Join(outDir, "continue.yml"),
	}
	var stdout, stderr bytes.Buffer
	if runPlan(args, &stdout, &stderr) != 0 {
		t.Fatalf("stderr=%s", stderr.String())
	}
	rep := readReportAt(t, outDir)
	runs := map[string]bool{}
	for _, j := range rep.Jobs {
		if j.Run {
			runs[j.Name] = true
		}
	}
	return runs
}

func readReportAt(t *testing.T, dir string) *report.Plan {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p report.Plan
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func assertGoTestSkipsPath(t *testing.T, repo, path string) {
	t.Helper()
	m, _, _, err := analyze.Run(context.Background(), analyze.RunOptions{
		RepoRoot:   repo,
		ConfigPath: filepath.Join(repo, ".circleci", "continue.yml"),
		Workflows:  []string{"ci"},
		AIMode:     analyze.AIModeOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	j := m.Jobs["go-test"]
	if j != nil && j.CoversPath(path) {
		t.Fatalf("go-test map covers %q; dirs=%v", path, j.DirPaths())
	}
}

func wantJobs(t *testing.T, runs map[string]bool, must []string, mustNot []string) {
	t.Helper()
	for _, name := range must {
		if !runs[name] {
			t.Errorf("want job %q to run; got %s", name, jobList(runs))
		}
	}
	for _, name := range mustNot {
		if runs[name] {
			t.Errorf("want job %q skipped; got %s", name, jobList(runs))
		}
	}
}

func jobList(runs map[string]bool) string {
	var names []string
	for n, on := range runs {
		if on {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func TestPlanRegressionDogfoodReleaseScript(t *testing.T) {
	root := moduleRoot(t)
	wt, base := planRegressionWorktree(t, root)
	before, err := os.ReadFile(filepath.Join(wt, "internal/report/report.go"))
	if err != nil {
		t.Fatal(err)
	}
	patchFile(t, wt, "internal/report/report.go", string(before)+"\n// plan regression\n")
	head := difftest.CommitFileInDir(t, wt, "internal/report/report.go", "touch report.go")
	runs := runPlanSelect(t, wt, "--base", base, "--head", head)
	wantJobs(t, runs, []string{"release-dry-run", "go-test", "go-lint", "security-scan"}, nil)
}

func TestPlanRegressionDogfoodGoDiff(t *testing.T) {
	root := moduleRoot(t)
	wt, base := planRegressionWorktree(t, root)
	patchFile(t, wt, "internal/diff/git.go", "// plan regression touch\n")
	head := difftest.CommitFileInDir(t, wt, "internal/diff/git.go", "touch internal/diff/git.go")
	t.Chdir(wt)
	runs := runPlanSelect(t, wt, "--base", base, "--head", head)
	wantJobs(t, runs, []string{"go-test", "go-lint", "security-scan"}, nil)
}

func TestPlanRegressionDogfoodPolicy(t *testing.T) {
	root := moduleRoot(t)
	wt, base := planRegressionWorktree(t, root)
	data, err := os.ReadFile(filepath.Join(wt, "policies/guardrails.rego"))
	if err != nil {
		t.Fatal(err)
	}
	patchFile(t, wt, "policies/guardrails.rego", string(data)+"\n# plan regression touch\n")
	head := difftest.CommitFileInDir(t, wt, "policies/guardrails.rego", "touch guardrails")
	runs := runPlanSelect(t, wt, "--base", base, "--head", head)
	wantJobs(t, runs, []string{"go-test", "policy-test", "security-scan"}, nil)
}

func TestPlanRegressionDogfoodNewPolicyFile(t *testing.T) {
	root := moduleRoot(t)
	wt, base := planRegressionWorktree(t, root)
	rel := "policies/new.rego"
	patchFile(t, wt, rel, "package smarter\n")
	head := difftest.CommitFileInDir(t, wt, rel, "new policy file")
	runs := runPlanSelect(t, wt, "--base", base, "--head", head)
	wantJobs(t, runs, []string{"go-test", "policy-test", "security-scan"}, nil)
}

func TestPlanRegressionDogfoodDocsOnly(t *testing.T) {
	root := moduleRoot(t)
	wt, base := planRegressionWorktree(t, root)
	rel := "docs/learn.md"
	before, err := os.ReadFile(filepath.Join(wt, rel))
	if err != nil {
		t.Fatal(err)
	}
	patchFile(t, wt, rel, string(before)+"\n")
	head := difftest.CommitFileInDir(t, wt, rel, "docs only")
	assertGoTestSkipsPath(t, wt, rel)
	runs := runPlanSelect(t, wt, "--base", base, "--head", head)
	wantJobs(t, runs, []string{"docs-site-build", "go-lint", "security-scan"}, []string{"go-test"})
}

func TestPlanRegressionDemoWeb(t *testing.T) {
	root := moduleRoot(t)
	wt, base := planRegressionWorktree(t, root)
	patchFile(t, wt, "examples/demo/services/web/src/greeting.js", "export function greeting(){return 'hi';}\n")
	head := difftest.CommitFileInDir(t, wt, "examples/demo/services/web/src/greeting.js", "demo web")
	runs := runPlanDemo(t, wt, base, head)
	wantJobs(t, runs, []string{"web-test", "web-bundle"}, nil)
}

func TestPlanRegressionDogfoodGoDeepNewFile(t *testing.T) {
	root := moduleRoot(t)
	wt, base := planRegressionWorktree(t, root)
	rel := "internal/report/plan_regression_new.go"
	patchFile(t, wt, rel, "package report\n")
	head := difftest.CommitFileInDir(t, wt, rel, "new go file under input dir")
	runs := runPlanSelect(t, wt, "--base", base, "--head", head)
	wantJobs(t, runs, []string{"go-test", "go-lint", "security-scan"}, nil)
}

func TestPlanRegressionDogfoodRootPlusGoLint(t *testing.T) {
	root := moduleRoot(t)
	wt, base := planRegressionWorktree(t, root)
	patchFile(t, wt, "orb/src/@orb.yml", "# plan regression touch\n")
	head := difftest.CommitFileInDir(t, wt, "orb/src/@orb.yml", "touch orb metadata")
	runs := runPlanSelect(t, wt, "--base", base, "--head", head)
	wantJobs(t, runs, []string{"go-lint", "security-scan"}, nil)
}

func TestPlanRegressionDemoDocs(t *testing.T) {
	root := moduleRoot(t)
	wt, base := planRegressionWorktree(t, root)
	patchFile(t, wt, "examples/demo/docs/plan-regression.md", "# scratch\n")
	head := difftest.CommitFileInDir(t, wt, "examples/demo/docs/plan-regression.md", "demo docs")
	runs := runPlanDemo(t, wt, base, head)
	wantJobs(t, runs, []string{"docs-check"}, nil)
}
