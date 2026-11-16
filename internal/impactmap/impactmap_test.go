package impactmap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMarshalParseRoundTrip(t *testing.T) {
	m := &Map{
		Version: 2, Revision: "deadbeef", CreatedAt: "2026-11-12T18:30:00Z", Method: "static",
		Jobs: map[string]*Job{
			"lint": {Files: []string{"a<b>&.go"}, Dirs: []string{"."}, Complete: true, Nodes: 1},
		},
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
	v1 := `{"version":1,"revision":"x","created_at":"2026-11-12T18:30:00Z","method":"static","jobs":{"a":{"files":["z.go"],"dirs":["."],"complete":true,"nodes":1}}}`
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
