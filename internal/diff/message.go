package diff

import (
	"regexp"
	"strings"
)

var (
	fullTag     = regexp.MustCompile(`(?i)\[smarter full\]`)
	fullTrailer = regexp.MustCompile(`(?im)^smarter-full:[ \t]*true[ \t]*$`)
)

// AsksForFullRun reports whether a commit message forces a full run: it
// contains "[smarter full]" (any case), or its last paragraph has a
// "Smarter-Full: true" trailer.
func AsksForFullRun(message string) bool {
	if fullTag.MatchString(message) {
		return true
	}
	msg := strings.TrimSpace(strings.ReplaceAll(message, "\r\n", "\n"))
	last := msg
	if i := strings.LastIndex(msg, "\n\n"); i >= 0 {
		last = msg[i+2:]
	}
	return fullTrailer.MatchString(last)
}
