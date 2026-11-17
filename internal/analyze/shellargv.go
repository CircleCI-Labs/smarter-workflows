package analyze

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ArgWord is one shell word: either a full literal or dynamic (non-literal parts).
type ArgWord struct {
	Lit     string
	Dynamic bool
	Word    *syntax.Word
}

const dynamicPlaceholder = `"$…"`

type shellChunk struct {
	text string
	dyn  bool
}

func parseCallArgv(c *syntax.CallExpr) []ArgWord {
	if c == nil {
		return nil
	}
	out := make([]ArgWord, 0, len(c.Args))
	for _, a := range c.Args {
		out = append(out, parseArgWord(a))
	}
	return out
}

func parseArgWord(w *syntax.Word) ArgWord {
	lit := fullLiteralWord(w)
	if lit != "" {
		return ArgWord{Lit: lit, Word: w}
	}
	return ArgWord{Dynamic: true, Word: w}
}

func fullLiteralWord(w *syntax.Word) string {
	if w == nil || wordHasExpansion(w) {
		return ""
	}
	if lit := litWord(w); lit != "" {
		return lit
	}
	return w.Lit()
}

func wordHasExpansion(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	for _, p := range w.Parts {
		switch p.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp, *syntax.ProcSubst,
			*syntax.ExtGlob, *syntax.DblQuoted:
			if _, ok := p.(*syntax.DblQuoted); ok {
				if dblQuotedHasExpansion(p.(*syntax.DblQuoted)) {
					return true
				}
				continue
			}
			return true
		}
	}
	return false
}

func dblQuotedHasExpansion(dq *syntax.DblQuoted) bool {
	if dq == nil {
		return false
	}
	for _, p := range dq.Parts {
		switch p.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp, *syntax.ProcSubst, *syntax.ExtGlob:
			return true
		}
	}
	return false
}

func argvLiterals(argv []ArgWord) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if !a.Dynamic {
			out[i] = a.Lit
		}
	}
	return out
}

func argvSeg(argv []ArgWord) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a.Dynamic {
			parts[i] = dynamicPlaceholder
		} else {
			parts[i] = a.Lit
		}
	}
	return strings.Join(parts, " ")
}

func resolveCommandName(argv []ArgWord) (name string, err string) {
	if len(argv) == 0 {
		return "", "command name is not literal"
	}
	if !argv[0].Dynamic {
		name = strings.ToLower(strings.TrimPrefix(argv[0].Lit, "./"))
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		return name, ""
	}
	base, ok := literalBasenameFromWord(argv[0].Word)
	if !ok {
		return "", "command name is not literal"
	}
	if strings.HasSuffix(strings.ToLower(base), ".sh") {
		return "", "command name is not literal"
	}
	return strings.ToLower(strings.TrimPrefix(base, "./")), ""
}

// literalBasenameFromWord is true when the segment after the last / is entirely literal (e.g. $HOME/bin/hugo → hugo).
func literalBasenameFromWord(w *syntax.Word) (string, bool) {
	if w == nil {
		return "", false
	}
	var chunks []shellChunk
	collectWordChunks(w, &chunks)
	if len(chunks) == 0 {
		return "", false
	}
	var pathLit strings.Builder
	for _, c := range chunks {
		if c.dyn {
			if pathLit.Len() > 0 {
				if i := strings.LastIndex(pathLit.String(), "/"); i >= 0 {
					suffix := pathLit.String()[i+1:]
					if suffix != "" && !strings.Contains(suffix, "/") {
						return suffix, true
					}
				}
				pathLit.Reset()
			}
			continue
		}
		pathLit.WriteString(c.text)
	}
	s := pathLit.String()
	if s == "" {
		return "", false
	}
	if i := strings.LastIndex(s, "/"); i >= 0 {
		suffix := s[i+1:]
		if suffix != "" && !strings.Contains(suffix, "/") {
			return suffix, true
		}
		return "", false
	}
	return "", false
}

func collectWordChunks(w *syntax.Word, out *[]shellChunk) {
	if w == nil {
		return
	}
	if len(w.Parts) == 0 {
		if s := w.Lit(); s != "" {
			*out = append(*out, shellChunk{text: s})
		}
		return
	}
	for _, p := range w.Parts {
		switch x := p.(type) {
		case *syntax.Lit:
			*out = append(*out, shellChunk{text: x.Value})
		case *syntax.SglQuoted:
			*out = append(*out, shellChunk{text: x.Value})
		case *syntax.DblQuoted:
			for _, dp := range x.Parts {
				switch d := dp.(type) {
				case *syntax.Lit:
					*out = append(*out, shellChunk{text: d.Value})
				case *syntax.SglQuoted:
					*out = append(*out, shellChunk{text: d.Value})
				default:
					*out = append(*out, shellChunk{dyn: true})
				}
			}
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp, *syntax.ProcSubst, *syntax.ExtGlob:
			*out = append(*out, shellChunk{dyn: true})
		}
	}
}

func outsideRepoPathLiteral(lit string) bool {
	if lit == "" {
		return false
	}
	if strings.HasPrefix(lit, "/tmp/") || strings.HasPrefix(lit, "/usr/") || strings.HasPrefix(lit, "/var/") {
		return true
	}
	if strings.HasPrefix(lit, "~/") || lit == "~" {
		return true
	}
	return false
}

func onlyFlagArgs(argv []ArgWord) bool {
	if len(argv) == 0 {
		return true
	}
	for _, a := range argv {
		if a.Dynamic {
			return false
		}
		if !strings.HasPrefix(a.Lit, "-") {
			return false
		}
	}
	return true
}

func outsideRepoLiteralPrefix(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	prefix := literalPathPrefix(w)
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return false
	}
	if strings.HasPrefix(prefix, "/tmp/") || prefix == "/tmp" {
		return true
	}
	if strings.HasPrefix(prefix, "/usr/") || prefix == "/usr" {
		return true
	}
	if strings.HasPrefix(prefix, "/var/") || prefix == "/var" {
		return true
	}
	if strings.HasPrefix(prefix, "~/") || prefix == "~" {
		return true
	}
	if strings.HasPrefix(prefix, "$HOME/") || strings.EqualFold(prefix, "$HOME") {
		return true
	}
	if strings.Contains(prefix, "$HOME/") {
		return true
	}
	// Literal segment before first expansion often includes $HOME as text in chunks
	low := strings.ToLower(prefix)
	if strings.HasPrefix(low, "${home}/") || strings.HasPrefix(low, "${home}") {
		return true
	}
	return false
}

func wordReferencesOutsideVar(w *syntax.Word) bool {
	for _, name := range shellParamNames(w) {
		switch strings.ToUpper(name) {
		case "HOME", "TMPDIR", "TMP", "USER":
			return true
		}
	}
	return false
}

func shellParamNames(w *syntax.Word) []string {
	if w == nil {
		return nil
	}
	var names []string
	var walk func(syntax.Node)
	walk = func(n syntax.Node) {
		if n == nil {
			return
		}
		switch x := n.(type) {
		case *syntax.Word:
			for _, p := range x.Parts {
				walk(p)
			}
		case *syntax.DblQuoted:
			for _, p := range x.Parts {
				walk(p)
			}
		case *syntax.ParamExp:
			names = append(names, paramExpName(x))
		}
	}
	walk(w)
	return names
}

func paramExpName(pe *syntax.ParamExp) string {
	if pe == nil || pe.Param == nil {
		return ""
	}
	return pe.Param.Value
}

func literalPathPrefix(w *syntax.Word) string {
	var chunks []shellChunk
	collectWordChunks(w, &chunks)
	var b strings.Builder
	for _, c := range chunks {
		if c.dyn {
			break
		}
		b.WriteString(c.text)
	}
	return b.String()
}

var goValueFlags = map[string]struct{}{
	"-ldflags": {}, "-o": {}, "--output": {}, "-tags": {}, "-X": {},
	"-run": {}, "-count": {}, "-timeout": {},
}

var understoodDynamicBuiltins = map[string]struct{}{
	"echo": {}, "printf": {}, "mkdir": {}, "export": {}, "test": {}, "[": {},
	"mktemp": {}, "trap": {},
}

var fileReadingBuiltins = map[string]struct{}{
	"cat": {}, "cp": {}, "source": {}, ".": {}, "tar": {}, "unzip": {}, "jq": {},
}

func argvHasDynamic(argv []ArgWord) bool {
	for _, a := range argv {
		if a.Dynamic {
			return true
		}
	}
	return false
}

func dynamicArgOpaque(repoRoot, name string, argv []ArgWord) string {
	if len(argv) == 0 {
		return ""
	}
	for i, a := range argv {
		if !a.Dynamic {
			continue
		}
		if i == 0 {
			continue
		}
		if outsideRepoLiteralPrefix(a.Word) || wordReferencesOutsideVar(a.Word) {
			continue
		}
		if _, ok := understoodDynamicBuiltins[name]; ok {
			continue
		}
		if name == "sha256sum" || name == "shasum" {
			if i >= 1 && argv[i-1].Lit == "-c" {
				return fmt.Sprintf("dynamic argument to %s", name)
			}
			continue
		}
		if _, ok := fileReadingBuiltins[name]; ok {
			return fmt.Sprintf("dynamic argument to %s", name)
		}
		if toolDynamicOpaque(name, argv, i) {
			return fmt.Sprintf("dynamic argument to %s", name)
		}
	}
	return ""
}

func toolDynamicOpaque(name string, argv []ArgWord, dynIdx int) bool {
	switch name {
	case "go":
		return goDynamicOpaque(argv, dynIdx)
	case "circleci":
		return circleciDynamicOpaque(argv, dynIdx)
	case "shellcheck", "gofmt", "jq", "yamllint", "lychee", "hugo":
		return genericToolPathOpaque(argv, dynIdx)
	case "npm", "yarn", "pnpm":
		return npmDynamicOpaque(argv, dynIdx)
	case "docker":
		return dockerDynamicOpaque(argv, dynIdx)
	case "sh", "bash":
		return dynIdx == 1
	case "pytest", "python", "python3":
		return pytestDynamicOpaque(argv, dynIdx)
	}
	if strings.HasSuffix(name, ".sh") && dynIdx == 0 {
		return true
	}
	return false
}

func goDynamicOpaque(argv []ArgWord, dynIdx int) bool {
	if len(argv) < 2 || argv[1].Dynamic {
		return true
	}
	switch argv[1].Lit {
	case "test", "build", "vet", "run", "install", "list", "generate":
	default:
		return false
	}
	if prev := flagExpectsValue(argv, dynIdx); prev != "" {
		if _, ok := goValueFlags[prev]; ok {
			return false
		}
	}
	return true
}

func flagExpectsValue(argv []ArgWord, i int) string {
	if i == 0 {
		return ""
	}
	prev := argv[i-1].Lit
	if strings.HasPrefix(prev, "-") {
		return prev
	}
	return ""
}

func circleciDynamicOpaque(argv []ArgWord, dynIdx int) bool {
	if len(argv) < 3 || argv[1].Dynamic {
		return dynIdx >= 1
	}
	sub := strings.ToLower(argv[1].Lit)
	switch sub {
	case "config":
		if len(argv) >= 3 && !argv[2].Dynamic && (argv[2].Lit == "validate" || argv[2].Lit == "process") {
			return dynIdx == 3
		}
	case "policy":
		if len(argv) >= 3 && !argv[2].Dynamic && argv[2].Lit == "test" {
			return dynIdx == 3
		}
	case "orb":
		if len(argv) >= 3 && !argv[2].Dynamic && (argv[2].Lit == "pack" || argv[2].Lit == "validate") {
			return dynIdx == 3
		}
	}
	return dynIdx >= 2
}

func genericToolPathOpaque(argv []ArgWord, dynIdx int) bool {
	if dynIdx < 1 {
		return false
	}
	if prev := flagExpectsValue(argv, dynIdx); prev != "" {
		return true
	}
	return true
}

func npmDynamicOpaque(argv []ArgWord, dynIdx int) bool {
	if dynIdx < 2 {
		return false
	}
	sub := ""
	if !argv[1].Dynamic {
		sub = argv[1].Lit
	}
	if sub == "test" || sub == "run" || sub == "ci" {
		return dynIdx >= 2 && !strings.HasPrefix(argv[dynIdx].Lit, "-")
	}
	return false
}

func dockerDynamicOpaque(argv []ArgWord, dynIdx int) bool {
	if len(argv) < 2 {
		return false
	}
	if argv[1].Dynamic {
		return true
	}
	if argv[1].Lit == "build" {
		return dynIdx >= 2
	}
	return false
}

func pytestDynamicOpaque(argv []ArgWord, dynIdx int) bool {
	return dynIdx >= 1
}
