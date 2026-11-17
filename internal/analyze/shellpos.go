package analyze

import (
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func positionalFromSlice(pos []string) []string {
	if len(pos) <= 1 {
		return nil
	}
	return pos[1:]
}

func (sw *shellWalk) setPositional(args []string) {
	sw.positional = []string{""}
	sw.positional = append(sw.positional, args...)
}

func positionalArgsFromArgv(repoRoot, wd string, argv []ArgWord) []string {
	var out []string
	for _, a := range argv {
		if a.Dynamic {
			out = append(out, "")
			continue
		}
		out = append(out, normalizePathToken(a.Lit))
	}
	return out
}

func (e *Engine) recordScriptCallArgs(repoRoot, wd string, args []ArgWord, into *InputSet) {
	for _, a := range args {
		if a.Dynamic {
			continue
		}
		lit := normalizePathToken(a.Lit)
		if lit == "" {
			continue
		}
		rel := resolveWD(repoRoot, wd, lit)
		if !looksLikePath(lit) && !fileExists(repoRoot, rel) {
			continue
		}
		if st, err := statRepo(repoRoot, rel); err != nil {
			continue
		} else if st.IsDir() {
			into.addVerifiedDir(repoRoot, wd, lit, InputNote{Signal: SignalScript, Detail: "arg"})
		} else {
			into.addVerifiedFile(repoRoot, wd, lit, InputNote{Signal: SignalScript, Detail: "arg"})
		}
	}
}

func (sw *shellWalk) expandParam(pe *syntax.ParamExp, wds wdSet) (string, bool) {
	if pe == nil {
		return "", false
	}
	if pe.Length && (pe.Param == nil || pe.Param.Value == "#") {
		n := len(sw.positional) - 1
		if n < 0 {
			n = 0
		}
		return strconv.Itoa(n), true
	}
	if pe.Param == nil {
		return "", false
	}
	name := pe.Param.Value
	if pe.Exp != nil && (pe.Exp.Op == syntax.DefaultUnsetOrNull || pe.Exp.Op == syntax.DefaultUnset) {
		if v, ok := sw.paramValue(name); ok && v != "" {
			return v, true
		}
		if pe.Exp.Word == nil {
			return "", true
		}
		if s, ok := sw.expandWord(pe.Exp.Word, wds); ok {
			return s, true
		}
		if litWord(pe.Exp.Word) == "" {
			return "", true
		}
		return "", false
	}
	return sw.paramValue(name)
}

func (sw *shellWalk) paramValue(name string) (string, bool) {
	switch name {
	case "0":
		if sw.scriptRel != "" {
			return sw.scriptRel, true
		}
		return "", false
	case "BASH_SOURCE":
		if sw.scriptRel != "" {
			return sw.scriptRel, true
		}
		return "", false
	case "@", "*":
		return sw.positionalJoin()
	}
	if n, err := strconv.Atoi(name); err == nil && n > 0 {
		if n < len(sw.positional) && sw.positional[n] != "" {
			return sw.positional[n], true
		}
		return "", false
	}
	v, ok := sw.env[name]
	return v, ok
}

func (sw *shellWalk) positionalCount() int {
	n := 0
	for i := 1; i < len(sw.positional); i++ {
		if sw.positional[i] != "" {
			n++
		}
	}
	return n
}

func (sw *shellWalk) positionalJoin() (string, bool) {
	var parts []string
	for i := 1; i < len(sw.positional); i++ {
		if sw.positional[i] != "" {
			parts = append(parts, sw.positional[i])
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, " "), true
}

func wordIsOnlyPositionalAt(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	if len(w.Parts) != 1 {
		return false
	}
	switch x := w.Parts[0].(type) {
	case *syntax.ParamExp:
		return x.Param != nil && (x.Param.Value == "@" || x.Param.Value == "*")
	case *syntax.DblQuoted:
		if len(x.Parts) != 1 {
			return false
		}
		pe, ok := x.Parts[0].(*syntax.ParamExp)
		return ok && pe.Param != nil && (pe.Param.Value == "@" || pe.Param.Value == "*")
	default:
		return false
	}
}

func (sw *shellWalk) expandAtCommand(argv []ArgWord) ([]ArgWord, bool) {
	if len(argv) == 0 || !argv[0].Dynamic || argv[0].Word == nil || !wordIsOnlyPositionalAt(argv[0].Word) {
		return argv, false
	}
	if len(sw.positional) < 2 {
		return argv, false
	}
	var out []ArgWord
	for i := 1; i < len(sw.positional); i++ {
		if sw.positional[i] == "" {
			continue
		}
		out = append(out, ArgWord{Lit: sw.positional[i], Dynamic: false})
	}
	if len(out) == 0 {
		return argv, false
	}
	return out, true
}

func parseScriptStepCommand(cmd string) (script string, args []ArgWord, ok bool) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || strings.Contains(cmd, "\n") || strings.ContainsAny(cmd, "|&;") {
		return "", nil, false
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil || len(file.Stmts) != 1 {
		return "", nil, false
	}
	ce, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(ce.Args) == 0 {
		return "", nil, false
	}
	argv := parseCallArgv(ce)
	if len(argv) == 0 {
		return "", nil, false
	}
	name, nameErr := resolveCommandName(argv)
	if nameErr != "" {
		return "", nil, false
	}
	litArgv := argvLiteralStrings(argv)
	seg := argvSeg(argv)
	s, ok := scriptArgv(name, litArgv, seg)
	if !ok {
		return "", nil, false
	}
	return s, argv[1:], true
}
