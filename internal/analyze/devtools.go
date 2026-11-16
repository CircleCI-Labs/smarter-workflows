package analyze

import (
	"strings"
)

func (e *Engine) analyzeDevTool(repoRoot string, wds wdSet, name string, argv []string, seg string, into *InputSet, needsInputs *bool) bool {
	switch name {
	case "git":
		if len(argv) >= 2 && argv[1] == "grep" {
			*needsInputs = true
			for w := range wds {
				for _, a := range argv[2:] {
					a = normalizePathToken(a)
					if a == "" || strings.HasPrefix(a, "-") || a == "--" {
						continue
					}
					if strings.HasPrefix(a, ":") {
						continue
					}
					if looksLikePath(a) {
						into.addVerifiedPathOrDir(repoRoot, w, a)
					}
				}
			}
			return true
		}
	case "circleci":
		return e.circleciTool(repoRoot, wds, argv, into, needsInputs)
	case "shellcheck", "jq", "yamllint", "lychee":
		*needsInputs = true
		for w := range wds {
			e.notePathArgsFromArgv(repoRoot, w, argv[1:], into)
		}
		return true
	case "gofmt":
		*needsInputs = true
		for w := range wds {
			e.gofmtTool(repoRoot, w, argv, into)
		}
		return true
	case "hugo":
		*needsInputs = true
		for w := range wds {
			e.hugoTool(repoRoot, w, argv, into)
		}
		return true
	default:
		if name == "go" && len(argv) >= 2 && argv[1] == "vet" {
			*needsInputs = true
			for w := range wds {
				e.scanLangCommands(repoRoot, w, seg, into)
			}
			return true
		}
	}
	return false
}

func (e *Engine) circleciTool(repoRoot string, wds wdSet, argv []string, into *InputSet, needsInputs *bool) bool {
	if len(argv) < 2 {
		return true
	}
	sub := strings.ToLower(argv[1])
	switch sub {
	case "config":
		if len(argv) >= 4 && (argv[2] == "validate" || argv[2] == "process") {
			*needsInputs = true
			for w := range wds {
				into.addVerifiedPathOrDir(repoRoot, w, argv[3])
			}
			return true
		}
	case "policy":
		if len(argv) >= 4 && argv[2] == "test" {
			*needsInputs = true
			for w := range wds {
				into.addVerifiedPathOrDir(repoRoot, w, argv[3])
			}
			return true
		}
	case "orb":
		if len(argv) >= 4 && (argv[2] == "pack" || argv[2] == "validate") {
			*needsInputs = true
			for w := range wds {
				into.addVerifiedPathOrDir(repoRoot, w, argv[3])
			}
			return true
		}
	}
	if sub == "version" || sub == "--help" {
		return true
	}
	return false
}

func (e *Engine) gofmtTool(repoRoot, wd string, argv []string, into *InputSet) {
	for i := 1; i < len(argv); i++ {
		if argv[i] == "-l" && i+1 < len(argv) {
			into.addVerifiedPathOrDir(repoRoot, wd, argv[i+1])
			i++
			continue
		}
		if !strings.HasPrefix(argv[i], "-") {
			into.addVerifiedPathOrDir(repoRoot, wd, argv[i])
		}
	}
}

func (e *Engine) hugoTool(repoRoot, wd string, argv []string, into *InputSet) {
	for i := 1; i < len(argv); i++ {
		if (argv[i] == "--source" || argv[i] == "-s") && i+1 < len(argv) {
			into.addVerifiedPathOrDir(repoRoot, wd, argv[i+1])
			i++
		}
	}
	into.addVerifiedDir(repoRoot, wd, ".", InputNote{Signal: SignalRunCommand, Detail: "hugo cwd"})
	if strings.HasSuffix(wd, "site") || wd == "site" {
		e.noteDocsNavFiles(repoRoot, into)
	}
}

func (e *Engine) notePathArgsFromArgv(repoRoot, wd string, argv []string, into *InputSet) {
	for _, a := range argv {
		a = normalizePathToken(a)
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		if strings.Contains(a, "*") {
			e.expandShellGlob(repoRoot, wd, a, into)
			continue
		}
		if looksLikePath(a) {
			into.addVerifiedPathOrDir(repoRoot, wd, a)
		}
	}
}
