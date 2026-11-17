package analyze

import (
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const (
	maxScriptDepth = 6
	maxScriptFiles = 48
)

func (e *Engine) followScript(repoRoot, wd, script string, depth int, into *InputSet, funcs map[string][]*syntax.Stmt) {
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
	if funcs != nil {
		mergeShellFuncs(string(data), funcs)
	}
	e.walkShellScriptFile(repoRoot, wd, rel, string(data), depth+1, into, nil)
	e.maybeDocsSiteScript(repoRoot, rel, into)
	e.scanScriptLiteralFileRefs(repoRoot, rel, string(data), into)
}

func mergeShellFuncs(body string, funcs map[string][]*syntax.Stmt) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(body), "")
	if err != nil {
		return
	}
	var walk func([]*syntax.Stmt)
	walk = func(stmts []*syntax.Stmt) {
		for _, st := range stmts {
			if st == nil || st.Cmd == nil {
				continue
			}
			if fd, ok := st.Cmd.(*syntax.FuncDecl); ok && fd.Name != nil {
				if blk, ok := fd.Body.Cmd.(*syntax.Block); ok {
					funcs[fd.Name.Value] = blk.Stmts
				}
			}
		}
	}
	walk(file.Stmts)
}
