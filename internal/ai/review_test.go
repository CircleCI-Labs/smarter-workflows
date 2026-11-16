package ai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CircleCI-Labs/smarter-workflows/internal/ai"
)

func TestReviewAddsKnownJob(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/messages/count_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 50})
			return
		}
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "m", "type": "message", "role": "assistant", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": `{"add":[{"name":"tb","why":"touches shared lib","confidence":0.9}]}`}},
			"usage":   map[string]any{"input_tokens": 10, "output_tokens": 5},
		})
	}))
	defer srv.Close()
	rev := ai.NewReviewer(ai.ReviewerConfig{BaseURL: srv.URL, APIKey: "k", Timeout: 5 * time.Second})
	res, err := rev.Review(context.Background(), ai.ReviewRequest{
		ChangedFiles: []string{"a.txt"},
		Skipped:      []ai.ReviewJob{{Name: "tb", Summary: "run tests", Inputs: "1 paths"}},
		Selected:     []ai.ReviewJob{{Name: "ta", Summary: "lint", Inputs: "2 paths"}},
	}, map[string]struct{}{"tb": {}, "ta": {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Add) != 1 || res.Add[0].Name != "tb" {
		t.Fatalf("add = %#v", res.Add)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls %d", calls.Load())
	}
}

func TestReviewIgnoresUnknownJob(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/messages/count_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 50})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn",
			"content":     []any{map[string]any{"type": "text", "text": `{"add":[{"name":"nope","why":"x","confidence":0.5}]}`}},
			"usage":       map[string]any{},
		})
	}))
	defer srv.Close()
	rev := ai.NewReviewer(ai.ReviewerConfig{BaseURL: srv.URL, APIKey: "k"})
	res, err := rev.Review(context.Background(), ai.ReviewRequest{}, map[string]struct{}{"ta": {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Add) != 0 {
		t.Fatalf("want no adds, got %#v", res.Add)
	}
}

func TestReviewTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages/count_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 50})
			return
		}
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	rev := ai.NewReviewer(ai.ReviewerConfig{BaseURL: srv.URL, APIKey: "k", Timeout: 50 * time.Millisecond})
	_, err := rev.Review(context.Background(), ai.ReviewRequest{}, nil)
	if err == nil {
		t.Fatal("want timeout error")
	}
}
