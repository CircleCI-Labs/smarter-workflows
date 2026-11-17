package analyze

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

func (e *Engine) noteDocsNavFiles(repoRoot string, into *InputSet) {
	for _, f := range []string{"README.md", "CONTRIBUTING.md", "GLOSSARY.md", "SECURITY.md"} {
		into.addVerifiedFile(repoRoot, ".", f, InputNote{Signal: SignalRunCommand, Detail: "docs site"})
	}
	into.addVerifiedDir(repoRoot, ".", "site", InputNote{Signal: SignalRunCommand, Detail: "hugo site"})
	into.addVerifiedFile(repoRoot, "site", "go.mod", InputNote{Signal: SignalRunCommand, Detail: "hugo site"})
	into.addVerifiedFile(repoRoot, "site", "go.sum", InputNote{Signal: SignalRunCommand, Detail: "hugo site"})
	data, err := os.ReadFile(filepath.Join(repoRoot, "site", "nav.txt"))
	if err != nil {
		return
	}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "==") {
			continue
		}
		path := line
		if i := strings.Index(path, "|"); i >= 0 {
			path = strings.TrimSpace(path[:i])
		}
		if strings.HasSuffix(path, ".md") {
			into.addVerifiedFile(repoRoot, ".", path, InputNote{Signal: SignalRunCommand, Detail: "nav.txt"})
		}
	}
}

func (e *Engine) maybeDocsSiteScript(repoRoot, script string, into *InputSet) {
	if strings.HasSuffix(script, "site/stage.sh") || strings.HasSuffix(script, "site/check.sh") {
		e.noteDocsNavFiles(repoRoot, into)
	}
}
