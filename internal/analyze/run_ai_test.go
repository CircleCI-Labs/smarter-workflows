package analyze_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/analyze"
)

func TestAnalyzeAIMalformedJSONAutoWarns(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "k")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/messages/count_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 1})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn",
			"content":     []any{map[string]any{"type": "text", "text": "{"}},
			"usage":       map[string]any{},
		})
	}))
	defer srv.Close()
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "continue.yml")
	writeMinConfig(t, cfg)
	_, rep, _, err := analyze.Run(context.Background(), analyze.RunOptions{
		RepoRoot:   dir,
		ConfigPath: cfg,
		Workflows:  []string{"w"},
		AIMode:     analyze.AIModeAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep == nil || len(rep.Warnings) == 0 || !strings.Contains(rep.Warnings[0], "AI classifier") {
		t.Fatalf("warnings=%v", rep.Warnings)
	}
}

func TestAnalyzeAIMalformedJSONOnFails(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "k")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/messages/count_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 1})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn",
			"content":     []any{map[string]any{"type": "text", "text": "{"}},
			"usage":       map[string]any{},
		})
	}))
	defer srv.Close()
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "continue.yml")
	writeMinConfig(t, cfg)
	_, _, _, err := analyze.Run(context.Background(), analyze.RunOptions{
		RepoRoot:   dir,
		ConfigPath: cfg,
		Workflows:  []string{"w"},
		AIMode:     analyze.AIModeOn,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("err=%v", err)
	}
}

func writeMinConfig(t *testing.T, path string) {
	t.Helper()
	yml := `version: 2.1
jobs:
  j:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - run: echo hi
workflows:
  w:
    jobs: [j]
`
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "go.mod"), []byte("module example.com/x\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
