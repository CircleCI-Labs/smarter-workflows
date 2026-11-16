package analyze

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

var (
	goCmdRe  = regexp.MustCompile(`\bgo\s+(?:test|vet|run|install)\s+([^\s;&|]+)`)
	vulnRe   = regexp.MustCompile(`govulncheck(?:\s+"?([^"\s;&|]+)"?)?`)
	nodeCdRe = regexp.MustCompile(`(?:^|\s)(?:--prefix|-C)\s+(\S+)`)
	npmRe    = regexp.MustCompile(`\bnpm\s+(?:test|run|ci)\b`)
	copyRe   = regexp.MustCompile(`(?i)^COPY\s+(\S+)`)
)

func (e *Engine) scanLangCommands(repoRoot, wd, script string, into *InputSet) {
	for _, m := range goCmdRe.FindAllStringSubmatch(script, -1) {
		if len(m) < 2 {
			continue
		}
		target := strings.Trim(m[1], `"'`)
		if strings.Contains(target, "@") {
			into.addVerifiedFile(repoRoot, wd, "go.mod", InputNote{Signal: SignalGoImports, Detail: "module"})
			into.addVerifiedFile(repoRoot, wd, "go.sum", InputNote{Signal: SignalGoImports, Detail: "module"})
			continue
		}
		e.addGoTarget(repoRoot, wd, target, into)
	}
	if vuln := vulnRe.FindStringSubmatch(script); len(vuln) > 0 {
		tgt := "./..."
		if len(vuln) > 1 && vuln[1] != "" {
			tgt = vuln[1]
		}
		e.addGoTarget(repoRoot, wd, tgt, into)
	}
	if npmRe.MatchString(script) || yarnRe.MatchString(script) {
		pkg := resolveWD(repoRoot, wd, ".")
		into.addVerifiedDir(repoRoot, ".", pkg, InputNote{Signal: SignalRunCommand, Detail: "npm package"})
		e.scanNodeTests(repoRoot, wd, into)
	}
	if pytestRe.MatchString(script) {
		pkg := resolveWD(repoRoot, wd, ".")
		into.addVerifiedDir(repoRoot, ".", pkg, InputNote{Signal: SignalRunCommand, Detail: "pytest package"})
		e.scanPytestReadFiles(repoRoot, wd, into)
	}
	for _, m := range nodeCdRe.FindAllStringSubmatch(script, -1) {
		if len(m) > 1 {
			into.addVerifiedDir(repoRoot, wd, strings.Trim(m[1], `"'`), InputNote{Signal: SignalRunCommand, Detail: "cd"})
		}
	}
	if strings.Contains(script, "python") || strings.Contains(script, "unittest") {
		pkg := resolveWD(repoRoot, wd, ".")
		into.addVerifiedDir(repoRoot, ".", pkg, InputNote{Signal: SignalRunCommand, Detail: "python package"})
		into.addVerifiedFile(repoRoot, wd, "requirements.txt", InputNote{Signal: SignalRunCommand, Detail: "manifest"})
		into.addVerifiedFile(repoRoot, wd, "pyproject.toml", InputNote{Signal: SignalRunCommand, Detail: "manifest"})
		if strings.Contains(script, "unittest discover") {
			if m := regexp.MustCompile(`-s\s+(\S+)`).FindStringSubmatch(script); len(m) > 1 {
				into.addVerifiedDir(repoRoot, wd, m[1], InputNote{Signal: SignalRunCommand, Detail: "unittest -s"})
			}
		}
	}
}

func (e *Engine) addGoTarget(repoRoot, wd, target string, into *InputSet) {
	into.addVerifiedFile(repoRoot, wd, "go.mod", InputNote{Signal: SignalGoImports, Detail: "module"})
	into.addVerifiedFile(repoRoot, wd, "go.sum", InputNote{Signal: SignalGoImports, Detail: "module"})
	if strings.HasSuffix(target, "/...") {
		base := strings.TrimSuffix(target, "/...")
		if base == "." || base == "./" || base == "" {
			base = wd
			if base == "" {
				base = "."
			}
		} else if wd != "" && wd != "." {
			base = resolveWD(repoRoot, wd, base)
		}
		if impactmap.NormalizeDir(base) != "." {
			into.addVerifiedDir(repoRoot, ".", base, InputNote{Signal: SignalGoImports, Detail: target})
		}
		listArg := "./..."
		if base != "" && base != "." {
			listArg = base + "/..."
		}
		e.addGoDeps(repoRoot, listArg, into)
		return
	}
	pkgDir := target
	if strings.HasPrefix(pkgDir, "./") {
		pkgDir = resolveWD(repoRoot, wd, pkgDir)
	} else if wd != "" && wd != "." {
		pkgDir = resolveWD(repoRoot, wd, pkgDir)
	}
	if strings.HasSuffix(pkgDir, "/...") {
		base := strings.TrimSuffix(pkgDir, "/...")
		if base == "" || base == "." {
			base = wd
			if base == "" {
				base = "."
			}
		}
		if impactmap.NormalizeDir(base) != "." {
			into.addVerifiedDir(repoRoot, ".", base, InputNote{Signal: SignalGoImports, Detail: target})
		}
		listArg := "./..."
		if base != "." {
			listArg = base + "/..."
		}
		e.addGoDeps(repoRoot, listArg, into)
		return
	}
	if impactmap.NormalizeDir(pkgDir) != "." {
		into.addVerifiedDir(repoRoot, ".", pkgDir, InputNote{Signal: SignalGoImports, Detail: target})
	}
	e.addGoDeps(repoRoot, pkgDir, into)
}

func (e *Engine) addGoDeps(repoRoot, pkgDir string, into *InputSet) {
	if e.GoWalk == nil || e.goWalkCalls >= maxGoWalkCalls {
		return
	}
	key := repoRoot + "\x00" + pkgDir
	deps, ok := e.goCache[key]
	if !ok {
		e.goWalkCalls++
		var err error
		deps, err = e.GoWalk(repoRoot, pkgDir)
		if err != nil {
			deps = nil
		}
		e.goCache[key] = deps
	}
	n := len(deps)
	for _, d := range deps {
		if impactmap.NormalizeDir(d) == "." {
			continue
		}
		into.addVerifiedDir(repoRoot, ".", d, InputNote{Signal: SignalGoImports, Detail: "import"})
	}
	if len(deps) > 0 {
		e.scanGoPathLiteralsInDirs(repoRoot, deps, into)
	}
	if n > 0 {
		into.noteGoImports(n)
	}
}

func (e *Engine) scanDockerfile(repoRoot, wd, dockerfile string, into *InputSet) {
	rel := resolveWD(repoRoot, wd, dockerfile)
	if !fileExists(repoRoot, rel) {
		return
	}
	into.addVerifiedFile(repoRoot, wd, dockerfile, InputNote{Signal: SignalDockerCopy, Detail: rel})
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	ctx := filepath.Dir(rel)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if m := copyRe.FindStringSubmatch(line); len(m) > 1 {
			src := m[1]
			if src != "." && !strings.HasPrefix(src, "/") {
				into.addVerifiedFile(repoRoot, ctx, src, InputNote{Signal: SignalDockerCopy, Detail: "COPY"})
			}
		}
	}
	into.addVerifiedDir(repoRoot, ".", ctx, InputNote{Signal: SignalDockerCopy, Detail: "context"})
}
