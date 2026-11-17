package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const reviewConfig = `version: 2.1
jobs:
  producer:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - run: echo prod
      - persist_to_workspace:
          root: .
          paths: [out]
  consumer:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - attach_workspace:
          at: .
      - run: cat out/data.txt
  docs:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - run: echo docs
workflows:
  w:
    jobs:
      - producer
      - consumer:
          requires: [producer]
      - docs
`

func TestPlanAIReviewAddsJobAndClosure(t *testing.T) {
	cleanCIEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/messages/count_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 100})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn",
			"content":     []any{map[string]any{"type": "text", "text": `{"add":[{"name":"consumer","why":"workspace consumer","confidence":0.95}]}`}},
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer srv.Close()
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)

	c := newCLIRepoWith(t, reviewConfig)
	c.writeMap(t, map[string][]string{
		"producer": {"Makefile"},
		"consumer": {"out/data.txt"},
		"docs":     {"README.md"},
	})
	c.Write("README.md", "docs\n")
	c.Commit("docs only")

	code, _, _ := doPlan(t, append(c.planArgs("--base", c.base, "--workflow", "w", "--ai-review", "on"),
		"--ai-review-model", "claude-haiku-5-5")...)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	rep := readReport(t, c)
	var producer, consumer bool
	for _, j := range rep.Jobs {
		if j.Name == "producer" && j.Run {
			producer = true
		}
		if j.Name == "consumer" && j.Run {
			consumer = true
		}
	}
	if !producer || !consumer {
		t.Fatalf("want producer+consumer after AI add, got %#v", rep.Jobs)
	}
}

func TestPlanAIReviewShadowAndNoKey(t *testing.T) {
	cleanCIEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	c := newCLIRepoWith(t, cliConfig)
	c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
	code, _, _ := doPlan(t, c.planArgs("--base", c.base, "--ai-review", "shadow")...)
	if code != 0 {
		t.Fatal(code)
	}
	rep := readReport(t, c)
	if rep.AIReview != nil && (len(rep.AIReview.Shadow) > 0 || len(rep.AIReview.Added) > 0) {
		t.Fatalf("expected no review without key, got %#v", rep.AIReview)
	}
}

func TestPlanAIReviewRemovalIgnored(t *testing.T) {
	cleanCIEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "k")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/messages/count_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 10})
			return
		}
		body := `{"add":[{"name":"tb","why":"still run","confidence":0.9},{"name":"remove-me","why":"nope","confidence":0.2}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn",
			"content":     []any{map[string]any{"type": "text", "text": body}},
			"usage":       map[string]any{},
		})
	}))
	defer srv.Close()
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	c := newCLIRepoWith(t, cliConfig)
	c.writeMap(t, map[string][]string{"ta": {"a.txt"}, "tb": {"b.txt"}})
	c.Write("a.txt", "changed\n")
	c.Commit("edit a")
	code, _, stderr := doPlan(t, c.planArgs("--base", c.base, "--ai-review", "on")...)
	if code != 0 {
		t.Fatal(code)
	}
	rep := readReport(t, c)
	if !strings.Contains(stderr, "unknown job") && rep.AIReview != nil {
		for _, j := range rep.AIReview.Added {
			if j.Name == "remove-me" {
				t.Fatal("unknown job should not be added")
			}
		}
	}
	_ = os.Getenv
}
