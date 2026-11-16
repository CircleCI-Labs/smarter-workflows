package history

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
)

// Repo resolves pipeline-time diff bases and changed files from a local clone.
type Repo struct {
	G diff.Git

	mu            sync.Mutex
	defaultBranch string
	fpOnce        sync.Once
	fpChain       []string
	fpErr         error
}

func (r *Repo) defaultBranchName(ctx context.Context) (string, error) {
	if r.defaultBranch != "" {
		return r.defaultBranch, nil
	}
	out, err := r.runGit(ctx, "symbolic-ref", "-q", "refs/remotes/origin/HEAD")
	if err != nil {
		r.defaultBranch = "main"
		return r.defaultBranch, nil
	}
	out = strings.TrimSpace(out)
	if i := strings.LastIndex(out, "/"); i >= 0 {
		r.defaultBranch = out[i+1:]
	} else {
		r.defaultBranch = "main"
	}
	return r.defaultBranch, nil
}

func (r *Repo) firstParentChain(ctx context.Context) ([]string, error) {
	r.fpOnce.Do(func() {
		def, err := r.defaultBranchName(ctx)
		if err != nil {
			r.fpErr = err
			return
		}
		out, err := r.runGit(ctx, "rev-list", "--first-parent", "origin/"+def)
		if err != nil {
			r.fpErr = err
			return
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		for _, ln := range lines {
			ln = strings.TrimSpace(ln)
			if ln != "" {
				r.fpChain = append(r.fpChain, ln)
			}
		}
	})
	return r.fpChain, r.fpErr
}

// historicalBase picks the diff base as of the pipeline commit (see docs/benchmarks-backtest.md).
func (r *Repo) historicalBase(ctx context.Context, commit string) (string, error) {
	full, err := r.resolveSHA(ctx, commit)
	if err != nil {
		return "", err
	}
	chain, err := r.firstParentChain(ctx)
	if err != nil || len(chain) == 0 {
		return "", nil
	}
	for i, c := range chain {
		if c == full {
			if i+1 < len(chain) {
				return chain[i+1], nil
			}
			return "", nil
		}
	}
	lo, hi := 0, len(chain)-1
	best := -1
	for lo <= hi {
		mid := (lo + hi) / 2
		p := chain[mid]
		if p == full {
			lo = mid + 1
			continue
		}
		if r.isAncestor(ctx, p, full) {
			best = mid
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	if best < 0 {
		return "", nil
	}
	return chain[best], nil
}

func (r *Repo) isAncestor(ctx context.Context, older, newer string) bool {
	err := r.runGitOK(ctx, "merge-base", "--is-ancestor", older, newer)
	return err == nil
}

func (r *Repo) commitExists(ctx context.Context, sha string) bool {
	if !validCommitSHA(sha) {
		return false
	}
	_, err := r.G.ResolveCommit(ctx, sha)
	return err == nil
}

func (r *Repo) resolveSHA(ctx context.Context, sha string) (string, error) {
	if !validCommitSHA(sha) {
		return "", fmt.Errorf("invalid commit sha %q", shortSHA(sha))
	}
	return r.G.ResolveCommit(ctx, sha)
}

func (r *Repo) changedFiles(ctx context.Context, base, commit string) ([]backtest.Change, error) {
	if base == "" {
		return []backtest.Change{}, nil
	}
	if !validCommitSHA(base) || !validCommitSHA(commit) {
		return []backtest.Change{}, nil
	}
	ch, err := r.G.Changes(ctx, base, commit)
	if err != nil {
		return nil, err
	}
	out := make([]backtest.Change, len(ch))
	for i, c := range ch {
		out[i] = backtest.Change{Path: c.Path, Status: c.Status}
	}
	return out, nil
}

func validCommitSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

func (r *Repo) runGit(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.G.Dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_LITERAL_PATHSPECS=1",
		"GIT_OPTIONAL_LOCKS=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return string(out), nil
}

func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

func (r *Repo) runGitOK(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.G.Dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_LITERAL_PATHSPECS=1",
		"GIT_OPTIONAL_LOCKS=0",
	)
	return cmd.Run()
}
