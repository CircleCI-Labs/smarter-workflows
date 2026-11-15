package tracescript

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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

// The orb keeps a copy of each script in its source tree (orb/src/scripts), and
// its commands include them when the orb is packed. The copies must stay the
// same as the embedded ones, and the commands must run them the way the planner
// runs them.
func TestOrbIncludesTheScripts(t *testing.T) {
	cases := []struct {
		command, file, script, name, when string
	}{
		{"reset-atime", "reset-atime.sh", Reset, "smarter: reset atime", ""},
		{"collect-trace", "collect-trace.sh", Script, "smarter: collect trace", "always"},
	}
	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			copied, err := os.ReadFile("../../orb/src/scripts/" + c.file)
			if err != nil {
				t.Fatal(err)
			}
			if string(copied) != c.script {
				t.Errorf("orb/src/scripts/%s differs from internal/tracescript/%s: copy the file over it", c.file, c.file)
			}
			data, err := os.ReadFile("../../orb/src/commands/" + c.command + ".yml")
			if err != nil {
				t.Fatal(err)
			}
			var cmd struct {
				Steps []map[string]any `yaml:"steps"`
			}
			if err := yaml.Unmarshal(data, &cmd); err != nil {
				t.Fatal(err)
			}
			if len(cmd.Steps) == 0 {
				t.Fatalf("orb command %s has no steps", c.command)
			}
			run, _ := cmd.Steps[0]["run"].(map[string]any)
			if run == nil {
				t.Fatalf("the first step of %s is not a run step", c.command)
			}
			if want := "<<include(scripts/" + c.file + ")>>"; run["command"] != want {
				t.Errorf("command = %v, want %q", run["command"], want)
			}
			if run["name"] != c.name {
				t.Errorf("name = %v, want %q", run["name"], c.name)
			}
			if run["shell"] != "/bin/sh" {
				t.Errorf("shell = %v, want /bin/sh", run["shell"])
			}
			if got, _ := run["when"].(string); got != c.when {
				t.Errorf("when = %q, want %q", got, c.when)
			}
		})
	}
}
