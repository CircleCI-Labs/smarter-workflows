package ai

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxFileBytes = 256 << 10

var secretContent = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]+`),
	regexp.MustCompile(`xox[bap]-[A-Za-z0-9-]+`),
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]+`),
}

// DenyContent reports whether a repo-relative path must never be sent to the model.
func DenyContent(rel string) bool {
	base := filepath.Base(rel)
	lower := strings.ToLower(rel)
	if strings.Contains(lower, "credentials") || strings.Contains(lower, "secret") {
		return true
	}
	switch {
	case strings.HasPrefix(base, ".env"):
		return true
	case strings.HasSuffix(lower, ".pem"), strings.HasSuffix(lower, ".key"):
		return true
	case strings.HasPrefix(base, "id_rsa"), strings.HasPrefix(base, "id_ed25519"), strings.HasPrefix(base, "id_ecdsa"), strings.HasPrefix(base, "id_dsa"):
		return true
	case strings.HasSuffix(lower, ".p12"), strings.HasSuffix(lower, ".pfx"), strings.HasSuffix(lower, ".jks"), strings.HasSuffix(lower, ".keystore"):
		return true
	case strings.HasSuffix(lower, ".tfvars"), strings.Contains(lower, ".tfstate"):
		return true
	case strings.Contains(lower, "kubeconfig"):
		return true
	case base == ".npmrc", base == ".pypirc", base == ".netrc", base == ".dockerconfigjson", base == ".htpasswd":
		return true
	case strings.HasSuffix(lower, ".gpg"):
		return true
	}
	return false
}

// DenyFileBody reports whether file contents must not be sent.
func DenyFileBody(data []byte) bool {
	if IsBinary(data) {
		return true
	}
	s := string(data)
	for _, re := range secretContent {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// IsBinary guesses binary content (non-UTF8 or NUL).
func IsBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if !utf8.Valid(data) {
		return true
	}
	return strings.Contains(string(data[:min(len(data), 8192)]), "\x00")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
