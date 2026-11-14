package main

import (
	"os"
	"strings"
	"testing"
)

// The map job saves the map under <prefix>-<checksum of the commit>. Two
// pipelines of one project share its cache, so each one gives its own prefix.
func TestPlanCommandMapKeyPrefix(t *testing.T) {
	c := newCLIRepo(t)
	c.Git("checkout", "-q", "main")

	savedKey := func(t *testing.T, extra ...string) string {
		t.Helper()
		args := append([]string{"--branch", "main", "--smarter-bin", "/opt/smarter/circleci-smarter"}, extra...)
		if code, _, stderr := doPlan(t, c.planArgs(args...)...); code != 0 {
			t.Fatalf("plan %v = %d, %q", extra, code, stderr)
		}
		cfg, err := os.ReadFile(c.path("continue.yml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(cfg), "\n") {
			if _, key, ok := strings.Cut(line, "key: "); ok && strings.Contains(key, `checksum ".smarter-key"`) {
				return strings.Trim(strings.TrimSpace(key), `"'`)
			}
		}
		t.Fatalf("no save_cache key in the traced config:\n%s", cfg)
		return ""
	}

	if got, want := savedKey(t), `smarter-impact-v1-{{ checksum ".smarter-key" }}`; got != want {
		t.Errorf("default key = %q, want %q", got, want)
	}
	if got, want := savedKey(t, "--map-key-prefix", "smarter-impact-v1-demo"), `smarter-impact-v1-demo-{{ checksum ".smarter-key" }}`; got != want {
		t.Errorf("key with a prefix = %q, want %q", got, want)
	}
}

func TestPlanCommandMapKeyPrefixMustBeUsable(t *testing.T) {
	c := newCLIRepo(t)
	for _, prefix := range []string{"", "with space", "{{ checksum \"x\" }}", "-lead", "a,b"} {
		code, _, stderr := doPlan(t, c.planArgs("--map-key-prefix", prefix)...)
		if code != 2 || !strings.Contains(stderr, "map key prefix") {
			t.Errorf("--map-key-prefix %q = %d, %q; want exit 2 and a map key prefix message", prefix, code, stderr)
		}
	}
}

func TestPlanCommandHelpListsTheMapKeyPrefix(t *testing.T) {
	c := newCLIRepo(t)
	code, _, stderr := doPlan(t, c.planArgs("-h")...)
	if code != 0 || !strings.Contains(stderr, "-map-key-prefix") || !strings.Contains(stderr, "smarter-impact-v1") {
		t.Fatalf("plan -h = %d, %q", code, stderr)
	}
}
