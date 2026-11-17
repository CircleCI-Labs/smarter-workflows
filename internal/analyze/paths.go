package analyze

import (
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/CircleCI-Labs/smarter-workflows/internal/impactmap"
)

// normalizePathToken strips quotes and trailing shell punctuation from a candidate path token.
func normalizePathToken(tok string) string {
	tok = strings.TrimSpace(tok)
	tok = strings.Trim(tok, `"'`+"`")
	for len(tok) > 0 {
		r := tok[len(tok)-1]
		if r == ')' || r == '(' || r == ';' || r == ',' || r == '|' || r == '&' {
			tok = tok[:len(tok)-1]
			continue
		}
		break
	}
	if strings.HasPrefix(tok, "-") {
		return ""
	}
	if strings.Contains(tok, "=") && !strings.Contains(tok, "/") {
		return ""
	}
	return tok
}

// repoRelPath resolves p from baseDir (repo-relative) to a repo-relative path inside repoRoot.
func repoRelPath(repoRoot, baseDir, p string) (string, bool) {
	p = filepath.ToSlash(strings.TrimSpace(p))
	if p == "" {
		return "", false
	}
	repoAbs, err := filepath.Abs(filepath.Clean(repoRoot))
	if err != nil {
		return "", false
	}
	base := repoAbs
	baseDir = filepath.ToSlash(baseDir)
	if baseDir != "" && baseDir != "." {
		base = filepath.Join(repoAbs, filepath.FromSlash(baseDir))
	}
	target := filepath.Clean(filepath.Join(base, filepath.FromSlash(p)))
	rel, err := filepath.Rel(repoAbs, target)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	if !filepath.IsLocal(rel) {
		return "", false
	}
	return rel, true
}

// tryAddResolvedLiteralPath stores p when it exists under fileDir, modRel, or repo root.
func tryAddResolvedLiteralPath(repoRoot, fileDir, modRel, p string, into *InputSet, note InputNote) {
	if strings.TrimSpace(p) == "" || strings.Contains(p, "$") {
		return
	}
	for _, base := range []string{fileDir, modRel, "."} {
		rel, ok := repoRelPath(repoRoot, base, p)
		if !ok {
			continue
		}
		if into.addVerifiedPath(repoRoot, rel, note) {
			return
		}
	}
}

func (s *InputSet) addVerifiedPath(repoRoot, rel string, note InputNote) bool {
	rel = impactmap.NormalizeDir(filepath.ToSlash(path.Clean(rel)))
	if rel == "." {
		if note.Signal != SignalRunCommand {
			return false
		}
		if st, err := statRepo(repoRoot, "."); err == nil && st.IsDir() {
			s.dirs["."] = struct{}{}
			s.got = true
			if note.Signal != "" {
				s.note(".", true, note)
			}
			return true
		}
		return false
	}
	if rel == "" || strings.Contains(rel, "..") || !filepath.IsLocal(rel) {
		return false
	}
	st, err := statRepo(repoRoot, rel)
	if err != nil {
		return false
	}
	if st.IsDir() {
		s.addDirVerified(rel, note)
		return true
	}
	s.addFileVerified(rel, note)
	return true
}

func (s *InputSet) addDirVerified(rel string, note InputNote) {
	if c := cleanDir(rel); c != "" && c != "." {
		s.dirs[c] = struct{}{}
		s.got = true
		if note.Signal != "" {
			s.note(c, true, note)
		}
	}
}

func (s *InputSet) addFileVerified(rel string, note InputNote) {
	if c := cleanFile(rel); c != "" {
		s.files[c] = struct{}{}
		s.got = true
		if note.Signal != "" {
			s.note(c, false, note)
		}
	}
}

func (s *InputSet) addVerifiedFile(repoRoot, wd, token string, note InputNote) {
	tok := normalizePathToken(token)
	if tok == "" {
		return
	}
	rel := resolveWD(repoRoot, wd, tok)
	s.addVerifiedPath(repoRoot, rel, note)
}

func (s *InputSet) addVerifiedDir(repoRoot, wd, token string, note InputNote) {
	tok := normalizePathToken(token)
	if tok == "" {
		return
	}
	rel := resolveWD(repoRoot, wd, tok)
	s.addVerifiedPath(repoRoot, rel, note)
}

func looksLikeFlag(tok string) bool {
	return strings.HasPrefix(tok, "-") || strings.Contains(tok, "=") && !strings.Contains(tok, "/")
}

func looksLikePath(p string) bool {
	if strings.HasSuffix(p, ".") && p != "..." {
		return false
	}
	if strings.Contains(p, "/") || strings.Contains(p, ".") {
		return true
	}
	for _, ext := range []string{".sh", ".go", ".py", ".yml", ".yaml", ".json", ".mod", ".sum", ".tf"} {
		if strings.HasSuffix(strings.ToLower(p), ext) {
			return true
		}
	}
	return false
}

func isVersionish(tok string) bool {
	if strings.Count(tok, ".") >= 2 && !strings.Contains(tok, "/") {
		for _, r := range tok {
			if r != '.' && r != '/' && !unicode.IsDigit(r) {
				return false
			}
		}
		return true
	}
	return false
}
