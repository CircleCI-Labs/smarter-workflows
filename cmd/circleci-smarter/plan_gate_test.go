package main

import (
	"os"
	"strings"
	"testing"
)

// The gate is a no-op job, so it has no image. --map-image sets the image of
// the impact-map job, and --gate-image, which used to set the image of both
// jobs, is a deprecated alias of it.
func TestPlanCommandMapImageAndTheDeprecatedGateImage(t *testing.T) {
	c := newCLIRepo(t)
	c.Git("checkout", "-q", "main")

	plan := func(t *testing.T, extra ...string) (config, stderr string) {
		t.Helper()
		args := append([]string{"--branch", "main", "--smarter-bin", "/opt/smarter/circleci-smarter"}, extra...)
		code, _, stderr := doPlan(t, c.planArgs(args...)...)
		if code != 0 {
			t.Fatalf("plan %v = %d, %q", extra, code, stderr)
		}
		cfg, err := os.ReadFile(c.path("continue.yml"))
		if err != nil {
			t.Fatal(err)
		}
		return string(cfg), stderr
	}

	t.Run("the gate has no image whatever the flags say", func(t *testing.T) {
		cfg, stderr := plan(t, "--map-image", "cimg/base:2026.10")
		if !strings.Contains(cfg, "  smarter-gate:\n    type: no-op\n") {
			t.Errorf("the gate is not a no-op job:\n%s", cfg)
		}
		if got := strings.Count(cfg, "cimg/base:2026.10"); got != 1 {
			t.Errorf("--map-image appears %d times in the config, want once (the map job)", got)
		}
		if strings.Contains(stderr, "deprecated") {
			t.Errorf("--map-image warns that it is deprecated: %q", stderr)
		}
	})

	t.Run("--gate-image still sets the map job's image, and says it is deprecated", func(t *testing.T) {
		cfg, stderr := plan(t, "--gate-image", "cimg/base:2026.10")
		if !strings.Contains(cfg, "  smarter-impact-map:\n    docker:\n      - image: cimg/base:2026.10\n") {
			t.Errorf("the map job does not use the image:\n%s", cfg)
		}
		if !strings.Contains(stderr, "--gate-image is deprecated") {
			t.Errorf("stderr = %q, want a deprecation warning", stderr)
		}
	})

	t.Run("a map image with a space is a usage error", func(t *testing.T) {
		code, _, stderr := doPlan(t, c.planArgs("--map-image", "has space")...)
		if code != 2 || !strings.Contains(stderr, "map image") {
			t.Errorf("--map-image with a space = %d, %q; want exit 2 and a map image message", code, stderr)
		}
	})
}
