package monorepo_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every config the planner generates must be valid CircleCI 2.1. The check is
// the CLI's own: `circleci config validate`, which sends the config to the
// CircleCI API to be compiled, so it needs network access (and no token, for a
// config without private orbs).
//
// With REQUIRE_CIRCLECI_CLI=1 a missing CLI, or an API that cannot be reached,
// fails the test. Without it, those cases skip.

func requireCLI() bool { return os.Getenv("REQUIRE_CIRCLECI_CLI") == "1" }

func findCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("circleci")
	if err != nil {
		if requireCLI() {
			t.Fatalf("REQUIRE_CIRCLECI_CLI=1, but the circleci CLI is not on the PATH: %v", err)
		}
		t.Skip("the circleci CLI is not installed; set REQUIRE_CIRCLECI_CLI=1 to make that an error")
	}
	return bin
}

// transportFailure tells "the CLI could not reach the API" from "the config is invalid".
func transportFailure(out string) bool {
	for _, s := range []string{"dial tcp", "no such host", "connection refused", "i/o timeout", "network is unreachable", "TLS handshake", "Client.Timeout", "context deadline exceeded"} {
		if strings.Contains(out, s) {
			return true
		}
	}
	return false
}

func validate(t *testing.T, bin, name string, cfg []byte) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(file, cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "config", "validate", file)
	cmd.Env = append(os.Environ(), "CIRCLE_NO_UPDATE_CHECK=1", "CIRCLE_NO_TELEMETRY=1", "CIRCLE_NO_INTERACTIVE=1", "CI=true", "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	if transportFailure(string(out)) {
		msg := "the CircleCI API is not reachable (validate needs network access): " + strings.TrimSpace(string(out))
		if requireCLI() {
			t.Errorf("REQUIRE_CIRCLECI_CLI=1, but %s", msg)
			return
		}
		t.Skip(msg)
	}
	t.Errorf("%s is not valid CircleCI config: %v\n%s", name, err, out)
}

func TestEmittedConfigsValidate(t *testing.T) {
	bin := findCLI(t)
	t.Run("example continue.yml", func(t *testing.T) {
		validate(t, bin, "continue.yml", []byte(exampleConfig()))
	})
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			// The real embedded trace scripts, not the stubs of the golden files:
			// they are part of what CircleCI has to compile.
			out := sc.run(t)
			validate(t, bin, sc.name, out.config)
		})
	}
}
