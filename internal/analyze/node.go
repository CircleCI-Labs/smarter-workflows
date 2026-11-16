package analyze

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	nodeRequireRe = regexp.MustCompile(`require\s*\(\s*['"]([^'"]+)['"]\s*\)`)
	pathJoinDirRe = regexp.MustCompile(`path\.join\(\s*__dirname\s*,\s*([^)]+)\)`)
	joinPartRe    = regexp.MustCompile(`['"]([^'"]+)['"]`)
)

func (e *Engine) scanNodeTests(repoRoot, wd string, into *InputSet) {
	pkg := resolveWD(repoRoot, wd, ".")
	pkgAbs := filepath.Join(repoRoot, filepath.FromSlash(pkg))
	testDir := filepath.Join(pkgAbs, "test")
	_ = filepath.WalkDir(testDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".js") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		e.scanNodeTestSource(repoRoot, path, string(data), into)
		return nil
	})
}

func (e *Engine) scanNodeTestSource(repoRoot, testFile, src string, into *InputSet) {
	testDir := filepath.Dir(testFile)
	for _, m := range nodeRequireRe.FindAllStringSubmatch(src, -1) {
		if len(m) < 2 {
			continue
		}
		imp := m[1]
		if !strings.HasPrefix(imp, ".") {
			continue
		}
		abs := filepath.Clean(filepath.Join(testDir, imp))
		rel, err := filepath.Rel(repoRoot, abs)
		if err != nil {
			continue
		}
		if st, err := os.Stat(abs); err == nil {
			if st.IsDir() {
				into.addVerifiedPath(repoRoot, filepath.ToSlash(rel), InputNote{Signal: SignalRunCommand, Detail: "require"})
			} else {
				into.addVerifiedPath(repoRoot, filepath.ToSlash(rel), InputNote{Signal: SignalRunCommand, Detail: "require"})
				if !strings.HasSuffix(imp, ".js") {
					js := abs + ".js"
					if st2, err := os.Stat(js); err == nil && !st2.IsDir() {
						rel2, _ := filepath.Rel(repoRoot, js)
						into.addVerifiedPath(repoRoot, filepath.ToSlash(rel2), InputNote{Signal: SignalRunCommand, Detail: "require"})
					}
				}
			}
		}
	}
	for _, m := range pathJoinDirRe.FindAllStringSubmatch(src, -1) {
		if len(m) < 2 {
			continue
		}
		var parts []string
		for _, p := range joinPartRe.FindAllStringSubmatch(m[1], -1) {
			if len(p) > 1 {
				parts = append(parts, p[1])
			}
		}
		if len(parts) == 0 {
			continue
		}
		abs := testDir
		for _, p := range parts {
			abs = filepath.Clean(filepath.Join(abs, p))
		}
		rel, err := filepath.Rel(repoRoot, abs)
		if err != nil {
			continue
		}
		into.addVerifiedPath(repoRoot, filepath.ToSlash(rel), InputNote{Signal: SignalRunCommand, Detail: "readFile"})
	}
}
