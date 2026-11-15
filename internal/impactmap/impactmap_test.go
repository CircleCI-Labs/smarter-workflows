package impactmap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func tsv(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func TestParseTrace(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    *Trace
		wantErr string
	}{
		{
			name: "full trace",
			in: tsv("# smarter-trace v1", "job\tapi-test", "node\t2", "method\tatime", "supported\ttrue",
				"F\tservices/api/main.go", "F\t./go.mod", "D\tservices/api", "D\t."),
			want: &Trace{Job: "api-test", Node: 2, Method: "atime", Supported: true,
				Files: []string{"services/api/main.go", "go.mod"}, Dirs: []string{"services/api", "."}},
		},
		{
			name: "unsupported with reason and no reads",
			in:   tsv("# smarter-trace v1", "job\tlint", "node\t0", "method\tatime", "supported\tfalse", "reason\tnoatime mount"),
			want: &Trace{Job: "lint", Method: "atime", Reason: "noatime mount"},
		},
		{
			name: "missing supported means unsupported, node defaults to 0",
			in:   tsv("# smarter-trace v1", "job\tlint", "F\ta.txt"),
			want: &Trace{Job: "lint", Method: "atime", Files: []string{"a.txt"}},
		},
		{
			name: "crlf, comments, blank lines and unknown keys are tolerated",
			in:   "\n# smarter-trace v1\r\njob\tdocs\r\n# note\r\n\r\nfuture\tvalue\r\nsupported\ttrue\r\nF\tdocs/a.md\r\n",
			want: &Trace{Job: "docs", Method: "atime", Supported: true, Files: []string{"docs/a.md"}},
		},
		{
			name: "job name with spaces and a tab-free path with spaces",
			in:   tsv("# smarter-trace v1", "job\tbuild linux", "supported\ttrue", "F\tdir with space/a b.txt"),
			want: &Trace{Job: "build linux", Method: "atime", Supported: true, Files: []string{"dir with space/a b.txt"}},
		},
		{name: "no header", in: tsv("job\tx"), wantErr: "want header"},
		{name: "wrong header version", in: tsv("# smarter-trace v2", "job\tx"), wantErr: "want header"},
		{name: "empty input", in: "", wantErr: "missing header"},
		{name: "no job", in: tsv("# smarter-trace v1", "supported\ttrue"), wantErr: "no job line"},
		{name: "duplicate job", in: tsv("# smarter-trace v1", "job\ta", "job\tb"), wantErr: `duplicate "job"`},
		{name: "bad node", in: tsv("# smarter-trace v1", "job\ta", "node\tx"), wantErr: "bad node"},
		{name: "negative node", in: tsv("# smarter-trace v1", "job\ta", "node\t-1"), wantErr: "bad node"},
		{name: "bad supported", in: tsv("# smarter-trace v1", "job\ta", "supported\tyes"), wantErr: "supported must be"},
		{name: "line without tab", in: tsv("# smarter-trace v1", "job a"), wantErr: "tab"},
		{name: "absolute file path", in: tsv("# smarter-trace v1", "job\ta", "F\t/etc/passwd"), wantErr: "absolute"},
		{name: "path escapes the repo", in: tsv("# smarter-trace v1", "job\ta", "F\t../x"), wantErr: "leaves the repository"},
		{name: "dot is not a file", in: tsv("# smarter-trace v1", "job\ta", "F\t."), wantErr: "not a file"},
		{name: "empty path", in: tsv("# smarter-trace v1", "job\ta", "F\t"), wantErr: "unusable path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTrace(strings.NewReader(tc.in))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestMerge(t *testing.T) {
	now := time.Date(2026, 11, 12, 18, 30, 0, 0, time.UTC)
	ok := func(job string, node int, files, dirs []string) *Trace {
		return &Trace{Job: job, Node: node, Method: "atime", Supported: true, Files: files, Dirs: dirs}
	}
	tests := []struct {
		name   string
		traces []*Trace
		want   map[string]*Job
		method string
	}{
		{
			name: "union over nodes, sorted and deduplicated",
			traces: []*Trace{
				ok("test", 1, []string{"b.go", "a.go"}, []string{"pkg"}),
				ok("test", 0, []string{"a.go", "c.go"}, []string{".", "pkg"}),
			},
			want: map[string]*Job{"test": {Files: []string{"a.go", "b.go", "c.go"}, Dirs: []string{".", "pkg"}, Complete: true, Nodes: 2}},
		},
		{
			name: "one unsupported node makes the job incomplete",
			traces: []*Trace{
				ok("test", 0, []string{"a.go"}, nil),
				{Job: "test", Node: 1, Method: "atime", Supported: false},
			},
			want: map[string]*Job{"test": {Files: []string{"a.go"}, Dirs: []string{}, Complete: false, Nodes: 2}},
		},
		{
			name:   "an unknown method is incomplete and the map says mixed",
			traces: []*Trace{ok("a", 0, nil, nil), {Job: "b", Method: "ebpf", Supported: true}},
			want: map[string]*Job{
				"a": {Files: []string{}, Dirs: []string{}, Complete: true, Nodes: 1},
				"b": {Files: []string{}, Dirs: []string{}, Complete: false, Nodes: 1},
			},
			method: "mixed",
		},
		{
			name:   "the same node twice counts once",
			traces: []*Trace{ok("a", 0, []string{"x"}, nil), ok("a", 0, []string{"y"}, nil)},
			want:   map[string]*Job{"a": {Files: []string{"x", "y"}, Dirs: []string{}, Complete: true, Nodes: 1}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Merge(tc.traces, "abc123", now)
			if err != nil {
				t.Fatal(err)
			}
			if m.Version != Version || m.Revision != "abc123" || m.CreatedAt != "2026-11-12T18:30:00Z" {
				t.Fatalf("header = %+v", m)
			}
			wantMethod := tc.method
			if wantMethod == "" {
				wantMethod = "atime"
			}
			if m.Method != wantMethod {
				t.Fatalf("method = %q, want %q", m.Method, wantMethod)
			}
			// Compare only the exported data fields.
			got := map[string]*Job{}
			for k, j := range m.Jobs {
				got[k] = &Job{Files: j.Files, Dirs: j.Dirs, Complete: j.Complete, Nodes: j.Nodes}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("jobs = %+v, want %+v", got, tc.want)
			}
		})
	}
	if _, err := Merge(nil, "x", now); err == nil {
		t.Fatal("Merge of no traces must fail, so that no empty map is cached")
	}
}

func TestMarshalParseRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 12, 18, 30, 0, 0, time.UTC)
	m, err := Merge([]*Trace{
		{Job: "lint", Method: "atime", Supported: true, Files: []string{"a<b>&.go"}, Dirs: []string{"."}},
	}, "deadbeef", now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !isGzip(b) {
		t.Fatal("v2 Marshal must write gzip")
	}
	canon, err := m.MarshalJSONCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canon), "a<b>&.go") {
		t.Fatalf("HTML characters must not be escaped:\n%s", canon)
	}
	if !strings.HasSuffix(string(canon), "\n") {
		t.Fatal("want a trailing newline in canonical JSON")
	}
	back, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Jobs["lint"].ReadsFile("a<b>&.go") || !back.Jobs["lint"].ListsDir(".") || back.Jobs["lint"].ReadsFile("zzz") {
		t.Fatalf("lookups wrong: %+v", back.Jobs["lint"])
	}
	if !back.Jobs["lint"].Complete || back.Jobs["lint"].Nodes != 1 {
		t.Fatalf("job = %+v", back.Jobs["lint"])
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string
		check   func(t *testing.T, m *Map)
	}{
		{name: "not json", in: "{", wantErr: "impact map"},
		{name: "wrong version", in: `{"version":3,"jobs":{}}`, wantErr: "unsupported version 3"},
		{name: "missing version", in: `{"jobs":{}}`, wantErr: "unsupported version 0"},
		{
			name: "null jobs is an empty map",
			in:   `{"version":1,"jobs":null}`,
			check: func(t *testing.T, m *Map) {
				if m.Jobs == nil || len(m.Jobs) != 0 {
					t.Fatalf("jobs = %v", m.Jobs)
				}
			},
		},
		{
			name: "missing complete means incomplete",
			in:   `{"version":1,"jobs":{"a":{"files":["x"]}}}`,
			check: func(t *testing.T, m *Map) {
				if m.Jobs["a"].Complete {
					t.Fatal("a job without complete:true must not be treated as complete")
				}
			},
		},
		{
			name: "paths are cleaned and an unusable path makes the job incomplete",
			in:   `{"version":1,"jobs":{"a":{"files":["./x//y.go","/abs"],"dirs":["d/"],"complete":true,"nodes":1},"b":{"files":["ok"],"dirs":["."],"complete":true,"nodes":1}}}`,
			check: func(t *testing.T, m *Map) {
				a := m.Jobs["a"]
				if a.Complete || !reflect.DeepEqual(a.Files, []string{"x/y.go"}) || !reflect.DeepEqual(a.Dirs, []string{"d"}) {
					t.Fatalf("a = %+v", a)
				}
				if !m.Jobs["b"].Complete {
					t.Fatal("b must stay complete")
				}
				if len(m.Problems) != 1 || !strings.Contains(m.Problems[0], `"a"`) {
					t.Fatalf("problems = %v", m.Problems)
				}
			},
		},
		{
			name: "a null job entry is incomplete, not a crash",
			in:   `{"version":1,"jobs":{"a":null}}`,
			check: func(t *testing.T, m *Map) {
				if m.Jobs["a"] == nil || m.Jobs["a"].Complete {
					t.Fatalf("a = %+v", m.Jobs["a"])
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Parse([]byte(tc.in))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, m)
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if !os.IsNotExist(err) {
		t.Fatalf("error = %v, want a not-exist error", err)
	}
}

func TestReadTraceDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("b.0.tsv", tsv("# smarter-trace v1", "job\tb", "supported\ttrue", "F\tb.txt"))
	write("a.0.tsv", tsv("# smarter-trace v1", "job\ta", "supported\ttrue", "F\ta.txt"))
	write("notes.txt", "ignored")
	if err := os.Mkdir(filepath.Join(dir, "sub.tsv"), 0o755); err != nil { // a directory is not a trace
		t.Fatal(err)
	}
	traces, err := ReadTraceDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 2 || traces[0].Job != "a" || traces[1].Job != "b" {
		t.Fatalf("traces = %+v", traces)
	}

	write("bad.0.tsv", "garbage\n")
	if _, err := ReadTraceDir(dir); err == nil || !strings.Contains(err.Error(), "bad.0.tsv") {
		t.Fatalf("error = %v, want it to name the bad file", err)
	}
}

func TestParseRejectsOversizedMap(t *testing.T) {
	jobs := make(map[string]*Job, MaxMapJobs+1)
	for i := range MaxMapJobs + 1 {
		jobs[fmt.Sprintf("j%d", i)] = &Job{Complete: true}
	}
	data, err := json.Marshal(&Map{Version: 1, Jobs: jobs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data); err == nil || !strings.Contains(err.Error(), "over the limit") {
		t.Fatalf("error = %v, want a limit error", err)
	}
}

func TestParseRejectsTooManyPathsPerJob(t *testing.T) {
	files := make([]string, MaxPathsPerJob+1)
	for i := range files {
		files[i] = fmt.Sprintf("f%d.go", i)
	}
	data, err := json.Marshal(&Map{Version: 1, Jobs: map[string]*Job{"lint": {Files: files, Complete: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data); err == nil || !strings.Contains(err.Error(), "over the limit") {
		t.Fatalf("error = %v, want a limit error", err)
	}
}

func TestLoadRejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.json")
	if err := os.WriteFile(path, make([]byte, MaxMapFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "over the limit") {
		t.Fatalf("error = %v, want a limit error", err)
	}
}

func TestParseTraceRejectsTooManyPaths(t *testing.T) {
	var b strings.Builder
	b.WriteString(tsv("# smarter-trace v1", "job\tx", "supported\ttrue"))
	for i := 0; i < MaxPathsPerTrace+1; i++ {
		fmt.Fprintf(&b, "F\tf%d.go\n", i)
	}
	if _, err := ParseTrace(strings.NewReader(b.String())); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("error = %v, want a path limit error", err)
	}
}

func TestConvertMonorepoExampleMap(t *testing.T) {
	if os.Getenv("WRITE_MONOREPO_MAP") == "" {
		t.Skip("set WRITE_MONOREPO_MAP=1 to regenerate examples/monorepo/impact-map.json.gz")
	}
	t.Chdir("../../examples/monorepo")
	raw, err := os.ReadFile("impact-map.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("impact-map.json.gz", gz, 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestParseV1StillWorks(t *testing.T) {
	v1 := `{"version":1,"revision":"x","created_at":"2026-11-12T18:30:00Z","method":"atime","jobs":{"a":{"files":["z.go"],"dirs":["."],"complete":true,"nodes":1}}}`
	m, err := Parse([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != VersionV1 || !m.Jobs["a"].ReadsFile("z.go") {
		t.Fatalf("got %+v", m)
	}
}

func TestEncodeIndexDeltasRoundTrip(t *testing.T) {
	in := []int{0, 2, 5, 99, 100}
	enc, err := encodeIndexDeltas(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeIndexDeltas(enc, 101, MaxIndicesPerJob)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("got %v", got)
	}
}

func TestCleanPath(t *testing.T) {
	tests := []struct {
		in      string
		dir     bool
		want    string
		wantErr bool
	}{
		{"a/b.go", false, "a/b.go", false},
		{"./a/b.go", false, "a/b.go", false},
		{"a//b/", false, "a/b", false},
		{".", true, ".", false},
		{".", false, "", true},
		{"", true, "", true},
		{"/a", false, "", true},
		{"a/../../b", false, "", true},
		{"a/../b", false, "b", false},
		{"..", true, "", true},
		{"a\x00b", false, "", true},
	}
	for _, tc := range tests {
		got, err := CleanPath(tc.in, tc.dir)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("CleanPath(%q, %v) = %q, %v; want %q, err=%v", tc.in, tc.dir, got, err, tc.want, tc.wantErr)
		}
	}
}
