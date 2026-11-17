package analyze

import (
	"regexp"
	"strings"
)

var (
	pytestRe       = regexp.MustCompile(`\b(?:pytest|python\s+-m\s+pytest)\b`)
	yarnRe         = regexp.MustCompile(`\b(?:yarn|pnpm)\s+(?:install|test|run)\b`)
	shInvokeRe     = regexp.MustCompile(`(?:^|\s)(?:sh|bash)\s+([^\s;&|]+)`)
	dotSlashExecRe = regexp.MustCompile(`^(?:\./)([^\s;&|]+)`)
	dockerBuildCmd = regexp.MustCompile(`\bdocker\s+build\b`)
)

var benignCommands = map[string]struct{}{
	"echo": {}, "printf": {}, "cd": {}, "export": {}, "set": {}, "unset": {}, "mkdir": {}, ":": {},
	"mv": {}, "rm": {}, "ls": {}, "test": {}, "true": {},
	"sleep": {}, "seq": {}, "read": {}, "then": {}, "else": {}, "fi": {}, "do": {}, "done": {}, "esac": {},
	"exit": {}, "return": {}, "continue": {}, "break": {}, "sort": {}, "wc": {}, "tail": {}, "tr": {},
	"dirname": {}, "pwd": {}, "mktemp": {}, "trap": {}, "cut": {}, "shift": {}, "od": {}, "openssl": {},
	"[": {}, "circleci-smarter": {}, "find": {}, "grep": {}, "head": {}, "sed": {},
}

func truncateOpaque(s string) string {
	const max = 120
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

var understoodStepKeys = map[string]struct{}{
	"checkout":             {},
	"attach_workspace":     {},
	"persist_to_workspace": {},
	"store_artifacts":      {},
	"store_test_results":   {},
	"setup_remote_docker":  {},
	"add_ssh_keys":         {},
	"save_cache":           {},
	"restore_cache":        {},
}
