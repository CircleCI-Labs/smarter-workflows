package tracescript

import (
	"regexp"
	"strings"
	"testing"
)

// The planner inlines both scripts as the command of a CircleCI run step. These
// checks keep them safe to inline: the emitted YAML stays a readable block
// scalar, and CircleCI does not read part of a script as its own syntax.
func TestEmbeddedScriptsAreSafeToInline(t *testing.T) {
	trailing := regexp.MustCompile(`(?m)[ \t]+$`)
	scripts := map[string]string{
		"reset-atime.sh":   Reset,
		"collect-trace.sh": Script,
	}
	for name, s := range scripts {
		t.Run(name, func(t *testing.T) {
			if !strings.HasPrefix(s, "#!/bin/sh\n") {
				t.Error("must start with #!/bin/sh")
			}
			if !strings.HasSuffix(s, "\n") || strings.HasSuffix(s, "\n\n") {
				t.Error("must end with exactly one newline")
			}
			if strings.Contains(s, "\t") {
				t.Error("has a tab (a YAML emitter would quote the whole script)")
			}
			if loc := trailing.FindStringIndex(s); loc != nil {
				t.Errorf("has trailing whitespace at byte %d", loc[0])
			}
			for i := 0; i < len(s); i++ {
				if c := s[i]; c != '\n' && (c < 0x20 || c > 0x7e) {
					t.Errorf("has a control or non-ASCII byte 0x%02x at %d", c, i)
					break
				}
			}
			for _, bad := range []string{"<<", ">>", "{{", "}}"} {
				if strings.Contains(s, bad) {
					t.Errorf("contains %q, which CircleCI may read as its own syntax", bad)
				}
			}
			if !strings.Contains(s, "\nexit 0\n") {
				t.Error("must end by exiting 0: it never fails the job")
			}
		})
	}
}
