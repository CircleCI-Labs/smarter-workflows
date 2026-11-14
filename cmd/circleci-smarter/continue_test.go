package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	continueTestKey    = "test-continuation-key-7b3a91c2"
	continueTestConfig = "version: 2.1\n# CONFIG-MARKER\nworkflows:\n  ci:\n    jobs: [build]\n"
)

// continueAPI is a server that answers with the statuses in turn (the last one
// repeats), writes the request back in every response body, and records the
// requests.
type continueAPI struct {
	*httptest.Server
	mu       sync.Mutex
	statuses []int
	bodies   [][]byte
}

func newContinueAPI(t *testing.T, statuses ...int) *continueAPI {
	t.Helper()
	api := &continueAPI{statuses: statuses}
	api.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		api.mu.Lock()
		n := len(api.bodies)
		api.bodies = append(api.bodies, body)
		status := api.statuses[min(n, len(api.statuses)-1)]
		api.mu.Unlock()
		w.WriteHeader(status)
		// A careless server: it quotes the request, key and all.
		w.Write(body)
		io.WriteString(w, " SERVER-SAYS")
	}))
	t.Cleanup(api.Close)
	return api
}

func (a *continueAPI) requests() [][]byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([][]byte(nil), a.bodies...)
}

// fastRetries makes the retries of `continue` wait next to nothing.
func fastRetries(t *testing.T) {
	t.Helper()
	old := continueBackoff
	continueBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { continueBackoff = old })
}

func continueEnv(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func continueRun(env map[string]string, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = runContinue(args, &out, &errOut, continueEnv(env))
	return code, out.String(), errOut.String()
}

func continueWriteFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertNoSecrets(t *testing.T, what, text string) {
	t.Helper()
	for _, secret := range []string{continueTestKey, "CONFIG-MARKER", "SERVER-SAYS"} {
		if strings.Contains(text, secret) {
			t.Errorf("%s contains %q: %s", what, secret, text)
		}
	}
}

func TestContinueSendsTheConfigAndPrintsOnlyTheStatus(t *testing.T) {
	api := newContinueAPI(t, http.StatusOK)
	config := continueWriteFile(t, "continue.yml", continueTestConfig)
	params := continueWriteFile(t, "params.json", `{"deploy": true, "count": 12345678901234567890, "name": "x"}`)

	code, stdout, stderr := continueRun(map[string]string{
		"CIRCLE_CONTINUATION_KEY": continueTestKey,
		"CIRCLECI_API_HOST":       api.URL,
	}, "--config", config, "--parameters", params)

	if code != 0 || stdout != "continue: HTTP 200\n" || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want 0, the status and nothing else", code, stdout, stderr)
	}
	reqs := api.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	var body struct {
		Key           string          `json:"continuation-key"`
		Configuration string          `json:"configuration"`
		Parameters    json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(reqs[0], &body); err != nil {
		t.Fatal(err)
	}
	if body.Key != continueTestKey || body.Configuration != continueTestConfig {
		t.Errorf("key or configuration are not what was sent: %+v", body)
	}
	var params2 map[string]json.RawMessage
	if err := json.Unmarshal(body.Parameters, &params2); err != nil {
		t.Fatal(err)
	}
	if string(params2["deploy"]) != "true" || string(params2["count"]) != "12345678901234567890" || string(params2["name"]) != `"x"` {
		t.Errorf("parameters = %s", body.Parameters)
	}
}

func TestContinueWithoutParameters(t *testing.T) {
	api := newContinueAPI(t, http.StatusOK)
	config := continueWriteFile(t, "continue.yml", continueTestConfig)
	code, _, stderr := continueRun(map[string]string{
		"CIRCLE_CONTINUATION_KEY": continueTestKey,
		"CIRCLECI_API_HOST":       api.URL,
	}, "--config", config)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(string(api.requests()[0]), `"parameters":{}`) {
		t.Errorf("body = %s", api.requests()[0])
	}
}

func TestContinueRetriesAndSucceeds(t *testing.T) {
	fastRetries(t)
	api := newContinueAPI(t, 503, 429, 200)
	config := continueWriteFile(t, "continue.yml", continueTestConfig)
	code, stdout, stderr := continueRun(map[string]string{
		"CIRCLE_CONTINUATION_KEY": continueTestKey,
		"CIRCLECI_API_HOST":       api.URL,
	}, "--config", config)
	if code != 0 || stdout != "continue: HTTP 200\n" || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if n := len(api.requests()); n != 3 {
		t.Errorf("%d requests, want 3", n)
	}
}

// Whatever happens, the key, the configuration and what the server said stay
// out of both outputs.
func TestContinueNeverPrintsTheKeyOrTheBody(t *testing.T) {
	fastRetries(t)
	config := continueWriteFile(t, "continue.yml", continueTestConfig)

	for _, status := range []int{200, 400, 401, 404, 429, 500, 503} {
		t.Run(fmt.Sprint("server answers ", status), func(t *testing.T) {
			api := newContinueAPI(t, status)
			code, stdout, stderr := continueRun(map[string]string{
				"CIRCLE_CONTINUATION_KEY": continueTestKey,
				"CIRCLECI_API_HOST":       api.URL,
			}, "--config", config)
			assertNoSecrets(t, "stdout", stdout)
			assertNoSecrets(t, "stderr", stderr)
			if status == 200 {
				if code != 0 {
					t.Fatalf("code = %d, want 0", code)
				}
				return
			}
			if code != 1 || !strings.Contains(stderr, fmt.Sprintf("HTTP %d", status)) {
				t.Fatalf("code=%d stderr=%q, want 1 and the status", code, stderr)
			}
		})
	}

	t.Run("nothing is listening", func(t *testing.T) {
		api := newContinueAPI(t, 200)
		url := api.URL
		api.Close()
		code, stdout, stderr := continueRun(map[string]string{
			"CIRCLE_CONTINUATION_KEY": continueTestKey,
			"CIRCLECI_API_HOST":       url,
		}, "--config", config)
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		assertNoSecrets(t, "stdout", stdout)
		assertNoSecrets(t, "stderr", stderr)
	})

	t.Run("a host that is refused", func(t *testing.T) {
		for _, host := range []string{"http://ci.example.com", "https://user:" + continueTestKey + "@ci.example.com", "ci.example.com"} {
			code, stdout, stderr := continueRun(map[string]string{
				"CIRCLE_CONTINUATION_KEY": continueTestKey,
				"CIRCLECI_API_HOST":       host,
			}, "--config", config)
			if code != 1 {
				t.Fatalf("host %q: code = %d, want 1", host, code)
			}
			assertNoSecrets(t, "stdout", stdout)
			assertNoSecrets(t, "stderr", stderr)
		}
	})
}

func TestContinueGivesUpOn5xx(t *testing.T) {
	fastRetries(t)
	api := newContinueAPI(t, 502)
	config := continueWriteFile(t, "continue.yml", continueTestConfig)
	code, stdout, stderr := continueRun(map[string]string{
		"CIRCLE_CONTINUATION_KEY": continueTestKey,
		"CIRCLECI_API_HOST":       api.URL,
	}, "--config", config)
	if code != 1 || stdout != "" || stderr != "continue: the continuation API answered HTTP 502\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if n := len(api.requests()); n != 4 {
		t.Errorf("%d requests, want 4 (one try and three retries)", n)
	}
}

func TestContinueRefusesAConfigOverTheLimit(t *testing.T) {
	api := newContinueAPI(t, 200)
	config := continueWriteFile(t, "big.yml", strings.Repeat("#", 8<<20+1))
	code, stdout, stderr := continueRun(map[string]string{
		"CIRCLE_CONTINUATION_KEY": continueTestKey,
		"CIRCLECI_API_HOST":       api.URL,
	}, "--config", config)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "over the limit") {
		t.Fatalf("code=%d stdout=%q stderr=%.200q", code, stdout, stderr)
	}
	if len(stderr) > 300 || strings.Contains(stderr, "####") {
		t.Errorf("stderr quotes the configuration: %.200q", stderr)
	}
	if n := len(api.requests()); n != 0 {
		t.Errorf("%d requests, want none", n)
	}
}

func TestContinueErrors(t *testing.T) {
	api := newContinueAPI(t, 200)
	config := continueWriteFile(t, "continue.yml", continueTestConfig)
	env := map[string]string{"CIRCLE_CONTINUATION_KEY": continueTestKey, "CIRCLECI_API_HOST": api.URL}

	tests := []struct {
		name string
		env  map[string]string
		args []string
		code int
		want string // in stderr
	}{
		{"no key", map[string]string{"CIRCLECI_API_HOST": api.URL}, []string{"--config", config}, 1, "CIRCLE_CONTINUATION_KEY is not set"},
		{"missing config file", env, []string{"--config", filepath.Join(t.TempDir(), "nope.yml")}, 1, "continue: "},
		{"empty config file", env, []string{"--config", continueWriteFile(t, "empty.yml", "")}, 1, "the configuration is empty"},
		{"missing parameters file", env, []string{"--config", config, "--parameters", filepath.Join(t.TempDir(), "nope.json")}, 1, "continue: "},
		{"parameters that are a list", env, []string{"--config", config, "--parameters", continueWriteFile(t, "p.json", "[1]")}, 1, "must be a JSON object"},
		{"parameters that are not json", env, []string{"--config", config, "--parameters", continueWriteFile(t, "p.json", "a: b")}, 1, "must be a JSON object"},
		{"two parameter objects", env, []string{"--config", config, "--parameters", continueWriteFile(t, "p.json", "{} {}")}, 1, "single JSON object"},
		{"no --config", env, nil, 2, "--config is required"},
		{"an extra argument", env, []string{"--config", config, "extra"}, 2, "unexpected argument"},
		{"an unknown flag", env, []string{"--config", config, "--key", "x"}, 2, "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := continueRun(tt.env, tt.args...)
			if code != tt.code || stdout != "" || !strings.Contains(stderr, tt.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q; want code %d and %q on stderr", code, stdout, stderr, tt.code, tt.want)
			}
			assertNoSecrets(t, "stderr", stderr)
		})
	}
	if n := len(api.requests()); n != 0 {
		t.Errorf("%d requests were sent, want none", n)
	}

	if code, _, _ := continueRun(env, "-h"); code != 0 {
		t.Errorf("-h: code = %d, want 0", code)
	}
}

// TestContinueProcess runs the command in a process of its own, as the
// planner's setup job does, and looks at what really reaches stdout and stderr.
func TestContinueProcess(t *testing.T) {
	if os.Getenv("SMARTER_CONTINUE_HELPER") == "1" {
		continueBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
		os.Args = []string{"circleci-smarter", "continue", "--config", os.Getenv("SMARTER_CONTINUE_CONFIG")}
		main() // exits
		return
	}

	config := continueWriteFile(t, "continue.yml", continueTestConfig)
	for _, statuses := range [][]int{{200}, {400}, {503}} {
		t.Run(fmt.Sprint(statuses), func(t *testing.T) {
			api := newContinueAPI(t, statuses...)
			cmd := exec.Command(os.Args[0], "-test.run=^TestContinueProcess$")
			// Only what the command needs: not the environment of the test.
			cmd.Env = []string{
				"SMARTER_CONTINUE_HELPER=1",
				"SMARTER_CONTINUE_CONFIG=" + config,
				"CIRCLE_CONTINUATION_KEY=" + continueTestKey,
				"CIRCLECI_API_HOST=" + api.URL,
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()

			assertNoSecrets(t, "stdout", stdout.String())
			assertNoSecrets(t, "stderr", stderr.String())
			if statuses[0] == 200 {
				if err != nil || stdout.String() != "continue: HTTP 200\n" || stderr.Len() != 0 {
					t.Fatalf("err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				}
				return
			}
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 {
				t.Fatalf("err = %v, want exit status 1", err)
			}
			if want := fmt.Sprintf("HTTP %d", statuses[0]); !strings.Contains(stderr.String(), want) {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
		})
	}
}
