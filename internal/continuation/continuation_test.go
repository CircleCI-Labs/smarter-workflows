package continuation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testKey    = "test-continuation-key-0f9e8d7c" // gitleaks:allow (fake key for tests)
	testConfig = "version: 2.1\n# CONFIG-MARKER\nworkflows:\n  ci:\n    jobs: [build]\n"
)

// fakeAPI is a server that answers with the given statuses in turn (the last
// one repeats), and records what it received.
type fakeAPI struct {
	*httptest.Server

	mu       sync.Mutex
	statuses []int
	bodies   [][]byte
	paths    []string
	methods  []string
	headers  []http.Header
	// echo, if set, is written as the response body.
	echo func(r *http.Request, body []byte) string
}

func newFakeAPI(t *testing.T, statuses ...int) *fakeAPI {
	t.Helper()
	f := &fakeAPI{statuses: statuses}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		n := len(f.bodies)
		f.bodies = append(f.bodies, body)
		f.paths = append(f.paths, r.URL.Path)
		f.methods = append(f.methods, r.Method)
		f.headers = append(f.headers, r.Header.Clone())
		status := f.statuses[min(n, len(f.statuses)-1)]
		f.mu.Unlock()
		w.WriteHeader(status)
		if f.echo != nil {
			io.WriteString(w, f.echo(r, body))
		} else {
			io.WriteString(w, `{"message":"ok"}`)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPI) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

func (f *fakeAPI) body(i int) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[i]
}

func (f *fakeAPI) method(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.methods[i]
}

func (f *fakeAPI) path(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paths[i]
}

func (f *fakeAPI) header(i int) http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.headers[i]
}

// client returns a client of the fake that waits for no time: it records the
// waits instead.
func (f *fakeAPI) client() (*Client, *[]time.Duration) {
	var waits []time.Duration
	c := &Client{Host: f.URL, Key: testKey}
	c.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	return c, &waits
}

func TestContinueSendsTheDocumentedRequest(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	c, waits := api.client()
	c.UserAgent = "circleci-smarter/test"

	status, err := c.Continue(context.Background(), []byte(testConfig), map[string]any{"deploy": true, "count": 3, "name": "x"})
	if err != nil || status != http.StatusOK {
		t.Fatalf("Continue = %d, %v; want 200, nil", status, err)
	}
	if api.requests() != 1 || len(*waits) != 0 {
		t.Fatalf("%d requests and %d waits, want 1 and 0", api.requests(), len(*waits))
	}
	if api.method(0) != http.MethodPost || api.path(0) != "/api/v2/pipeline/continue" {
		t.Errorf("request was %s %s", api.method(0), api.path(0))
	}
	if got := api.header(0).Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := api.header(0).Get("User-Agent"); got != "circleci-smarter/test" {
		t.Errorf("User-Agent = %q", got)
	}

	var body map[string]any
	if err := json.Unmarshal(api.body(0), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"continuation-key": testKey,
		"configuration":    testConfig,
		"parameters":       map[string]any{"deploy": true, "count": float64(3), "name": "x"},
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v, want %v", body, want)
	}
}

func TestContinueWithoutParametersSendsAnEmptyObject(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	c, _ := api.client()
	if _, err := c.Continue(context.Background(), []byte(testConfig), nil); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Parameters map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(api.body(0), &body); err != nil {
		t.Fatal(err)
	}
	if body.Parameters == nil || len(body.Parameters) != 0 {
		t.Fatalf("parameters = %#v, want {}", body.Parameters)
	}
	if !strings.Contains(string(api.body(0)), `"parameters":{}`) {
		t.Fatalf("body = %s", api.body(0))
	}
}

func TestContinueKeepsTheConfigurationAsItIs(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	c, _ := api.client()
	config := "when: << pipeline.parameters.run >> & <b>\n"
	if _, err := c.Continue(context.Background(), []byte(config), nil); err != nil {
		t.Fatal(err)
	}
	// encoding/json escapes < > & as \u003c \u003e \u0026 unless told not to.
	backslash := string(rune(92))
	for _, escaped := range []string{backslash + "u003c", backslash + "u003e", backslash + "u0026"} {
		if strings.Contains(string(api.body(0)), escaped) {
			t.Errorf("the body escapes HTML characters (%s): %s", escaped, api.body(0))
		}
	}
	var body struct{ Configuration string }
	if err := json.Unmarshal(api.body(0), &body); err != nil || body.Configuration != config {
		t.Errorf("configuration = %q, %v; want %q", body.Configuration, err, config)
	}
}

func TestContinueRetriesOn429And5xx(t *testing.T) {
	api := newFakeAPI(t, 503, 429, 500, 200)
	c, waits := api.client()

	status, err := c.Continue(context.Background(), []byte(testConfig), nil)
	if err != nil || status != 200 {
		t.Fatalf("Continue = %d, %v; want 200, nil", status, err)
	}
	if api.requests() != 4 {
		t.Errorf("%d requests, want 4", api.requests())
	}
	if want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}; !reflect.DeepEqual(*waits, want) {
		t.Errorf("waits = %v, want %v", *waits, want)
	}
}

func TestContinueGivesUpAfterThreeRetries(t *testing.T) {
	for _, code := range []int{429, 500, 502, 503, 504} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			api := newFakeAPI(t, code)
			c, waits := api.client()

			status, err := c.Continue(context.Background(), []byte(testConfig), nil)
			var se *StatusError
			if !errors.As(err, &se) || se.Code != code || status != code {
				t.Fatalf("Continue = %d, %v; want a StatusError for %d", status, err, code)
			}
			if api.requests() != 4 || len(*waits) != 3 {
				t.Errorf("%d requests and %d waits, want 4 and 3", api.requests(), len(*waits))
			}
		})
	}
}

func TestContinueDoesNotRetryOtherStatuses(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 409, 422, 301, 307} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			api := newFakeAPI(t, code)
			c, waits := api.client()

			status, err := c.Continue(context.Background(), []byte(testConfig), nil)
			var se *StatusError
			if !errors.As(err, &se) || se.Code != code || status != code {
				t.Fatalf("Continue = %d, %v; want a StatusError for %d", status, err, code)
			}
			if api.requests() != 1 || len(*waits) != 0 {
				t.Errorf("%d requests and %d waits, want 1 and 0", api.requests(), len(*waits))
			}
		})
	}
}

func TestContinueFollowsNoRedirect(t *testing.T) {
	other := newFakeAPI(t, http.StatusOK)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+Path, http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()

	c := &Client{Host: redirecting.URL, Key: testKey}
	status, err := c.Continue(context.Background(), []byte(testConfig), nil)
	var se *StatusError
	if !errors.As(err, &se) || status != http.StatusTemporaryRedirect {
		t.Fatalf("Continue = %d, %v; want a StatusError for 307", status, err)
	}
	if n := other.requests(); n != 0 {
		t.Fatalf("the redirect target got %d requests, want none", n)
	}
}

func TestContinueRetriesAFailedRequest(t *testing.T) {
	// The first connection is dropped; the second works.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var waits []time.Duration
	c := &Client{Host: srv.URL, Key: testKey}
	c.sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }

	status, err := c.Continue(context.Background(), []byte(testConfig), nil)
	if err != nil || status != 200 {
		t.Fatalf("Continue = %d, %v; want 200, nil", status, err)
	}
	if calls.Load() != 2 || len(waits) != 1 {
		t.Errorf("%d calls and %d waits, want 2 and 1", calls.Load(), len(waits))
	}
}

func TestContinueTimesOut(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// Read the body, so that the server notices when the client hangs up.
		io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()

	c := &Client{
		Host:    srv.URL,
		Key:     testKey,
		Timeout: 40 * time.Millisecond,
		Backoff: []time.Duration{time.Millisecond, time.Millisecond},
	}
	start := time.Now()
	status, err := c.Continue(context.Background(), []byte(testConfig), nil)
	if err == nil || !strings.Contains(err.Error(), "timed out after 40ms") {
		t.Fatalf("Continue = %d, %v; want a timeout error", status, err)
	}
	if status != 0 {
		t.Errorf("status = %d, want 0", status)
	}
	if calls.Load() != 3 {
		t.Errorf("%d requests, want 3", calls.Load())
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %s", elapsed)
	}
}

func TestContinueStopsWhenTheContextEnds(t *testing.T) {
	api := newFakeAPI(t, 503)
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := api.client()
	c.sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}
	status, err := c.Continue(ctx, []byte(testConfig), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Continue = %d, %v; want context.Canceled", status, err)
	}
	if api.requests() != 1 {
		t.Errorf("%d requests, want 1", api.requests())
	}

	// A context that has ended before the first request stops it too.
	c2 := &Client{Host: api.URL, Key: testKey, Backoff: []time.Duration{}}
	before := api.requests()
	if _, err := c2.Continue(ctx, []byte(testConfig), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if api.requests() != before {
		t.Error("a request was sent with a context that had ended")
	}
}

func TestContinueRefusesAConfigurationOverTheLimit(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	c, _ := api.client()

	tooBig := make([]byte, MaxConfigBytes+1)
	for i := range tooBig {
		tooBig[i] = '#'
	}
	_, err := c.Continue(context.Background(), tooBig, nil)
	var big *ConfigTooLargeError
	if !errors.As(err, &big) || big.Size != MaxConfigBytes+1 {
		t.Fatalf("err = %v, want a ConfigTooLargeError of %d bytes", err, MaxConfigBytes+1)
	}
	if api.requests() != 0 {
		t.Fatalf("%d requests were sent, want none", api.requests())
	}
	if strings.Contains(err.Error(), "#") || strings.Contains(err.Error(), testKey) {
		t.Errorf("the error quotes the configuration or the key: %.80s", err)
	}

	// Exactly the limit is accepted.
	var atLimit = tooBig[:MaxConfigBytes]
	status, err := c.Continue(context.Background(), atLimit, nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("Continue(%d bytes) = %d, %v; want 200, nil", len(atLimit), status, err)
	}
	var body struct{ Configuration string }
	if err := json.Unmarshal(api.body(0), &body); err != nil || len(body.Configuration) != MaxConfigBytes {
		t.Fatalf("the server got %d bytes of configuration (%v), want %d", len(body.Configuration), err, MaxConfigBytes)
	}
}

func TestContinueNeedsAKeyAndAConfiguration(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	c, _ := api.client()

	noKey := *c
	noKey.Key = ""
	if _, err := noKey.Continue(context.Background(), []byte(testConfig), nil); !errors.Is(err, ErrNoKey) {
		t.Errorf("err = %v, want ErrNoKey", err)
	}
	if _, err := c.Continue(context.Background(), nil, nil); !errors.Is(err, ErrNoConfig) {
		t.Errorf("err = %v, want ErrNoConfig", err)
	}
	if api.requests() != 0 {
		t.Errorf("%d requests were sent, want none", api.requests())
	}
}

func TestEndpoint(t *testing.T) {
	tests := []struct {
		host string
		want string // the endpoint, or "" for an error
	}{
		{"", "https://circleci.com/api/v2/pipeline/continue"},
		{"https://circleci.com", "https://circleci.com/api/v2/pipeline/continue"},
		{"https://circleci.com/", "https://circleci.com/api/v2/pipeline/continue"},
		{"https://ci.example.com:8443", "https://ci.example.com:8443/api/v2/pipeline/continue"},
		{"https://ci.example.com/prefix/", "https://ci.example.com/prefix/api/v2/pipeline/continue"},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080/api/v2/pipeline/continue"},
		{"http://localhost:8080", "http://localhost:8080/api/v2/pipeline/continue"},
		{"http://[::1]:8080", "http://[::1]:8080/api/v2/pipeline/continue"},
		{"http://ci.example.com", ""},
		{"http://127.0.0.1.example.com", ""},
		{"ftp://ci.example.com", ""},
		{"circleci.com", ""},
		{"https://", ""},
		{"https://user:hunter2@ci.example.com", ""},
		{"://", ""},
	}
	for _, tt := range tests {
		c := &Client{Host: tt.host, Key: testKey}
		got, err := c.endpoint()
		if tt.want == "" {
			if err == nil {
				t.Errorf("endpoint(%q) = %q, want an error", tt.host, got)
			} else if strings.Contains(err.Error(), "hunter2") {
				t.Errorf("endpoint(%q) error repeats the credentials: %v", tt.host, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("endpoint(%q) = %q, %v; want %q", tt.host, got, err, tt.want)
		}
	}
}

// The key must never appear in an error, whatever the server or the network
// does, and neither must what the server says.
func TestContinueNeverRevealsTheKeyOrTheBody(t *testing.T) {
	leakyBody := func(r *http.Request, body []byte) string {
		// A server that quotes the whole request back.
		return string(body) + " SERVER-SAYS"
	}

	check := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("Continue succeeded, want an error")
		}
		for _, secret := range []string{testKey, "CONFIG-MARKER", "SERVER-SAYS"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the error contains %q: %v", secret, err)
			}
			for e := err; e != nil; e = errors.Unwrap(e) {
				if strings.Contains(e.Error(), secret) {
					t.Errorf("a wrapped error contains %q: %v", secret, e)
				}
			}
		}
	}

	for _, code := range []int{400, 401, 429, 500, 503} {
		t.Run(fmt.Sprintf("server answers %d and quotes the request", code), func(t *testing.T) {
			api := newFakeAPI(t, code)
			api.echo = leakyBody
			c, _ := api.client()
			_, err := c.Continue(context.Background(), []byte(testConfig), nil)
			check(t, err)
		})
	}

	t.Run("a transport that fails with the request body in the error", func(t *testing.T) {
		c := &Client{
			Host:    "https://ci.example.com",
			Key:     testKey,
			Backoff: []time.Duration{},
			HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(r.Body)
				return nil, fmt.Errorf("transport broke on %s", body)
			})},
		}
		_, err := c.Continue(context.Background(), []byte(testConfig), nil)
		if err == nil || !strings.Contains(err.Error(), "[redacted]") {
			t.Fatalf("err = %v, want the key redacted", err)
		}
		if strings.Contains(err.Error(), testKey) {
			t.Fatalf("the error contains the key: %v", err)
		}
		// errors.Is still sees through the redaction, and Unwrap gives away nothing.
		if errors.Unwrap(err) != nil {
			t.Errorf("Unwrap(err) = %v, want nil", errors.Unwrap(err))
		}
	})

	t.Run("redaction keeps errors.Is and errors.As working", func(t *testing.T) {
		c := &Client{Key: testKey}
		err := c.redact(fmt.Errorf("failed with %s: %w", testKey, context.Canceled))
		if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), testKey) {
			t.Fatalf("err = %v", err)
		}
		err = c.redact(fmt.Errorf("failed with %s: %w", testKey, &StatusError{Code: 502}))
		var se *StatusError
		if !errors.As(err, &se) || se.Code != 502 {
			t.Fatalf("errors.As failed on %v", err)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
