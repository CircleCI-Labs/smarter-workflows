package diff

import (
	"context"
	"fmt"
	"strings"
)

// FilePatch holds a unified diff hunk for one changed file (no file header paths with secrets).
type FilePatch struct {
	Path  string
	Patch string
}

// Patches returns unified diffs for each path in changes between base and head.
func (g Git) Patches(ctx context.Context, base, head string, changes []Change) ([]FilePatch, error) {
	var out []FilePatch
	for _, c := range changes {
		if c.Status == "D" {
			continue
		}
		patch, err := g.patchOne(ctx, base, head, c.Path)
		if err != nil || strings.TrimSpace(patch) == "" {
			continue
		}
		out = append(out, FilePatch{Path: c.Path, Patch: patch})
	}
	return out, nil
}

func (g Git) patchOne(ctx context.Context, base, head, path string) (string, error) {
	if base == "" || head == "" {
		return "", nil
	}
	out, err := g.run(ctx, "diff", "--no-color", "-U3", base, head, "--", path)
	if err != nil {
		return "", fmt.Errorf("diff %s: %w", path, err)
	}
	return strings.TrimSpace(out), nil
}
