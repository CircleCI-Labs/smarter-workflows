package analyze

import "strings"

func (e *Engine) gitTool(repoRoot string, wds wdSet, argv []string, into *InputSet, needsInputs *bool) bool {
	if len(argv) < 2 {
		return true
	}
	i := 1
	if i < len(argv) && argv[i] == "-C" {
		if i+1 >= len(argv) {
			return false
		}
		if gitSubcommand(argv[i+1]) {
			i++
		} else if i+2 < len(argv) {
			dir := normalizePathToken(argv[i+1])
			*needsInputs = true
			for w := range wds {
				into.addVerifiedPathOrDir(repoRoot, w, dir)
			}
			i += 2
		} else {
			return false
		}
	}
	if i >= len(argv) {
		return true
	}
	sub := argv[i]
	switch sub {
	case "rev-parse", "describe", "log", "status", "symbolic-ref":
		return true
	case "config":
		if i+1 < len(argv) && argv[i+1] == "--get" {
			return true
		}
		return false
	case "remote":
		if i+2 < len(argv) && argv[i+1] == "get-url" {
			return true
		}
		return false
	case "ls-files", "diff", "archive":
		*needsInputs = true
		e.gitPathspecArgs(repoRoot, wds, argv[i+1:], into)
		return true
	case "grep":
		*needsInputs = true
		for w := range wds {
			for _, a := range argv[i+1:] {
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
	default:
		return false
	}
}

func gitSubcommand(s string) bool {
	switch s {
	case "rev-parse", "describe", "log", "status", "symbolic-ref", "config", "remote",
		"ls-files", "diff", "archive", "grep":
		return true
	default:
		return false
	}
}

func (e *Engine) gitPathspecArgs(repoRoot string, wds wdSet, argv []string, into *InputSet) {
	var paths []string
	for _, a := range argv {
		a = normalizePathToken(a)
		if a == "" || strings.HasPrefix(a, "-") || a == "--" {
			continue
		}
		if strings.HasPrefix(a, ":") {
			continue
		}
		paths = append(paths, a)
	}
	if len(paths) == 0 {
		for w := range wds {
			into.addVerifiedDir(repoRoot, w, ".", InputNote{Signal: SignalRunCommand, Detail: "git pathspec"})
		}
		return
	}
	for w := range wds {
		for _, a := range paths {
			if looksLikePath(a) {
				into.addVerifiedPathOrDir(repoRoot, w, a)
			}
		}
	}
}
