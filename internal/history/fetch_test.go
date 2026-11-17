package history

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/backtest"
	"github.com/CircleCI-Labs/smarter-workflows/internal/diff/difftest"
)

func TestFetchHTTPServer(t *testing.T) {
	page1 := mustReadFile(t, "testdata/pipelines_page1.json")
	jobs1 := mustReadFile(t, "testdata/jobs_page.json")

	repo := difftest.New(t)
	sha := repo.Commit("one")
	repo.Write("readme.md", "hello")
	sha = repo.Commit("two")
	setupOriginMain(t, repo, sha)

	page1 = []byte(strings.ReplaceAll(string(page1), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", sha))
	page1 = []byte(strings.ReplaceAll(string(page1), "pipe-1", "pipe-local"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Circle-Token") == "" {
			http.Error(w, "missing", http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/pipeline"):
			w.Write(page1)
		case strings.HasSuffix(r.URL.Path, "/pipeline/pipe-local/workflow"):
			w.Write([]byte(`{"items":[{"id":"wf-1","name":"ci","created_at":"2026-01-01T00:00:00Z"}]}`))
		case strings.HasSuffix(r.URL.Path, "/workflow/wf-1/job"):
			w.Write(jobs1)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var buf bytes.Buffer
	client := NewClient(srv.URL, "token")
	client.HTTP = srv.Client()
	_, err := Fetch(context.Background(), client, FetchOptions{
		ProjectSlug: "example-org/example-repo",
		Workflow:    "ci",
		Limit:       1,
		RepoDir:     repo.Dir,
	}, &buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	var list []backtest.CommitHistory
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("commits = %d", len(list))
	}
	if list[0].Commit != sha {
		t.Fatalf("commit = %q want %q", list[0].Commit, sha)
	}
}

func TestFetchFlakyRerunAPI(t *testing.T) {
	wf := mustReadFile(t, "testdata/workflows_ci.json")
	jobsR1 := mustReadFile(t, "testdata/jobs_rerun_first.json")
	jobsR2 := mustReadFile(t, "testdata/jobs_rerun_second.json")

	repo := difftest.New(t)
	sha := repo.Commit("init")
	setupOriginMain(t, repo, sha)

	page := []byte(`{"items":[{"id":"p1","vcs":{"revision":"` + sha + `","branch":"main"}}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pipeline"):
			w.Write(page)
		case strings.HasSuffix(r.URL.Path, "/pipeline/p1/workflow"):
			w.Write(wf)
		case strings.HasSuffix(r.URL.Path, "/workflow/wf-1/job"):
			w.Write(jobsR1)
		case strings.HasSuffix(r.URL.Path, "/workflow/wf-2/job"):
			w.Write(jobsR2)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var buf bytes.Buffer
	client := NewClient(srv.URL, "token")
	client.HTTP = srv.Client()
	_, err := Fetch(context.Background(), client, FetchOptions{
		ProjectSlug: "example-org/repo",
		Workflow:    "ci",
		Limit:       1,
		RepoDir:     repo.Dir,
	}, &buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	var list []backtest.CommitHistory
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || len(list[0].Jobs) != 1 {
		t.Fatalf("jobs = %#v", list)
	}
	j := list[0].Jobs[0]
	if j.Name != "unit-test" || !j.Flaky || j.Status != "success" {
		t.Fatalf("job = %#v", j)
	}
}

func TestFetchGoldenParity(t *testing.T) {
	repo := difftest.New(t)
	base := repo.Commit("base")
	setupOriginMain(t, repo, base)
	repo.Write("pkg/a.go", "package a\n")
	sha := repo.Commit("change")

	page := []byte(`{"items":[{"id":"golden-pipe","vcs":{"revision":"` + sha + `","branch":"main"}}]}`)
	jobs := []byte(`{"items":[{"name":"lint","type":"build","status":"success","duration":45000},{"name":"test","type":"build","status":"failed","duration":120000}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pipeline"):
			w.Write(page)
		case strings.HasSuffix(r.URL.Path, "/workflow"):
			w.Write([]byte(`{"items":[{"id":"w1","name":"ci","created_at":"2026-01-01T00:00:00Z"}]}`))
		case strings.HasSuffix(r.URL.Path, "/job"):
			w.Write(jobs)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var buf bytes.Buffer
	client := NewClient(srv.URL, "tok")
	client.HTTP = srv.Client()
	_, err := Fetch(context.Background(), client, FetchOptions{
		ProjectSlug: "circleci/example-org/example-repo",
		Workflow:    "ci",
		Limit:       1,
		RepoDir:     repo.Dir,
	}, &buf, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := bytes.TrimSpace(buf.Bytes())
	wantPath := filepath.Join("testdata", "fetch_golden.json")
	if os.Getenv("UPDATE_HISTORY_GOLDEN") == "1" {
		if err := os.WriteFile(wantPath, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("missing golden (run with UPDATE_HISTORY_GOLDEN=1): %v", err)
	}
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	ga, _ := json.Marshal(g)
	wa, _ := json.Marshal(w)
	if string(ga) != string(wa) {
		t.Fatalf("golden mismatch:\n%s\n---\n%s", got, want)
	}
}

func TestFetchConcurrencyParity(t *testing.T) {
	repo := difftest.New(t)
	sha1 := repo.Commit("one")
	repo.Write("a.txt", "1")
	sha2 := repo.Commit("two")
	setupOriginMain(t, repo, sha2)

	page := []byte(`{"items":[` +
		`{"id":"p2","vcs":{"revision":"` + sha2 + `","branch":"main"}},` +
		`{"id":"p1","vcs":{"revision":"` + sha1 + `","branch":"main"}}` +
		`]}`)
	jobs := []byte(`{"items":[{"name":"lint","type":"build","status":"success","duration":1000}]}`)
	wf := []byte(`{"items":[{"id":"w1","name":"ci","created_at":"2026-01-01T00:00:00Z"}]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pipeline"):
			w.Write(page)
		case strings.HasSuffix(r.URL.Path, "/workflow"):
			w.Write(wf)
		case strings.HasSuffix(r.URL.Path, "/job"):
			w.Write(jobs)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	fetch := func(conc int) []byte {
		var buf bytes.Buffer
		client := NewClient(srv.URL, "tok")
		client.HTTP = srv.Client()
		if _, err := Fetch(context.Background(), client, FetchOptions{
			ProjectSlug: "org/repo",
			Workflow:    "ci",
			Limit:       2,
			RepoDir:     repo.Dir,
			Concurrency: conc,
		}, &buf, nil); err != nil {
			t.Fatal(err)
		}
		return bytes.TrimSpace(buf.Bytes())
	}
	one, four := fetch(1), fetch(4)
	if string(one) != string(four) {
		t.Fatalf("sequential vs concurrent mismatch:\n%s\n---\n%s", one, four)
	}
}

func setupOriginMain(t *testing.T, repo *difftest.Repo, sha string) {
	t.Helper()
	difftest.RunGit(t, repo.Dir, nil, "update-ref", "refs/remotes/origin/main", sha)
	difftest.RunGit(t, repo.Dir, nil, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
}

func mustReadFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
