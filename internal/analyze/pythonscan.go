package analyze

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	pyOpenRe     = regexp.MustCompile(`open\s*\(\s*['"]([^'"]+)['"]`)
	pyJoinRe     = regexp.MustCompile(`os\.path\.join\s*\(([^)]+)\)`)
	pyPathlibRe  = regexp.MustCompile(`Path\s*\(\s*['"]([^'"]+)['"]`)
	pyJoinPartRe = regexp.MustCompile(`['"]([^'"]+)['"]`)
)

func (e *Engine) scanPytestReadFiles(repoRoot, wd string, into *InputSet) {
	pkg := resolveWD(repoRoot, wd, ".")
	root := filepath.Join(repoRoot, filepath.FromSlash(pkg))
	for _, sub := range []string{".", "tests"} {
		dir := root
		if sub != "." {
			dir = filepath.Join(root, sub)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".py") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, ent.Name()))
			if err != nil {
				continue
			}
			e.scanPythonSource(repoRoot, pkg, string(data), into)
		}
	}
}

func (e *Engine) scanPythonSource(repoRoot, wd, src string, into *InputSet) {
	note := InputNote{Signal: SignalRunCommand, Detail: "literal"}
	modRel := "."
	addPath := func(p string) {
		tryAddResolvedLiteralPath(repoRoot, wd, modRel, p, into, note)
	}
	for _, m := range pyOpenRe.FindAllStringSubmatch(src, -1) {
		if len(m) > 1 {
			addPath(m[1])
		}
	}
	for _, m := range pyPathlibRe.FindAllStringSubmatch(src, -1) {
		if len(m) > 1 {
			addPath(m[1])
		}
	}
	for _, m := range pyJoinRe.FindAllStringSubmatch(src, -1) {
		var parts []string
		for _, p := range pyJoinPartRe.FindAllStringSubmatch(m[1], -1) {
			if len(p) > 1 {
				parts = append(parts, p[1])
			}
		}
		if len(parts) == 0 {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(parts...))
		addPath(rel)
	}
}
