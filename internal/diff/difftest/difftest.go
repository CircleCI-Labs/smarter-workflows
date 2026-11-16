// Package difftest builds throwaway git repositories for tests. The repos are
// hermetic: no global or system git config, no background git work, a fixed
// identity and deterministic commit dates, so the same steps produce the same
// SHAs.
package difftest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitEnv is the environment of every git process a test starts, the planner's
// included. It hides the machine's git config, and it turns off the automatic
// maintenance that git starts after a commit, a fetch or a push. Git starts
// that maintenance detached: the parent returns at once, and the child keeps
// writing into the repository (it takes objects/maintenance.lock, then removes
// it) after the test has moved on. The test removes its temp directory when it
// ends, and that removal can run while the child still writes, so it fails with
// "directory not empty". With the maintenance off, no git process outlives the
// command that started it.
var gitEnv = []string{
	"GIT_CONFIG_GLOBAL=" + os.DevNull,
	"GIT_CONFIG_SYSTEM=" + os.DevNull,
	"GIT_CONFIG_NOSYSTEM=1",
	"GIT_CONFIG_COUNT=3",
	"GIT_CONFIG_KEY_0=maintenance.auto", "GIT_CONFIG_VALUE_0=false",
	"GIT_CONFIG_KEY_1=gc.auto", "GIT_CONFIG_VALUE_1=0",
	"GIT_CONFIG_KEY_2=gc.autoDetach", "GIT_CONFIG_VALUE_2=false",
}

// Repo is a git repository in a test temp directory.
type Repo struct {
	t       testing.TB
	Dir     string
	commits int
}

// InstallGitEnv configures the test process so every git invocation is hermetic
// (no global config, maintenance off). Call before git or planner work on a tree.
func InstallGitEnv(t testing.TB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	for _, kv := range gitEnv {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
}

// commitAuthorEnv is the author identity for deterministic test commits.
var commitAuthorEnv = []string{
	"GIT_AUTHOR_NAME=Test Author", "GIT_AUTHOR_EMAIL=author@example.com",
	"GIT_COMMITTER_NAME=Test Author", "GIT_COMMITTER_EMAIL=author@example.com",
}

// CommitFileInDir stages one path and commits it with the test author identity.
func CommitFileInDir(t testing.TB, dir, rel, message string) string {
	t.Helper()
	RunGit(t, dir, nil, "add", rel)
	RunGit(t, dir, commitAuthorEnv, "commit", "-m", message)
	return RunGit(t, dir, nil, "rev-parse", "HEAD")
}

// RunGitQuiet runs git in dir and ignores errors (test cleanup).
func RunGitQuiet(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), gitEnv...), "GIT_TERMINAL_PROMPT=0", "GIT_LITERAL_PATHSPECS=1")
	_ = cmd.Run()
}

// New makes an empty repository whose default branch is main. It sets gitEnv
// for the rest of the test, so the planner's own git calls are hermetic too.
func New(t testing.TB) *Repo {
	t.Helper()
	InstallGitEnv(t)
	r := &Repo{t: t, Dir: t.TempDir()}
	r.Git("init", "-q", "-b", "main")
	return r
}

// Git runs git in the repository and fails the test on error. It returns the
// trimmed standard output.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	return RunGit(r.t, r.Dir, nil, args...)
}

// RunGit runs git in dir with extra environment variables. It always adds
// gitEnv, so a command outside New is hermetic too.
func RunGit(t testing.TB, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), gitEnv...), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Write creates or replaces a file, making parent directories.
func (r *Repo) Write(name, content string) {
	r.t.Helper()
	full := filepath.Join(r.Dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// Remove deletes a file or directory tree.
func (r *Repo) Remove(name string) {
	r.t.Helper()
	if err := os.RemoveAll(filepath.Join(r.Dir, filepath.FromSlash(name))); err != nil {
		r.t.Fatal(err)
	}
}

// Commit stages everything and commits it. It returns the new commit's SHA.
func (r *Repo) Commit(message string) string {
	r.t.Helper()
	r.commits++
	date := fmt.Sprintf("2026-01-01T00:%02d:%02d+00:00", (r.commits/60)%60, r.commits%60)
	env := append(slices.Clone(commitAuthorEnv),
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	RunGit(r.t, r.Dir, nil, "add", "-A")
	RunGit(r.t, r.Dir, env, "commit", "-q", "--allow-empty", "-m", message)
	return r.Git("rev-parse", "HEAD")
}
