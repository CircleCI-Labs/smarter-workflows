package analyze

import (
	"path/filepath"
	"strings"
	"unicode"
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

func (s *InputSet) addVerifiedPath(repoRoot, rel string, note InputNote) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	rel = strings.TrimPrefix(rel, "./")
	if rel == "" || rel == "." || strings.Contains(rel, "..") || !filepath.IsLocal(rel) {
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
	if c := cleanDir(rel); c != "" {
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
	rel = filepath.ToSlash(filepath.Clean(rel))
	if st, err := statRepo(repoRoot, rel); err == nil && st.IsDir() {
		s.addDirVerified(rel, note)
		return
	}
	if st, err := statRepo(repoRoot, rel); err == nil && !st.IsDir() {
		return
	}
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
