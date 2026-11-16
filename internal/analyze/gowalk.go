package analyze

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var embedRe = regexp.MustCompile(`//go:embed\s+(.+)`)

// WalkGoPackages returns repo-relative directories for Go sources reachable
// from pkgDir (imports, replace dirs, embed patterns, go.mod/sum, vendor).
func WalkGoPackages(repoRoot, pkgDir string) ([]string, error) {
	pkgDir = strings.TrimSuffix(pkgDir, "/...")
	pkgDir = strings.TrimPrefix(pkgDir, "./")
	if pkgDir == "" || pkgDir == "..." {
		pkgDir = "."
	}
	modAbs, module, err := goModuleRoot(repoRoot, pkgDir)
	if err != nil {
		return nil, err
	}
	modRel, err := filepath.Rel(repoRoot, modAbs)
	if err != nil || strings.HasPrefix(modRel, "..") {
		modRel = "."
	}
	modRel = filepath.ToSlash(modRel)

	dirs := map[string]struct{}{}
	add := func(rel string) {
		if modRel != "." && !strings.HasPrefix(rel, modRel) && rel != "go.mod" && rel != "go.sum" {
			rel = filepath.ToSlash(filepath.Join(modRel, rel))
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		if rel == "" || strings.HasPrefix(rel, "..") {
			return
		}
		dirs[rel] = struct{}{}
	}
	add(filepath.Join(modRel, "go.mod"))
	add(filepath.Join(modRel, "go.sum"))
	if modRel == "." {
		add("go.mod")
		add("go.sum")
	}
	_ = walkGoWork(modAbs, modRel, add)
	replaces, _ := parseGoModReplaces(filepath.Join(modAbs, "go.mod"))
	for _, r := range replaces {
		add(filepath.Join(modRel, r))
	}
	_ = walkVendorModules(filepath.Join(modAbs, "vendor", "modules.txt"), modAbs, modRel, add)
	importDirs, err := collectImportDirs(repoRoot, modAbs, modRel, module, pkgDir)
	if err != nil {
		return nil, err
	}
	for _, d := range importDirs {
		add(d)
		_ = scanGoEmbeds(repoRoot, d, add)
	}
	out := make([]string, 0, len(dirs))
	for d := range dirs {
		out = append(out, d)
	}
	return out, nil
}

func goModuleRoot(repoRoot, pkgDir string) (modAbs, module string, err error) {
	dir := repoRoot
	if pkgDir != "" && pkgDir != "." {
		dir = filepath.Join(repoRoot, filepath.FromSlash(pkgDir))
	}
	for {
		data, readErr := os.ReadFile(filepath.Join(dir, "go.mod"))
		if readErr == nil {
			module = parseModulePath(string(data))
			if module == "" {
				return "", "", fmt.Errorf("go walk: no module path in %s", dir)
			}
			return dir, module, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", fmt.Errorf("go walk: no go.mod under %s", pkgDir)
		}
		dir = parent
	}
}

func parseModulePath(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func parseGoModReplaces(modPath string) ([]string, error) {
	data, err := os.ReadFile(modPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "replace ") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		local := strings.Trim(parts[len(parts)-1], `"`)
		if strings.Contains(local, "/") || strings.Contains(local, `\`) {
			out = append(out, local)
		}
	}
	return out, nil
}

func walkGoWork(modAbs, modRel string, add func(string)) error {
	data, err := os.ReadFile(filepath.Join(modAbs, "go.work"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "use ") {
			dir := strings.Trim(strings.TrimPrefix(line, "use "), `()`)
			dir = strings.Trim(dir, `"`)
			add(filepath.Join(modRel, dir))
		}
	}
	return nil
}

func walkVendorModules(path, modAbs, modRel string, add func(string)) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "## ") {
			pkg := strings.TrimPrefix(line, "## ")
			if i := strings.Index(pkg, " "); i >= 0 {
				pkg = pkg[:i]
			}
			dir := filepath.Join(modRel, "vendor", filepath.ToSlash(pkg))
			abs := filepath.Join(modAbs, "vendor", filepath.FromSlash(pkg))
			if st, statErr := os.Stat(abs); statErr == nil && st.IsDir() {
				add(dir)
			}
		}
	}
	return sc.Err()
}

func collectImportDirs(repoRoot, modAbs, modRel, module, pkgDir string) ([]string, error) {
	walkRoot := modAbs
	if pkgDir != "" && pkgDir != "." {
		candidate := filepath.Join(repoRoot, filepath.FromSlash(pkgDir))
		if strings.HasPrefix(candidate, modAbs) {
			walkRoot = candidate
		}
	}
	dirSet := map[string]struct{}{}
	fset := token.NewFileSet()
	err := filepath.Walk(walkRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info == nil {
			return walkErr
		}
		if info.IsDir() {
			base := info.Name()
			if base == "vendor" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return nil
		}
		relDir, relErr := filepath.Rel(repoRoot, filepath.Dir(path))
		if relErr == nil {
			dirSet[filepath.ToSlash(relDir)] = struct{}{}
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if module != "" && (p == module || strings.HasPrefix(p, module+"/")) {
				suffix := strings.TrimPrefix(strings.TrimPrefix(p, module), "/")
				local := filepath.Join(modAbs, filepath.FromSlash(suffix))
				if st, statErr := os.Stat(local); statErr == nil && st.IsDir() {
					if rel, relErr := filepath.Rel(repoRoot, local); relErr == nil {
						dirSet[filepath.ToSlash(rel)] = struct{}{}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(dirSet))
	for d := range dirSet {
		out = append(out, d)
	}
	return out, nil
}

func scanGoEmbeds(repoRoot, dir string, add func(string)) error {
	abs := filepath.Join(repoRoot, filepath.FromSlash(dir))
	return filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, m := range embedRe.FindAllStringSubmatch(string(data), -1) {
			if len(m) < 2 {
				continue
			}
			for _, pat := range strings.Fields(m[1]) {
				pat = strings.Trim(pat, `"`)
				if pat == "" || strings.Contains(pat, "..") {
					continue
				}
				target := filepath.ToSlash(filepath.Join(dir, pat))
				add(target)
				add(filepath.ToSlash(filepath.Join(dir, filepath.Dir(pat))))
			}
		}
		return nil
	})
}

func defaultGoWalk(repoRoot, pkgDir string) ([]string, error) {
	return WalkGoPackages(repoRoot, pkgDir)
}
