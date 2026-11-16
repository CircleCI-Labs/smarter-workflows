// Package circlecitest helps tests call the CircleCI CLI for config validation.
package circlecitest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const requireEnv = "REQUIRE_CIRCLECI_CLI"

// RequireEnabled is true when REQUIRE_CIRCLECI_CLI=1.
func RequireEnabled() bool { return os.Getenv(requireEnv) == "1" }

var (
	probeOnce sync.Once
	probeOK   bool
)

func probeValidateToken() bool {
	probeOnce.Do(func() {
		if strings.TrimSpace(os.Getenv("CIRCLE_TOKEN")) != "" || strings.TrimSpace(os.Getenv("CIRCLE_CLI_TOKEN")) != "" {
			probeOK = true
			return
		}
		bin, err := exec.LookPath("circleci")
		if err != nil {
			return
		}
		dir, err := os.MkdirTemp("", "smarter-cci-probe-*")
		if err != nil {
			return
		}
		defer os.RemoveAll(dir)
		file := filepath.Join(dir, "config.yml")
		cfg := []byte("version: 2.1\njobs:\n  j:\n    steps: []\nworkflows:\n  w:\n    jobs: [j]\n")
		if err := os.WriteFile(file, cfg, 0o600); err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "config", "validate", file)
		cmd.Env = append(os.Environ(), "CIRCLE_NO_UPDATE_CHECK=1", "CIRCLE_NO_TELEMETRY=1", "CIRCLE_NO_INTERACTIVE=1", "CI=true", "NO_COLOR=1")
		out, err := cmd.CombinedOutput()
		if err == nil {
			probeOK = true
			return
		}
		if needsAPIToken(string(out)) {
			return
		}
		// Any other error still means the CLI talked to the API with credentials.
		probeOK = true
	})
	return probeOK
}

func needsAPIToken(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "api token") || strings.Contains(lower, "not authenticated")
}

// ShouldRunValidate decides whether to run circleci config validate in a test.
func ShouldRunValidate(t *testing.T) bool {
	t.Helper()
	if !probeValidateToken() {
		msg := "circleci config validate needs an API token: run circleci auth login locally, or in CI attach the smarter-workflows-ci context with CIRCLE_TOKEN (fork pull requests have no project contexts)"
		if RequireEnabled() {
			t.Fatalf("%s: set %s=1", msg, requireEnv)
		}
		t.Skip(msg)
		return false
	}
	return true
}

// FindCLI returns the circleci binary path, or skips/fails per REQUIRE_CIRCLECI_CLI.
func FindCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("circleci")
	if err != nil {
		if RequireEnabled() {
			t.Fatalf("%s=1, but the circleci CLI is not on the PATH: %v", requireEnv, err)
		}
		t.Skip("the circleci CLI is not installed; set " + requireEnv + "=1 to make that an error")
	}
	return bin
}

// TransportFailure reports network errors from the CLI output.
func TransportFailure(out string) bool {
	for _, s := range []string{"dial tcp", "no such host", "connection refused", "i/o timeout", "network is unreachable", "TLS handshake", "Client.Timeout", "context deadline exceeded"} {
		if strings.Contains(out, s) {
			return true
		}
	}
	return false
}
