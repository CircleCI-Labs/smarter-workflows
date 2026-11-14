// Package diff answers the planner's questions about a git checkout: which
// commit a branch forked from, which files changed, whether a directory exists
// at a revision, and what the head commit message says. Everything shells out
// to git, with prompts and pathspec magic turned off.
package diff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Change is one changed path. A rename is reported as two changes: the old path
// deleted and the new path added. Type changes and the like count as modified.
type Change struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "A", "M" or "D"
}

// Git runs git commands in Dir.
type Git struct {
	// Dir is the working directory ("" means the current directory).
	Dir string
	// Timeout bounds each git command (default 2 minutes).
	Timeout time.Duration
}

var credentialsInURL = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s@]+@`)

func (g Git) run(ctx context.Context, args ...string) (string, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",   // never wait for a password
		"GIT_LITERAL_PATHSPECS=1", // paths are paths, not globs
		"GIT_OPTIONAL_LOCKS=0",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		msg = credentialsInURL.ReplaceAllString(msg, "${1}***@")
		if msg == "" {
			return "", fmt.Errorf("git %s: %w", args[0], err)
		}
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return stdout.String(), nil
}

// checkRev rejects revision strings that git could read as options.
func checkRev(rev string) error {
	if rev == "" || strings.HasPrefix(rev, "-") || strings.ContainsAny(rev, "\x00\n") {
		return fmt.Errorf("bad revision %q", rev)
	}
	return nil
}

// ResolveCommit returns the full SHA of the commit rev names.
func (g Git) ResolveCommit(ctx context.Context, rev string) (string, error) {
	if err := checkRev(rev); err != nil {
		return "", err
	}
	out, err := g.run(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("cannot resolve revision %q: %w", rev, err)
	}
	return strings.TrimSpace(out), nil
}

// BaseResult is the outcome of Base. SHA is empty when the branch gets a full
// run, and Why then says why (it is meant for reports, not for parsing).
type BaseResult struct {
	SHA string
	Why string
}

// Base resolves the diff base: `git merge-base origin/<default> HEAD`. It
// fetches origin/<default> first when that ref is missing. On the default
// branch, on a tag, or on any git failure there is no base, and the branch gets
// a full run.
func (g Git) Base(ctx context.Context, defaultBranch, branch, tag string) BaseResult {
	if tag != "" {
		return BaseResult{Why: "tag " + tag}
	}
	if defaultBranch == "" {
		return BaseResult{Why: "no default branch given"}
	}
	if branch != "" && branch == defaultBranch {
		return BaseResult{Why: "default branch " + branch}
	}
	if err := checkRev(defaultBranch); err != nil {
		return BaseResult{Why: err.Error()}
	}
	ref := "refs/remotes/origin/" + defaultBranch
	if _, err := g.run(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		spec := "+refs/heads/" + defaultBranch + ":" + ref
		if _, ferr := g.run(ctx, "fetch", "--quiet", "origin", spec); ferr != nil {
			return BaseResult{Why: fmt.Sprintf("origin/%s is missing and could not be fetched: %v", defaultBranch, ferr)}
		}
	}
	out, err := g.run(ctx, "merge-base", ref, "HEAD")
	if err != nil {
		return BaseResult{Why: fmt.Sprintf("no merge base with origin/%s: %v", defaultBranch, err)}
	}
	sha := strings.TrimSpace(out)
	if sha == "" {
		return BaseResult{Why: fmt.Sprintf("no merge base with origin/%s", defaultBranch)}
	}
	return BaseResult{SHA: sha}
}

// Changes lists the paths that differ between base and head, from
// `git diff --name-status -M -z`.
func (g Git) Changes(ctx context.Context, base, head string) ([]Change, error) {
	if err := checkRev(base); err != nil {
		return nil, err
	}
	if err := checkRev(head); err != nil {
		return nil, err
	}
	out, err := g.run(ctx, "diff", "--name-status", "-M", "-z", "--no-ext-diff", base, head, "--")
	if err != nil {
		return nil, err
	}
	return ParseNameStatus(out)
}

// ParseNameStatus parses `git diff --name-status -z` output.
func ParseNameStatus(out string) ([]Change, error) {
	if out == "" {
		return nil, nil
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	var changes []Change
	for i := 0; i < len(fields); {
		status := fields[i]
		i++
		if status == "" {
			return nil, errors.New("diff: empty status field")
		}
		switch status[0] {
		case 'R', 'C':
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("diff: %s entry is missing a path", status)
			}
			oldPath, newPath := fields[i], fields[i+1]
			i += 2
			if status[0] == 'R' {
				changes = append(changes, Change{Path: oldPath, Status: "D"})
			}
			changes = append(changes, Change{Path: newPath, Status: "A"})
		default:
			if i >= len(fields) {
				return nil, fmt.Errorf("diff: %s entry is missing a path", status)
			}
			path := fields[i]
			i++
			switch status[0] {
			case 'A':
				changes = append(changes, Change{Path: path, Status: "A"})
			case 'D':
				changes = append(changes, Change{Path: path, Status: "D"})
			default: // M, T, U, X, B: the content or type changed
				changes = append(changes, Change{Path: path, Status: "M"})
			}
		}
	}
	return changes, nil
}

// DirsExist reports, for each directory (repo-relative, "." is the root),
// whether it exists as a tree at rev. It uses `git ls-tree`.
//
// ls-tree does not print a tree when another pathspec lies below it (it
// recurses instead), so the directories are queried one depth at a time: the
// pathspecs of one call never nest.
func (g Git) DirsExist(ctx context.Context, rev string, dirs []string) (map[string]bool, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	exists := make(map[string]bool, len(dirs))
	byDepth := map[int][]string{}
	for _, d := range dirs {
		if d == "." {
			exists[d] = true
			continue
		}
		if _, dup := exists[d]; dup {
			continue
		}
		exists[d] = false
		depth := strings.Count(d, "/")
		byDepth[depth] = append(byDepth[depth], d)
	}
	const chunk = 200
	for _, group := range byDepth {
		for len(group) > 0 {
			n := min(chunk, len(group))
			args := append([]string{"ls-tree", "-d", "-z", "--full-tree", "--name-only", rev, "--"}, group[:n]...)
			out, err := g.run(ctx, args...)
			if err != nil {
				return nil, err
			}
			for _, name := range strings.Split(out, "\x00") {
				if name != "" {
					exists[name] = true
				}
			}
			group = group[n:]
		}
	}
	return exists, nil
}

// HeadMessage returns the full commit message of rev.
func (g Git) HeadMessage(ctx context.Context, rev string) (string, error) {
	if err := checkRev(rev); err != nil {
		return "", err
	}
	return g.run(ctx, "log", "-1", "--format=%B", rev, "--")
}
