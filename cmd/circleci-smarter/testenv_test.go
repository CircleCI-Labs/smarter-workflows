package main

import (
	"os"
	"strings"
	"testing"
)

// cleanCIEnv clears CI, token, and git metadata env vars that can leak into
// plan and other CLI commands during tests.
func cleanCIEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"CIRCLE_BRANCH", "CIRCLE_TAG", "CIRCLE_SHA1", "CIRCLE_PULL_REQUEST",
		"CIRCLE_PROJECT_ID", "CIRCLE_BUILD_NUM", "CIRCLE_REPOSITORY_URL",
		"CIRCLE_TOKEN", "CIRCLECI_TOKEN", "CIRCLECI_HOST", "CIRCLECI_API_HOST",
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
	} {
		t.Setenv(k, "")
	}
	for _, kv := range os.Environ() {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(k, "CIRCLE_PR_") {
			t.Setenv(k, "")
		}
		if strings.HasPrefix(k, "GIT_") {
			t.Setenv(k, "")
		}
	}
}
