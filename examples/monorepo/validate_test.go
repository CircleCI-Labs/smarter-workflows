package monorepo_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CircleCI-Labs/smarter-workflows/internal/circlecitest"
)

// Every config the planner generates must be valid CircleCI 2.1. The check is
// the CLI's own: `circleci config validate`, which sends the config to the
// CircleCI API to be compiled, so it needs network access and an API token.

func validate(t *testing.T, bin, name string, cfg []byte) {
	t.Helper()
	if !circlecitest.ShouldRunValidate(t) {
		return
	}
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
	outStr := string(out)
	if circlecitest.TransportFailure(outStr) {
		msg := "the CircleCI API is not reachable (validate needs network access): " + strings.TrimSpace(outStr)
		if circlecitest.RequireEnabled() {
			t.Errorf("%s=1, but %s", "REQUIRE_CIRCLECI_CLI", msg)
			return
		}
		t.Skip(msg)
	}
	t.Errorf("%s is not valid CircleCI config: %v\n%s", name, err, out)
}

func TestEmittedConfigsValidate(t *testing.T) {
	bin := circlecitest.FindCLI(t)
	t.Run("example continue.yml", func(t *testing.T) {
		validate(t, bin, "continue.yml", []byte(exampleConfig()))
	})
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			out := sc.run(t)
			validate(t, bin, sc.name, out.config)
		})
	}
}
