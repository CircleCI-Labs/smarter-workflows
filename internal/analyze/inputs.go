package analyze

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

// MethodInference is the impact map method value for analyze-built maps.
const MethodInference = "inference"

// InputSet accumulates repo-relative file and directory inputs for one job.
type InputSet struct {
	files map[string]struct{}
	dirs  map[string]struct{}
	got   bool
	notes map[string][]InputNote
	goPkg int
}

func newInputSet() *InputSet {
	return &InputSet{
		files: map[string]struct{}{},
		dirs:  map[string]struct{}{},
		notes: map[string][]InputNote{},
	}
}

func (s *InputSet) merge(o *InputSet) {
	if o == nil {
		return
	}
	if len(o.files) > 0 || len(o.dirs) > 0 {
		s.got = true
	}
	for f := range o.files {
		s.files[f] = struct{}{}
	}
	for d := range o.dirs {
		s.dirs[d] = struct{}{}
	}
	for k, v := range o.notes {
		s.notes[k] = append(s.notes[k], v...)
	}
	if o.goPkg > s.goPkg {
		s.goPkg = o.goPkg
	}
}

func (s *InputSet) addFile(p string) {
	if c := cleanFile(p); c != "" {
		s.files[c] = struct{}{}
		s.got = true
	}
}

func (s *InputSet) addDir(p string) {
	if c := cleanDir(p); c != "" && c != "." {
		s.dirs[c] = struct{}{}
		s.got = true
	}
}

func (s *InputSet) addPath(repoRoot, wd, p string) {
	p = strings.TrimSpace(p)
	if p == "" || p == "..." || strings.HasPrefix(p, "/") || strings.Contains(p, "<<") || strings.Contains(p, "$") {
		return
	}
	rel := resolveWD(repoRoot, wd, p)
	if strings.Contains(rel, "..") {
		return
	}
	rel = strings.TrimPrefix(path.Clean(rel), "./")
	if strings.HasSuffix(p, "/") || strings.HasSuffix(p, "/**") {
		s.addDir(strings.TrimSuffix(strings.TrimSuffix(rel, "/**"), "/"))
		return
	}
	base := filepath.Base(rel)
	if ext := filepath.Ext(base); ext != "" || strings.HasSuffix(base, "Dockerfile") {
		s.addFile(rel)
		return
	}
	s.addDir(rel)
}

func (s *InputSet) sortedFiles() []string {
	out := make([]string, 0, len(s.files))
	for f := range s.files {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// provenComplete is true when at least one inferred path exists in the repo checkout
// (or is a non-root directory). Prevents complete:true with useless paths.
func (s *InputSet) provenComplete(repoRoot string) bool {
	if len(s.files) == 0 && len(s.dirs) == 0 {
		return false
	}
	for f := range s.files {
		if fileExists(repoRoot, f) {
			return true
		}
	}
	for d := range s.dirs {
		if d == "." {
			continue
		}
		if dirExists(repoRoot, d) {
			return true
		}
	}
	return false
}

func (s *InputSet) sortedDirs() []string {
	out := make([]string, 0, len(s.dirs))
	for d := range s.dirs {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func cleanFile(p string) string {
	c, err := impactmap.CleanPath(p, false)
	if err != nil {
		// Allow manifest paths that CleanPath rejects as "not a file" when they are dirs.
		c2, err2 := impactmap.CleanPath(p, true)
		if err2 != nil {
			return ""
		}
		return c2
	}
	return c
}

func cleanDir(p string) string {
	p = impactmap.NormalizeDir(filepath.ToSlash(path.Clean(strings.TrimSpace(p))))
	if p == "." {
		return "."
	}
	c, err := impactmap.CleanPath(p, true)
	if err != nil {
		return ""
	}
	return impactmap.NormalizeDir(c)
}

func resolveWD(repoRoot, wd, p string) string {
	base := wd
	if base == "" {
		base = "."
	}
	return path.Clean(path.Join(base, p))
}

func rel(repoRoot, abs string) string {
	if abs == "." {
		return "."
	}
	return strings.TrimPrefix(path.Clean(abs), "./")
}
