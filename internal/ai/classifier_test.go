package ai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/ai"
)

func messagesHandler(t *testing.T, calls *atomic.Int32, first429 bool, body func() map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages/count_tokens" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 100})
			return
		}
		var raw map[string]any
		_ = json.NewDecoder(r.Body).Decode(&raw)
		if v, ok := raw["temperature"]; ok && v != nil {
			t.Fatalf("temperature sent: %v", v)
		}
		if model, _ := raw["model"].(string); model != "claude-haiku-5-5" {
			t.Fatalf("model %q", model)
		}
		outCfg, _ := raw["output_config"].(map[string]any)
		if outCfg == nil {
			t.Fatal("missing output_config")
		}
		if effort, _ := outCfg["effort"].(string); effort != "low" {
			t.Fatalf("effort %v", effort)
		}
		_, _ = io.ReadAll(r.Body)
		n := calls.Add(1)
		if n == 1 && first429 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body())
	}
}

func TestClassifierGoodReply(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(messagesHandler(t, &calls, false, func() map[string]any {
		return map[string]any{
			"id": "msg", "type": "message", "role": "assistant",
			"stop_reason": "end_turn",
			"content":     []any{map[string]any{"type": "text", "text": `{"jobs":[{"name":"lint","inputs":["Makefile"],"confidence":0.9,"why":"make"}]}`}},
			"usage":       map[string]any{"input_tokens": 10, "output_tokens": 20},
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("all:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clf := ai.NewClassifier(ai.ClassifierConfig{
		BaseURL:  srv.URL,
		APIKey:   "test",
		RepoRoot: dir,
		Model:    "claude-haiku-5-5",
		JobNames: map[string]struct{}{"lint": {}},
	})
	res, _, err := clf.ClassifyJobs(context.Background(), "system", []ai.Batch{{
		UserMessage: "{}",
		RequestJSON: []byte("{}"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res["lint"].Inputs) != 1 {
		t.Fatalf("inputs %v", res)
	}
}

func TestClassifier429Then200(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(messagesHandler(t, &calls, true, func() map[string]any {
		return map[string]any{
			"stop_reason": "end_turn",
			"content":     []any{map[string]any{"type": "text", "text": `{"jobs":[]}`}},
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		}
	}))
	defer srv.Close()
	clf := ai.NewClassifier(ai.ClassifierConfig{BaseURL: srv.URL, APIKey: "x", RepoRoot: t.TempDir(), Model: "claude-haiku-5-5"})
	_, _, err := clf.ClassifyJobs(context.Background(), "s", []ai.Batch{{UserMessage: "{}", RequestJSON: []byte("{}")}})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 2 {
		t.Fatalf("expected retry, calls=%d", calls.Load())
	}
}

func TestClassifierRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "count_tokens") {
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 1})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "refusal",
			"content":     []any{map[string]any{"type": "text", "text": "{}"}},
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer srv.Close()
	clf := ai.NewClassifier(ai.ClassifierConfig{BaseURL: srv.URL, APIKey: "x", RepoRoot: t.TempDir(), Model: "claude-haiku-5-5"})
	res, warns, err := clf.ClassifyJobs(context.Background(), "s", []ai.Batch{{UserMessage: "{}", RequestJSON: []byte("{}")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 || len(warns) == 0 {
		t.Fatalf("res=%v warns=%v", res, warns)
	}
}

func TestClassifier401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"bad"}}`))
	}))
	defer srv.Close()
	clf := ai.NewClassifier(ai.ClassifierConfig{BaseURL: srv.URL, APIKey: "x", RepoRoot: t.TempDir()})
	_, _, err := clf.ClassifyJobs(context.Background(), "s", []ai.Batch{{UserMessage: "{}", RequestJSON: []byte("{}")}})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("err %v", err)
	}
}

func TestValidateDropsBadPaths(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("x"), 0o644)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "count_tokens") {
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 1})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn",
			"content":     []any{map[string]any{"type": "text", "text": `{"jobs":[{"name":"j","inputs":["/abs","../x","missing","ok.txt"],"confidence":0.9,"why":"x"}]}`}},
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer srv.Close()
	clf := ai.NewClassifier(ai.ClassifierConfig{BaseURL: srv.URL, APIKey: "x", RepoRoot: dir, Model: "claude-haiku-5-5", JobNames: map[string]struct{}{"j": {}}})
	res, _, err := clf.ClassifyJobs(context.Background(), "s", []ai.Batch{{UserMessage: "{}", RequestJSON: []byte("{}")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res["j"].Inputs) != 1 || res["j"].Confidence != 0 {
		t.Fatalf("got %+v", res["j"])
	}
}

func TestDenyContent(t *testing.T) {
	if !ai.DenyContent(".env.production") || !ai.DenyContent("foo.tfvars") {
		t.Fatal("deny names")
	}
	if ai.DenyFileBody([]byte("ghp_abcdefghijklmnopqrst")) {
		// ok
	} else {
		t.Fatal("deny body")
	}
}
