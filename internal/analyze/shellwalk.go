package analyze

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type wdSet map[string]struct{}

func (s wdSet) copy() wdSet {
	out := make(wdSet, len(s))
	for k := range s {
		out[k] = struct{}{}
	}
	return out
}

func (s wdSet) merge(other wdSet) {
	for k := range other {
		s[k] = struct{}{}
	}
}

type shellWalk struct {
	e           *Engine
	repoRoot    string
	into        *InputSet
	proof       *JobProof
	depth       int
	needsInputs bool
	opaque      string
	funcs       map[string][]*syntax.Stmt
	scriptRel   string
	env         map[string]string
	positional  []string
	workspaceAt []string
}

func (e *Engine) walkShellScript(repoRoot, wd, script string, depth int, into *InputSet, proof *JobProof, workspaceAt []string) string {
	return e.walkShellScriptFile(repoRoot, wd, "", script, depth, into, proof, nil, nil, workspaceAt)
}

func (e *Engine) walkShellScriptFile(repoRoot, wd, scriptRel, script string, depth int, into *InputSet, proof *JobProof, positional []string, parentEnv map[string]string, workspaceAt []string) string {
	script = strings.TrimSpace(script)
	if script == "" {
		return ""
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	if err != nil {
		return truncateOpaque(script)
	}
	sw := &shellWalk{e: e, repoRoot: repoRoot, into: into, proof: proof, depth: depth, funcs: map[string][]*syntax.Stmt{}, scriptRel: scriptRel, env: cloneShellEnv(parentEnv), positional: []string{""}, workspaceAt: workspaceAt}
	if len(positional) > 0 {
		sw.setPositional(positional)
	}
	sw.collectFuncs(file.Stmts)
	start := wdSet{wd: {}}
	for _, st := range file.Stmts {
		start = sw.stmt(st, start)
	}
	if proof != nil && sw.needsInputs {
		proof.requiresRepoInputs = true
	}
	return sw.opaque
}

func (sw *shellWalk) collectFuncs(stmts []*syntax.Stmt) {
	for _, st := range stmts {
		if st == nil || st.Cmd == nil {
			continue
		}
		if fd, ok := st.Cmd.(*syntax.FuncDecl); ok && fd.Name != nil {
			if blk, ok := fd.Body.Cmd.(*syntax.Block); ok {
				sw.funcs[fd.Name.Value] = blk.Stmts
			}
		}
	}
}

func (sw *shellWalk) fail(op string) {
	if sw.opaque == "" {
		sw.opaque = op
	}
}

func (sw *shellWalk) stmts(stmts []*syntax.Stmt, wds wdSet) wdSet {
	cur := wds.copy()
	for _, st := range stmts {
		cur = sw.stmt(st, cur)
	}
	return cur
}

func (sw *shellWalk) stmt(st *syntax.Stmt, wds wdSet) wdSet {
	if st == nil || st.Cmd == nil {
		return wds
	}
	return sw.command(st.Cmd, wds)
}

func (sw *shellWalk) command(cmd syntax.Command, wds wdSet) wdSet {
	if sw.opaque != "" {
		return wds
	}
	switch c := cmd.(type) {
	case *syntax.CallExpr:
		return sw.callExpr(c, wds, false)
	case *syntax.IfClause:
		cond := sw.stmts(c.Cond, wds.copy())
		then := sw.stmts(c.Then, wds.copy())
		out := cond.copy()
		out.merge(then)
		if c.Else != nil {
			elseW := sw.ifClause(c.Else, wds.copy())
			out.merge(elseW)
		}
		return out
	case *syntax.WhileClause:
		out := wds.copy()
		out.merge(sw.stmts(c.Cond, wds.copy()))
		out.merge(sw.stmts(c.Do, wds.copy()))
		return out
	case *syntax.ForClause:
		sw.forLoopGlobs(c, wds)
		return sw.stmts(c.Do, wds.copy())
	case *syntax.BinaryCmd:
		if c.Op == syntax.Pipe || c.Op == syntax.PipeAll {
			if sw.pipeIntoInterpreter(c.Y) {
				sw.fail(truncateOpaque("pipe into interpreter"))
				return wds
			}
		}
		left := sw.stmt(c.X, wds)
		return sw.stmt(c.Y, left)
	case *syntax.Subshell:
		_ = sw.stmts(c.Stmts, wds.copy())
		return wds
	case *syntax.Block:
		return sw.stmts(c.Stmts, wds.copy())
	case *syntax.CaseClause:
		return sw.caseClause(c, wds)
	case *syntax.TestClause:
		sw.walkTestExpr(c.X, wds)
		return wds
	case *syntax.ArithmCmd:
		return wds
	case *syntax.DeclClause:
		return wds
	case *syntax.TimeClause:
		if c.Stmt != nil {
			return sw.stmt(c.Stmt, wds)
		}
		return wds
	case *syntax.FuncDecl:
		return wds
	default:
		sw.fail(truncateOpaque(fmt.Sprintf("unsupported %T", cmd)))
		return wds
	}
}

func (sw *shellWalk) ifClause(c *syntax.IfClause, wds wdSet) wdSet {
	if c == nil {
		return wds
	}
	if truth, ok := sw.evalIfCondition(c.Cond, wds); ok {
		if truth {
			return sw.stmts(c.Then, wds.copy())
		}
		if c.Else != nil {
			return sw.ifClause(c.Else, wds.copy())
		}
		return wds
	}
	out := wds.copy()
	out.merge(sw.stmts(c.Cond, wds.copy()))
	out.merge(sw.stmts(c.Then, wds.copy()))
	if c.Else != nil {
		out.merge(sw.ifClause(c.Else, wds.copy()))
	}
	return out
}

func (sw *shellWalk) pipeIntoInterpreter(st *syntax.Stmt) bool {
	for st != nil {
		switch c := st.Cmd.(type) {
		case *syntax.BinaryCmd:
			if c.Op == syntax.Pipe || c.Op == syntax.PipeAll {
				if sw.callName(c.Y) {
					return true
				}
				st = c.Y
				continue
			}
			st = c.Y
		case *syntax.CallExpr:
			return sw.callName(st)
		default:
			return false
		}
	}
	return false
}

func (sw *shellWalk) callName(st *syntax.Stmt) bool {
	ce, ok := st.Cmd.(*syntax.CallExpr)
	if !ok || len(ce.Args) == 0 {
		return false
	}
	name := strings.ToLower(litWord(ce.Args[0]))
	return name == "sh" || name == "bash" || name == "python" || name == "python3" || name == "node"
}

func (sw *shellWalk) walkTestExpr(te syntax.TestExpr, wds wdSet) {
	if te == nil {
		return
	}
	switch x := te.(type) {
	case *syntax.BinaryTest:
		sw.walkTestExpr(x.X, wds)
		sw.walkTestExpr(x.Y, wds)
	case *syntax.UnaryTest:
		sw.walkTestExpr(x.X, wds)
	case *syntax.ParenTest:
		sw.walkTestExpr(x.X, wds)
	case *syntax.Word:
		sw.walkWordParts(x, wds)
	}
}

func (sw *shellWalk) walkWordParts(w *syntax.Word, wds wdSet) {
	if w == nil {
		return
	}
	for _, p := range w.Parts {
		switch sub := p.(type) {
		case *syntax.CmdSubst:
			if _, ok := sw.evalCmdSubst(sub, wds); !ok {
				_ = sw.stmts(sub.Stmts, wds.copy())
			}
		case *syntax.DblQuoted:
			for _, dp := range sub.Parts {
				if cs, ok := dp.(*syntax.CmdSubst); ok {
					if _, ok := sw.evalCmdSubst(cs, wds); !ok {
						_ = sw.stmts(cs.Stmts, wds.copy())
					}
				}
			}
		}
	}
}

func (sw *shellWalk) callExpr(c *syntax.CallExpr, wds wdSet, nested bool) wdSet {
	for _, a := range c.Args {
		sw.walkWordParts(a, wds)
	}
	sw.captureAssigns(c.Assigns, wds)
	for _, a := range c.Assigns {
		if a.Value == nil || a.Name == nil {
			continue
		}
		if _, ok := sw.env[a.Name.Value]; ok {
			continue
		}
		sw.walkWordParts(a.Value, wds)
	}
	if len(c.Args) == 0 && len(c.Assigns) > 0 {
		return wds
	}
	argv := parseCallArgv(c)
	for len(argv) > 0 && !argv[0].Dynamic && argv[0].Lit == "!" {
		argv = argv[1:]
	}
	if expanded, ok := sw.expandAtCommand(argv); ok {
		argv = expanded
	}
	for i := range argv {
		if !argv[i].Dynamic || argv[i].Word == nil {
			continue
		}
		if i == 0 {
			if p, ok := sw.resolveExecutable(argv[i].Word, pickWD(wds)); ok {
				argv[i] = ArgWord{Lit: p, Dynamic: false, Word: argv[i].Word}
				continue
			}
			if s, ok := sw.expandWord(argv[i].Word, wds); ok {
				argv[i] = ArgWord{Lit: s, Dynamic: false, Word: argv[i].Word}
				continue
			}
			if commandVarUnset(argv[i].Word, sw) {
				return wds
			}
			continue
		}
		if s, ok := sw.expandWord(argv[i].Word, wds); ok {
			argv[i] = ArgWord{Lit: s, Dynamic: false, Word: argv[i].Word}
		}
	}
	if len(argv) == 0 {
		return sw.callExprPartial(c, wds)
	}
	name, nameErr := resolveCommandName(argv)
	if nameErr != "" && len(argv) >= 2 {
		sub := strings.ToLower(argv[1].Lit)
		if (sub == "version" || sub == "--version") && argv[0].Word != nil {
			if base, ok := literalBasenameFromWord(argv[0].Word); ok {
				argv[0] = ArgWord{Lit: base, Dynamic: false, Word: argv[0].Word}
				name, nameErr = resolveCommandName(argv)
			}
		}
	}
	if nameErr != "" {
		if len(argv) >= 2 {
			sub := strings.ToLower(argv[1].Lit)
			if sub == "version" || sub == "--version" {
				return wds
			}
			if sub == "verify-blob" || sub == "sign-blob" {
				return wds
			}
		}
		if len(argv) > 0 {
			lit0 := normalizePathToken(argv[0].Lit)
			if argv[0].Dynamic && argv[0].Word != nil {
				if s, ok := sw.expandWord(argv[0].Word, wds); ok {
					lit0 = normalizePathToken(s)
				}
			}
			if isWorkspaceBinaryPath(lit0, sw.workspaceAt) {
				sw.e.noteArgvRepoPaths(sw.repoRoot, wds, argv, c.Args, sw.into)
				return wds
			}
		}
		if argv[0].Dynamic && argv[0].Word != nil {
			if s, ok := sw.expandWord(argv[0].Word, wds); ok {
				s = normalizePathToken(s)
				if strings.HasSuffix(s, ".sh") {
					rel := resolveWD(sw.repoRoot, pickWD(wds), s)
					if fileExists(sw.repoRoot, rel) {
						sw.needsInputs = true
						if op := sw.e.followScript(sw.repoRoot, pickWD(wds), s, sw.depth, sw.into, sw.funcs, argv[1:], sw.env, nil); op != "" {
							sw.fail(op)
						}
						return wds
					}
				}
			}
		}
		sw.fail(nameErr)
		return wds
	}
	if name == "" {
		return sw.callExprPartial(c, wds)
	}
	if name == "shift" {
		n := 1
		if len(argv) >= 2 && !argv[1].Dynamic {
			if v, err := strconv.Atoi(argv[1].Lit); err == nil && v > 0 {
				n = v
			}
		}
		if len(sw.positional) > n {
			sw.positional = append([]string{sw.positional[0]}, sw.positional[1+n:]...)
		} else {
			sw.positional = []string{""}
		}
		return wds
	}
	if len(c.Args) > 0 {
		if lit := litWord(c.Args[0]); lit != "" {
			if stmts, ok := sw.funcs[lit]; ok {
				saved := sw.positional
				sw.setPositional(positionalArgsFromArgv(sw.repoRoot, pickWD(wds), argv[1:]))
				out := sw.stmts(stmts, wds)
				sw.positional = saved
				return out
			}
		}
	}
	sub := ""
	if len(argv) > 1 && !argv[1].Dynamic {
		sub = strings.ToLower(argv[1].Lit)
	}
	if name == "git" && sub == "grep" {
		sw.needsInputs = true
		for w := range wds {
			for _, a := range c.Args[2:] {
				if lit := litWord(a); lit != "" {
					lit = normalizePathToken(lit)
					if lit != "" && !strings.HasPrefix(lit, "-") && !strings.HasPrefix(lit, ":") && lit != "--" && looksLikePath(lit) {
						sw.into.addVerifiedPathOrDir(sw.repoRoot, w, lit)
					}
				}
			}
		}
		return wds
	}
	if stmts, ok := sw.funcs[name]; ok {
		saved := sw.positional
		sw.setPositional(positionalArgsFromArgv(sw.repoRoot, pickWD(wds), argv[1:]))
		out := sw.stmts(stmts, wds)
		sw.positional = saved
		return out
	}
	if name == "find" {
		sw.needsInputs = true
		for w := range wds {
			for i := 1; i < len(c.Args); i++ {
				lit := litWord(c.Args[i])
				if lit == "" || strings.HasPrefix(lit, "-") || lit == "(" || lit == ")" || strings.HasPrefix(lit, `\`) {
					continue
				}
				sw.into.addVerifiedPathOrDir(sw.repoRoot, w, lit)
			}
		}
		return wds
	}
	if name == "cd" && len(argv) >= 2 {
		if argv[1].Dynamic {
			if p, ok := sw.expandWord(argv[1].Word, wds); ok {
				out := wdSet{}
				for wd := range wds {
					out[resolveWD(sw.repoRoot, wd, p)] = struct{}{}
				}
				return out
			}
			if !dynamicArgIsBounded(argv[1].Word, sw.env) {
				sw.fail(truncateOpaque("dynamic argument to cd"))
				return wds
			}
			return wds
		}
		out := wdSet{}
		for wd := range wds {
			out[resolveWD(sw.repoRoot, wd, argv[1].Lit)] = struct{}{}
		}
		return out
	}
	if name == "eval" {
		sw.fail(truncateOpaque("eval"))
		return wds
	}
	if name == "source" || name == "." {
		if len(argv) < 2 {
			return wds
		}
		if argv[1].Dynamic && argv[1].Word != nil {
			if p, ok := sw.expandWord(argv[1].Word, wds); ok {
				argv[1] = ArgWord{Lit: p, Dynamic: false, Word: argv[1].Word}
			} else if p, ok := sw.resolveExecutable(argv[1].Word, pickWD(wds)); ok {
				argv[1] = ArgWord{Lit: p, Dynamic: false, Word: argv[1].Word}
			}
		}
		if argv[1].Dynamic {
			if outsideRepoLiteralPrefix(argv[1].Word) {
				return wds
			}
			sw.fail(truncateOpaque("dynamic argument to source"))
			return wds
		}
		sw.needsInputs = true
		rel := resolveWD(sw.repoRoot, pickWD(wds), argv[1].Lit)
		if !fileExists(sw.repoRoot, rel) {
			sw.fail(fmt.Sprintf("source %q not in repo", argv[1].Lit))
			return wds
		}
		if op := sw.e.followScript(sw.repoRoot, pickWD(wds), argv[1].Lit, sw.depth, sw.into, sw.funcs, nil, sw.env, nil); op != "" {
			sw.fail(op)
		}
		return wds
	}
	pos := positionalFromSlice(sw.positional)
	if op := sw.e.analyzeSimpleCommand(sw.repoRoot, wds, argv, c.Args, sw.into, &sw.needsInputs, sw.depth, sw.funcs, sw.env, sw.scriptRel, pos); op != "" {
		sw.fail(op)
	}
	return wds
}

func pickWD(wds wdSet) string {
	for w := range wds {
		return w
	}
	return "."
}

func litWord(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	if s := w.Lit(); s != "" {
		return s
	}
	if len(w.Parts) != 1 {
		return ""
	}
	switch p := w.Parts[0].(type) {
	case *syntax.Lit:
		return p.Value
	case *syntax.SglQuoted:
		return p.Value
	case *syntax.DblQuoted:
		return w.Lit()
	default:
		return ""
	}
}

func (sw *shellWalk) forLoopGlobs(c *syntax.ForClause, wds wdSet) {
	wi, ok := c.Loop.(*syntax.WordIter)
	if !ok {
		return
	}
	for _, item := range wi.Items {
		lit := litWord(item)
		if lit == "" {
			continue
		}
		for wd := range wds {
			if strings.Contains(lit, "*") {
				sw.e.expandShellGlob(sw.repoRoot, wd, lit, sw.into)
			} else {
				sw.into.addVerifiedPathOrDir(sw.repoRoot, wd, lit)
			}
		}
	}
}

func (e *Engine) expandShellGlob(repoRoot, wd, pattern string, into *InputSet) {
	pat := resolveWD(repoRoot, wd, pattern)
	matches, err := filepath.Glob(filepath.Join(repoRoot, filepath.FromSlash(pat)))
	if err != nil {
		return
	}
	for _, abs := range matches {
		rel, err := filepath.Rel(repoRoot, abs)
		if err != nil {
			continue
		}
		into.addVerifiedPath(repoRoot, filepath.ToSlash(rel), InputNote{Signal: SignalRunCommand, Detail: "glob"})
	}
}

func (sw *shellWalk) callExprPartial(c *syntax.CallExpr, wds wdSet) wdSet {
	argv := parseCallArgv(c)
	pos := positionalFromSlice(sw.positional)
	if op := sw.e.analyzeSimpleCommand(sw.repoRoot, wds, argv, c.Args, sw.into, &sw.needsInputs, sw.depth, sw.funcs, sw.env, sw.scriptRel, pos); op != "" {
		sw.fail(op)
	}
	return wds
}
