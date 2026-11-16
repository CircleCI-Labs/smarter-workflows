package history

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
)

// Repo reads commit metadata from a local git clone.
type Repo struct {
	Dir string
}

func (r Repo) defaultBranch(ctx context.Context) string {
	out, err := r.git(ctx, "symbolic-ref", "-q", "refs/remotes/origin/HEAD")
	if err != nil {
		return "main"
	}
	out = strings.TrimSpace(out)
	if i := strings.LastIndex(out, "/"); i >= 0 {
		return out[i+1:]
	}
	return "main"
}

func (r Repo) mergeBase(ctx context.Context, commit string) string {
	def := r.defaultBranch(ctx)
	out, err := r.git(ctx, "merge-base", "origin/"+def, commit)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (r Repo) commitExists(ctx context.Context, sha string) bool {
	_, err := r.git(ctx, "rev-parse", "-q", "--verify", sha+"^{commit}")
	return err == nil
}

// changedFiles lists path changes between base and commit (shell fetch-history semantics).
func (r Repo) changedFiles(ctx context.Context, base, commit string) ([]backtest.Change, error) {
	if base == "" {
		return nil, nil
	}
	out, err := r.git(ctx, "diff", "--name-status", base, commit)
	if err != nil {
		return nil, err
	}
	var changes []backtest.Change
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		st, filePath := parts[0], parts[1]
		switch {
		case strings.HasPrefix(st, "R"):
			changes = append(changes, backtest.Change{Path: filePath, Status: "M"})
		case st == "A" || st == "M" || st == "D":
			changes = append(changes, backtest.Change{Path: filePath, Status: st})
		}
	}
	return changes, nil
}

func (r Repo) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
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
