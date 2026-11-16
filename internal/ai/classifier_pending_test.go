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
	"sync"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/ai"
)

func TestClassifierMaxTokensSplitsBatch(t *testing.T) {
	var (
		mu     sync.Mutex
		calls  int
		bodies []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/messages/count_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 10})
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls++
		bodies = append(bodies, string(body))
		mu.Unlock()
		payload := map[string]any{
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		}
		switch calls {
		case 1:
			payload["stop_reason"] = "max_tokens"
			payload["content"] = []any{map[string]any{"type": "text", "text": "{}"}}
		case 2:
			payload["content"] = []any{map[string]any{"type": "text", "text": `{"jobs":[{"name":"alpha","inputs":["go.mod"],"confidence":0.9,"why":"a"}]}`}}
		default:
			payload["content"] = []any{map[string]any{"type": "text", "text": `{"jobs":[{"name":"beta","inputs":["README.md"],"confidence":0.8,"why":"b"}]}`}}
		}
		_ = body
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module t\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(jobs []ai.BatchJob) (ai.Batch, error) {
		var names []string
		for _, j := range jobs {
			names = append(names, j.Name)
		}
		msg := strings.Join(names, ",")
		return ai.Batch{Jobs: jobs, UserMessage: msg, RequestJSON: []byte(msg)}, nil
	}
	clf := ai.NewClassifier(ai.ClassifierConfig{
		BaseURL:    srv.URL,
		APIKey:     "k",
		RepoRoot:   dir,
		Model:      "claude-haiku-5-5",
		JobNames:   map[string]struct{}{"alpha": {}, "beta": {}},
		BuildBatch: build,
	})
	res, _, err := clf.ClassifyJobs(context.Background(), "system", []ai.Batch{{
		Jobs:        []ai.BatchJob{{Name: "alpha"}, {Name: "beta"}},
		UserMessage: "alpha,beta",
		RequestJSON: []byte("alpha,beta"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res["alpha"].Inputs == nil || res["beta"].Inputs == nil {
		t.Fatalf("results %#v", res)
	}
	if calls < 3 {
		t.Fatalf("calls=%d bodies=%v", calls, bodies)
	}
}

func TestClassifierMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/messages/count_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 1})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn",
			"content":     []any{map[string]any{"type": "text", "text": "not json"}},
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer srv.Close()
	clf := ai.NewClassifier(ai.ClassifierConfig{BaseURL: srv.URL, APIKey: "k", RepoRoot: t.TempDir(), Model: "claude-haiku-5-5"})
	_, _, err := clf.ClassifyJobs(context.Background(), "s", []ai.Batch{{UserMessage: "{}", RequestJSON: []byte("{}")}})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("err=%v", err)
	}
}

func TestClassifierUnknownJobName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/messages/count_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 1})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn",
			"content":     []any{map[string]any{"type": "text", "text": `{"jobs":[{"name":"nope","inputs":["x"],"confidence":0.9,"why":"?"}]}`}},
			"usage":       map[string]any{},
		})
	}))
	defer srv.Close()
	clf := ai.NewClassifier(ai.ClassifierConfig{
		BaseURL:  srv.URL,
		APIKey:   "k",
		RepoRoot: t.TempDir(),
		Model:    "claude-haiku-5-5",
		JobNames: map[string]struct{}{"lint": {}},
	})
	res, warns, err := clf.ClassifyJobs(context.Background(), "s", []ai.Batch{{UserMessage: "{}", RequestJSON: []byte("{}")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Fatalf("res=%v", res)
	}
	if len(warns) == 0 || !strings.Contains(warns[0], "unknown job") {
		t.Fatalf("warns=%v", warns)
	}
}
