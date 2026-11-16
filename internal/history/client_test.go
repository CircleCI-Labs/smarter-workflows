package history

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

func TestClientPagination(t *testing.T) {
	page1, _ := os.ReadFile("testdata/pipelines_page1.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Circle-Token") != "test-token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/v2/project/example-org/example-repo/pipeline" {
			_, _ = w.Write(page1)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	page, err := c.Pipelines(context.Background(), "example-org/example-repo", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %d", len(page.Items))
	}
	if page.NextPageToken == "" {
		t.Fatal("expected page token")
	}
}

func TestClientRetry429And5xx(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		switch n {
		case 1:
			w.Header().Set("Retry-After", "0")
			http.Error(w, "slow down", http.StatusTooManyRequests)
		case 2:
			http.Error(w, "broken", http.StatusInternalServerError)
		default:
			fmt.Fprint(w, `{"items":[]}`)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "tok")
	c.HTTP = srv.Client()
	_, err := c.Pipelines(context.Background(), "slug", "")
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() < 3 {
		t.Fatalf("hits = %d", hits.Load())
	}
}

func TestClient401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "bad")
	_, err := c.Pipelines(context.Background(), "slug", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClient403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "bad")
	_, err := c.Pipelines(context.Background(), "slug", "")
	if err == nil || err.Error() != "API api/v2/project/slug/pipeline: unauthorized" {
		t.Fatalf("err = %v", err)
	}
}
