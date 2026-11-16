package analyze

import (
	"regexp"
	"strings"
)

var (
	bareScriptRe   = regexp.MustCompile(`^([./]?[^\s;&|]+\.sh)$`)
	goBuildLineRe  = regexp.MustCompile(`\bgo\s+build\b`)
	goInstallPkgRe = regexp.MustCompile(`\bgo\s+install\s+(\S+)`)
	gofmtListRe    = regexp.MustCompile(`\bgofmt\s+-l\s+(\S+)`)
	pipRequireRe   = regexp.MustCompile(`\bpip(?:3)?\s+install\b.*\s-r\s+(\S+)`)
)

var downloadCommands = map[string]struct{}{
	"curl": {}, "wget": {}, "tar": {}, "unzip": {}, "gzip": {}, "gunzip": {},
	"chmod": {}, "chown": {}, "install": {}, "ln": {}, "touch": {},
	"which": {}, "command": {}, "uname": {}, "date": {}, "env": {}, "sudo": {},
	"sha256sum": {}, "shasum": {}, "md5sum": {},
	"apt-get": {}, "apt": {}, "apk": {}, "yum": {}, "dnf": {}, "brew": {},
	"docker": {},
}

// analyzeRunCommand parses a run step command. It returns a non-empty opaque reason when the step cannot be understood.
func (e *Engine) analyzeRunCommand(repoRoot, wd, cmd string, into *InputSet, proof *JobProof) string {
	if strings.TrimSpace(cmd) == "" {
		return ""
	}
	return e.walkShellScript(repoRoot, wd, cmd, 0, into, proof)
}

func (into *InputSet) hasInputs() bool {
	return len(into.files) > 0 || len(into.dirs) > 0
}

func (e *Engine) scanGoBuildLineFromArgv(repoRoot, wd string, argv []ArgWord, into *InputSet) {
	into.addVerifiedFile(repoRoot, wd, "go.mod", InputNote{Signal: SignalGoImports, Detail: "go build"})
	into.addVerifiedFile(repoRoot, wd, "go.sum", InputNote{Signal: SignalGoImports, Detail: "go build"})
	for i := len(argv) - 1; i >= 0; i-- {
		if argv[i].Dynamic {
			continue
		}
		f := strings.Trim(argv[i].Lit, `"'`)
		if f == "." || strings.HasPrefix(f, "./") || strings.Contains(f, "/") {
			e.addGoTarget(repoRoot, wd, f, into)
			return
		}
	}
	e.addGoTarget(repoRoot, wd, ".", into)
}

func (e *Engine) scanGoBuildLine(repoRoot, wd, seg string, into *InputSet) {
	into.addVerifiedFile(repoRoot, wd, "go.mod", InputNote{Signal: SignalGoImports, Detail: "go build"})
	into.addVerifiedFile(repoRoot, wd, "go.sum", InputNote{Signal: SignalGoImports, Detail: "go build"})
	fields := strings.Fields(seg)
	for i := len(fields) - 1; i >= 0; i-- {
		f := strings.Trim(fields[i], `"'`)
		if f == "." || strings.HasPrefix(f, "./") || strings.Contains(f, "/") {
			e.addGoTarget(repoRoot, wd, f, into)
			return
		}
	}
	e.addGoTarget(repoRoot, wd, ".", into)
}
