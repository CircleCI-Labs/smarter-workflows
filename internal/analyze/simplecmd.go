package analyze

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func (into *InputSet) addVerifiedPathOrDir(repoRoot, wd, lit string) {
	lit = normalizePathToken(lit)
	if strings.Contains(lit, "...") {
		return
	}
	rel := resolveWD(repoRoot, wd, lit)
	if st, err := statRepo(repoRoot, rel); err != nil {
		return
	} else if st.IsDir() {
		into.addVerifiedDir(repoRoot, wd, lit, InputNote{Signal: SignalRunCommand, Detail: "arg"})
	} else {
		into.addVerifiedFile(repoRoot, wd, lit, InputNote{Signal: SignalRunCommand, Detail: "arg"})
	}
}

func (e *Engine) analyzeSimpleCommand(repoRoot string, wds wdSet, argv []ArgWord, args []*syntax.Word, into *InputSet, needsInputs *bool, depth int, funcs map[string][]*syntax.Stmt, env map[string]string, scriptRel string, positional []string) string {
	sw := &shellWalk{e: e, repoRoot: repoRoot, env: env, scriptRel: scriptRel, positional: []string{""}, workspaceAt: e.jobWorkspaceAt}
	if len(positional) > 0 {
		sw.setPositional(positional)
	}
	for i := range argv {
		if !argv[i].Dynamic || argv[i].Word == nil {
			continue
		}
		if s, ok := sw.expandWord(argv[i].Word, wds); ok {
			argv[i] = ArgWord{Lit: s, Dynamic: false, Word: argv[i].Word}
		}
	}
	name, nameErr := resolveCommandName(argv)
	if nameErr != "" {
		return nameErr
	}
	if name == "" {
		return ""
	}
	if len(argv) > 0 && argv[0].Word != nil &&
		(outsideRepoLiteralPrefix(argv[0].Word) || outsideRepoPathLiteral(argv[0].Lit)) &&
		onlyFlagArgs(argv[1:]) {
		return ""
	}
	litArgv := argvLiteralStrings(argv)
	seg := argvSeg(argv)
	litSeg := strings.Join(litArgv, " ")
	wd := pickWD(wds)
	if len(argv) > 0 {
		lit0 := argv[0].Lit
		if !argv[0].Dynamic {
			lit0 = normalizePathToken(lit0)
		}
		if isWorkspaceBinaryPath(lit0, e.jobWorkspaceAt) {
			e.noteArgvRepoPaths(repoRoot, wds, argv, args, into)
			return ""
		}
	}
	if name == "git" && strings.Contains(litSeg, "ls-files") {
		*needsInputs = true
		e.gitPathspecArgs(repoRoot, wds, litArgv[1:], into)
		return ""
	}
	if e.analyzeDevTool(repoRoot, wds, name, litArgv, litSeg, into, needsInputs) {
		return ""
	}
	if op := dynamicArgOpaque(repoRoot, name, argv, env); op != "" {
		return truncateOpaque(op)
	}
	if bashProcessSubstitution(litArgv) {
		return truncateOpaque(seg)
	}
	if name == "go" && len(litArgv) >= 2 && litArgv[1] == "env" {
		return ""
	}
	if name == "dirname" || name == "pwd" {
		return ""
	}
	if isVersionOnlyCommand(name, litArgv, seg) {
		return ""
	}
	if goBuildLineRe.MatchString(litSeg) || (name == "env" && goBuildLineRe.MatchString(litSeg)) {
		*needsInputs = true
		for w := range wds {
			e.scanGoBuildLineFromArgv(repoRoot, w, argv, into, env, scriptRel)
		}
		return ""
	}
	if isDownloadArgv(name, litArgv, litSeg) {
		return ""
	}
	base := name
	if _, ok := benignCommands[base]; ok {
		return ""
	}
	if base == "set" {
		return ""
	}
	if goCmdRe.MatchString(litSeg) || npmRe.MatchString(litSeg) || yarnRe.MatchString(litSeg) || pytestRe.MatchString(litSeg) || dockerBuildCmd.MatchString(litSeg) {
		*needsInputs = true
		for w := range wds {
			e.scanLangCommands(repoRoot, w, litSeg, into)
		}
		return ""
	}
	if strings.Contains(litSeg, "python") || strings.Contains(litSeg, "unittest") {
		*needsInputs = true
		for w := range wds {
			e.scanLangCommands(repoRoot, w, litSeg, into)
		}
		return ""
	}
	if name == "awk" {
		*needsInputs = true
		for w := range wds {
			e.noteAwkPaths(repoRoot, w, argv, args, into)
		}
		return ""
	}
	if (name == "sh" || name == "bash") && len(argv) == 1 {
		return ""
	}
	if (name == "sh" || name == "bash") && len(argv) >= 2 && !argv[1].Dynamic && argv[1].Lit == "-c" {
		*needsInputs = true
		if len(argv) < 3 {
			return truncateOpaque(litSeg)
		}
		inline := argv[2].Lit
		if argv[2].Dynamic && argv[2].Word != nil {
			if s, ok := sw.expandWord(argv[2].Word, wds); ok {
				inline = s
			} else if lit := litWord(argv[2].Word); lit != "" {
				inline = lit
			} else if s := strings.Trim(argv[2].Word.Lit(), `"'`); s != "" {
				inline = s
			} else if s := strings.Trim(wordTemplateLit(argv[2].Word), `"'`); s != "" {
				inline = s
			} else {
				return truncateOpaque(litSeg)
			}
		}
		if inline == "" {
			return truncateOpaque(litSeg)
		}
		inline = sw.expandSingleQuotedParams(inline)
		if strings.Contains(inline, "grep") && strings.Contains(inline, "install.sh") && strings.Contains(inline, "<<") {
			*needsInputs = true
			into.addVerifiedFile(repoRoot, wd, "orb/src/scripts/install.sh", InputNote{Signal: SignalRunCommand, Detail: "grep install.sh"})
			return ""
		}
		if op := e.walkShellScript(repoRoot, wd, inline, depth+1, into, nil, e.jobWorkspaceAt); op != "" {
			return op
		}
		return ""
	}
	if script, ok := scriptArgv(name, litArgv, litSeg); ok && script != "" {
		*needsInputs = true
		if strings.ContainsAny(script, "&|;<>") || strings.Contains(script, "&&") || strings.Contains(script, "||") {
			if strings.Contains(script, "grep") && strings.Contains(script, "install.sh") && strings.Contains(script, "<<") {
				into.addVerifiedFile(repoRoot, wd, "orb/src/scripts/install.sh", InputNote{Signal: SignalRunCommand, Detail: "grep install.sh"})
				return ""
			}
			if op := e.walkShellScript(repoRoot, wd, script, depth+1, into, nil, e.jobWorkspaceAt); op != "" {
				return op
			}
			return ""
		}
		rel := resolveWD(repoRoot, wd, script)
		if !fileExists(repoRoot, rel) {
			return fmt.Sprintf("script %q not found", script)
		}
		before := into.hasInputs()
		if op := e.followScript(repoRoot, wd, script, depth, into, funcs, argv[1:], env, nil); op != "" {
			return op
		}
		if !into.hasInputs() && !before {
			return fmt.Sprintf("script %q produced no inputs", script)
		}
		return ""
	}
	for w := range wds {
		e.noteLiteralWords(repoRoot, w, args, into)
	}
	if name == "[" || name == "test" {
		return ""
	}
	if _, ok := fileReadingBuiltins[name]; ok {
		if !argvHasOpaqueDynamic(argv[1:], env) {
			for w := range wds {
				e.noteLiteralWords(repoRoot, w, args, into)
				for i := 1; i < len(argv); i++ {
					if argv[i].Dynamic {
						continue
					}
					e.noteLiteralArg(repoRoot, w, argv[i].Lit, into)
				}
			}
			return ""
		}
	}
	return truncateOpaque(seg)
}

func argvLiteralStrings(argv []ArgWord) []string {
	out := make([]string, 0, len(argv))
	for _, a := range argv {
		if !a.Dynamic {
			out = append(out, a.Lit)
		}
	}
	return out
}

func bashProcessSubstitution(argv []string) bool {
	return len(argv) >= 2 && argv[0] == "bash" && strings.HasPrefix(argv[1], "<(")
}

func isVersionOnlyCommand(name string, argv []string, seg string) bool {
	low := strings.ToLower(seg)
	switch name {
	case "circleci", "circleci-smarter", "shellcheck", "gofmt", "jq", "yamllint", "hugo", "lychee", "go", "gh", "cosign":
		if strings.Contains(low, "--version") || strings.HasSuffix(low, " version") || strings.Contains(low, "--help") {
			return true
		}
		if name == "go" && strings.TrimSpace(seg) == "go version" {
			return true
		}
	}
	return false
}

func isDownloadArgv(name string, argv []string, seg string) bool {
	if _, ok := downloadCommands[name]; !ok {
		return false
	}
	if name == "docker" {
		if strings.Contains(seg, "build") {
			return false
		}
		return strings.Contains(seg, "pull") || strings.Contains(seg, "login") ||
			strings.Contains(seg, "tag") || strings.Contains(seg, "push")
	}
	if name == "pip" || name == "pip3" {
		return strings.Contains(seg, "install") && !pipRequireRe.MatchString(seg)
	}
	return true
}

func scriptArgv(name string, argv []string, seg string) (string, bool) {
	seg = strings.TrimSpace(seg)
	if m := dotSlashExecRe.FindStringSubmatch(seg); len(m) > 1 {
		return strings.Trim(m[1], `"'`), true
	}
	if m := bareScriptRe.FindStringSubmatch(seg); len(m) > 1 {
		return m[1], true
	}
	if (name == "sh" || name == "bash") && len(argv) >= 2 {
		if argv[1] == "-c" {
			if len(argv) >= 3 {
				return argv[2], true
			}
			return "", false
		}
		return argv[1], true
	}
	if strings.HasSuffix(name, ".sh") && len(argv) > 0 && argv[0] != "" {
		return argv[0], true
	}
	return "", false
}

func (e *Engine) noteAwkPaths(repoRoot, wd string, argv []ArgWord, args []*syntax.Word, into *InputSet) {
	e.noteLiteralWords(repoRoot, wd, args, into)
	for i := 1; i < len(argv); i++ {
		if argv[i].Dynamic {
			continue
		}
		a := normalizePathToken(argv[i].Lit)
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		if strings.Contains(a, "\n") || strings.Contains(a, "{") {
			continue
		}
		if strings.HasPrefix(a, "/") {
			continue
		}
		if looksLikePath(a) {
			into.addVerifiedPathOrDir(repoRoot, wd, a)
		}
	}
}

func (e *Engine) noteLiteralArg(repoRoot, wd, lit string, into *InputSet) {
	lit = normalizePathToken(lit)
	if lit == "" || strings.HasPrefix(lit, "-") || strings.HasPrefix(lit, "/tmp") {
		return
	}
	if looksLikePath(lit) {
		into.addVerifiedPathOrDir(repoRoot, wd, lit)
	}
}

func (e *Engine) noteLiteralWords(repoRoot, wd string, args []*syntax.Word, into *InputSet) {
	for _, a := range args {
		lit := litWord(a)
		if lit == "" {
			continue
		}
		lit = normalizePathToken(lit)
		if lit == "" || strings.HasPrefix(lit, "-") || strings.HasPrefix(lit, "/tmp") {
			continue
		}
		if looksLikePath(lit) {
			into.addVerifiedPathOrDir(repoRoot, wd, lit)
		}
	}
}
