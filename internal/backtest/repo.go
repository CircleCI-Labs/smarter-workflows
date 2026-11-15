package backtest

import (
	"context"
	"path"

	"github.com/CircleCI-Labs/smarter-workflows/internal/diff"
)

// staticRepo hands the planner the recorded change list instead of calling git.
type staticRepo struct {
	changes []diff.Change
	dirs    map[string]bool
}

func newStaticRepo(changes []Change) *staticRepo {
	dirs := map[string]bool{".": true}
	for _, c := range changes {
		for _, d := range pathDirs(c.Path) {
			dirs[d] = true
		}
	}
	dc := make([]diff.Change, len(changes))
	for i, c := range changes {
		dc[i] = diff.Change{Path: c.Path, Status: c.Status}
	}
	return &staticRepo{changes: dc, dirs: dirs}
}

func pathDirs(p string) []string {
	var dirs []string
	dirs = append(dirs, ".")
	for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
		dirs = append(dirs, d)
	}
	return dirs
}

func (s *staticRepo) Changes(ctx context.Context, base, head string) ([]diff.Change, error) {
	return s.changes, nil
}

func (s *staticRepo) DirsExist(ctx context.Context, rev string, dirs []string) (map[string]bool, error) {
	out := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		out[d] = s.dirs[d]
	}
	return out, nil
}
