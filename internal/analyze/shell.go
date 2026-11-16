package analyze

import (
	"os"
	"path/filepath"
)

const (
	maxScriptDepth = 6
	maxScriptFiles = 48
)

func (e *Engine) followScript(repoRoot, wd, script string, depth int, into *InputSet) {
	if e.scriptFiles >= maxScriptFiles {
		return
	}
	e.scriptFiles++
	rel := resolveWD(repoRoot, wd, script)
	if !fileExists(repoRoot, rel) {
		return
	}
	into.addVerifiedFile(repoRoot, wd, script, InputNote{Signal: SignalScript, Detail: rel})
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	e.walkShellScript(repoRoot, wd, string(data), depth+1, into, nil)
	e.maybeDocsSiteScript(repoRoot, rel, into)
	e.scanScriptLiteralFileRefs(repoRoot, rel, string(data), into)
}
