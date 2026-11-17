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

func (e *Engine) analyzeSimpleCommand(repoRoot string, wds wdSet, argv []ArgWord, args []*syntax.Word, into *InputSet, needsInputs *bool, depth int, funcs map[string][]*syntax.Stmt, env map[string]string, scriptRel string) string {
	name, nameErr := resolveCommandName(argv)
	if nameErr != "" {
		return nameErr
	}
	if name == "" {
		return ""
	}
	if op := dynamicArgOpaque(repoRoot, name, argv); op != "" {
		return truncateOpaque(op)
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
	if e.analyzeDevTool(repoRoot, wds, name, litArgv, litSeg, into, needsInputs) {
		return ""
	}
	if name == "circleci" {
		return truncateOpaque(seg)
	}
	if goBuildLineRe.MatchString(litSeg) {
		*needsInputs = true
		for w := range wds {
			e.scanGoBuildLineFromArgv(repoRoot, w, argv, into, env, scriptRel)
		}
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
	if script, ok := scriptArgv(name, litArgv, litSeg); ok {
		*needsInputs = true
		rel := resolveWD(repoRoot, wd, script)
		if !fileExists(repoRoot, rel) {
			return fmt.Sprintf("script %q not found", script)
		}
		before := into.hasInputs()
		e.followScript(repoRoot, wd, script, depth, into, funcs)
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
	case "circleci", "shellcheck", "gofmt", "jq", "yamllint", "hugo", "lychee", "go", "gh", "cosign":
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
		return argv[1], true
	}
	if strings.HasSuffix(name, ".sh") && len(argv) > 0 && argv[0] != "" {
		return argv[0], true
	}
	return "", false
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
