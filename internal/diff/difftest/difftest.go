// Package difftest builds throwaway git repositories for tests. The repos are
// hermetic: no global or system git config, a fixed identity and
// deterministic commit dates, so the same steps produce the same SHAs.
package difftest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Repo is a git repository in a test temp directory.
type Repo struct {
	t       testing.TB
	Dir     string
	commits int
}

// New makes an empty repository whose default branch is main.
func New(t testing.TB) *Repo {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
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

// RunGit runs git in dir with extra environment variables.
func RunGit(t testing.TB, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
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
	env := []string{
		"GIT_AUTHOR_NAME=Test Author", "GIT_AUTHOR_EMAIL=author@example.com", "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=Test Author", "GIT_COMMITTER_EMAIL=author@example.com", "GIT_COMMITTER_DATE=" + date,
	}
	RunGit(r.t, r.Dir, nil, "add", "-A")
	RunGit(r.t, r.Dir, env, "commit", "-q", "--allow-empty", "-m", message)
	return r.Git("rev-parse", "HEAD")
}
