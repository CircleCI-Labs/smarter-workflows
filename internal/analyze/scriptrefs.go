package analyze

import (
	"path/filepath"
	"regexp"
	"strings"
)

var scriptRelJSONRe = regexp.MustCompile(`testdata/[a-zA-Z0-9_/.-]+\.json`)

func (e *Engine) scanScriptLiteralFileRefs(repoRoot, scriptRel, body string, into *InputSet) {
	scriptDir := filepath.Dir(filepath.FromSlash(scriptRel))
	for _, p := range scriptRelJSONRe.FindAllString(body, -1) {
		if strings.Contains(p, "$") {
			continue
		}
		full := filepath.ToSlash(filepath.Clean(filepath.Join(scriptDir, p)))
		into.addVerifiedPath(repoRoot, full, InputNote{Signal: SignalScript, Detail: "fixture"})
	}
}
