package analyze

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func normalizeWorkspaceRoots(ats []string, baseWD string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, at := range ats {
		at = strings.TrimSpace(at)
		if at == "" {
			continue
		}
		if !strings.HasPrefix(at, "/") {
			at = resolveWD("", baseWD, at)
		}
		at = filepath.ToSlash(filepath.Clean(at))
		if _, ok := seen[at]; ok {
			continue
		}
		seen[at] = struct{}{}
		out = append(out, at)
	}
	return out
}

func isWorkspaceBinaryPath(lit string, workspaceAt []string) bool {
	if lit == "" || len(workspaceAt) == 0 {
		return false
	}
	if !strings.HasPrefix(lit, "/") {
		return false
	}
	lit = filepath.ToSlash(filepath.Clean(lit))
	for _, at := range workspaceAt {
		at = filepath.ToSlash(filepath.Clean(at))
		if lit == at || strings.HasPrefix(lit, at+"/") {
			return true
		}
	}
	return false
}

func (e *Engine) noteArgvRepoPaths(repoRoot string, wds wdSet, argv []ArgWord, args []*syntax.Word, into *InputSet) {
	for w := range wds {
		e.noteLiteralWords(repoRoot, w, args, into)
		for i := 1; i < len(argv); i++ {
			if argv[i].Dynamic {
				continue
			}
			a := normalizePathToken(argv[i].Lit)
			if a == "" || strings.HasPrefix(a, "-") {
				continue
			}
			if strings.HasPrefix(a, "/") {
				continue
			}
			if looksLikePath(a) {
				into.addVerifiedPathOrDir(repoRoot, w, a)
			}
		}
	}
}
