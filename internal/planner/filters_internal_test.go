package planner

import (
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
)

func TestFilterVerdict(t *testing.T) {
	ref := func(only, ignore []string) *config.RefFilter {
		return &config.RefFilter{Only: only, Ignore: ignore, HasOnly: only != nil, HasIgnore: ignore != nil}
	}
	filters := func(b, tg *config.RefFilter) config.Filters {
		return config.Filters{Present: b != nil || tg != nil, Branches: b, Tags: tg}
	}
	tests := []struct {
		name        string
		f           config.Filters
		branch, tag string
		want        tri
	}{
		{"no filters on a branch", config.Filters{}, "feature", "", triYes},
		{"no filters on a tag: tags need a filter", config.Filters{}, "", "v1", triNo},
		{"neither branch nor tag known", config.Filters{}, "", "", triUnknown},
		{"both a branch and a tag", filters(ref([]string{"main"}, nil), nil), "main", "v1", triUnknown},
		{"an expression", config.Filters{Present: true, Expr: true}, "main", "", triUnknown},

		{"only: exact match", filters(ref([]string{"main"}, nil), nil), "main", "", triYes},
		{"only: no match", filters(ref([]string{"main"}, nil), nil), "feature", "", triNo},
		{"only: the name is not a prefix match", filters(ref([]string{"main"}, nil), nil), "main-2", "", triNo},
		{"only: a regex must match the whole name", filters(ref([]string{"/rel-.*/"}, nil), nil), "rel-1.2", "", triYes},
		{"only: a regex that matches only a part", filters(ref([]string{"/rel/"}, nil), nil), "rel-1.2", "", triNo},
		{"only: an anchored regex", filters(ref([]string{"/^release\\/.*/"}, nil), nil), "release/1.0", "", triYes},
		{"only: any of a list", filters(ref([]string{"main", "/hot-.*/"}, nil), nil), "hot-fix", "", triYes},
		{"ignore: a match", filters(ref(nil, []string{"wip", "/tmp-.*/"}), nil), "tmp-x", "", triNo},
		{"ignore: no match", filters(ref(nil, []string{"wip"}), nil), "feature", "", triYes},
		{"only and ignore: in only, not in ignore", filters(ref([]string{"/.*/"}, []string{"wip"}), nil), "feature", "", triYes},
		{"only and ignore: outside only", filters(ref([]string{"main"}, []string{"wip"}), nil), "feature", "", triNo},
		{"only and ignore: in both is left to CircleCI", filters(ref([]string{"/.*/"}, []string{"wip"}), nil), "wip", "", triUnknown},
		{"a branches filter with neither key matches nothing", filters(&config.RefFilter{}, nil), "main", "", triNo},
		{"a regex Go cannot read", filters(ref([]string{"/(?<=a)b/"}, nil), nil), "ab", "", triUnknown},
		{"a dynamic value", filters(&config.RefFilter{Dynamic: true}, nil), "main", "", triUnknown},
		{"a branch filter does not matter on a tag", filters(ref([]string{"main"}, nil), nil), "", "v1", triNo},

		{"tags only, on a branch: the job runs there too", filters(nil, ref([]string{"/v.*/"}, nil)), "feature", "", triYes},
		{"tags only, on a matching tag", filters(nil, ref([]string{"/v.*/"}, nil)), "", "v1.0", triYes},
		{"tags only, on another tag", filters(nil, ref([]string{"/v.*/"}, nil)), "", "x1", triNo},
		{"tags ignore, on an ignored tag", filters(nil, ref(nil, []string{"/v1.*/"})), "", "v1.2", triNo},
		{"tags ignore, on another tag", filters(nil, ref(nil, []string{"/v1.*/"})), "", "v2.0", triYes},
		{"a branches filter alone, on a tag", filters(ref([]string{"main"}, nil), nil), "", "v1", triNo},
		{"tags and branches ignore, on a tag: the tag filter decides", filters(ref(nil, []string{"/.*/"}), ref([]string{"/v.*/"}, nil)), "", "v1", triYes},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := filterVerdict(tc.f, tc.branch, tc.tag); got != tc.want {
				t.Fatalf("verdict = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWillRunCascade(t *testing.T) {
	only := func(b string) config.Filters {
		return config.Filters{Present: true, Branches: &config.RefFilter{Only: []string{b}, HasOnly: true}}
	}
	entry := func(name string, f config.Filters, requires ...string) *config.Entry {
		return &config.Entry{Name: name, Ref: name, Filters: f, Requires: requires}
	}
	tests := []struct {
		name    string
		entries []*config.Entry
		deps    [][]int
		status  []bool
		unres   []bool
		want    []tri
	}{
		{
			name:    "a job whose only requirement is filtered out does not run either",
			entries: []*config.Entry{entry("a", only("main")), entry("b", config.Filters{}, "a")},
			deps:    [][]int{nil, {0}},
			want:    []tri{triNo, triNo},
		},
		{
			name:    "a requirement on a filtered-out job is ignored when another one runs",
			entries: []*config.Entry{entry("a", only("main")), entry("c", config.Filters{}), entry("b", config.Filters{}, "a", "c")},
			deps:    [][]int{nil, nil, {0, 1}},
			want:    []tri{triNo, triYes, triYes},
		},
		{
			name:    "the cascade goes down the chain",
			entries: []*config.Entry{entry("a", only("main")), entry("b", config.Filters{}, "a"), entry("c", config.Filters{}, "b")},
			deps:    [][]int{nil, {0}, {1}},
			want:    []tri{triNo, triNo, triNo},
		},
		{
			name:    "an unknown requirement makes the job unknown",
			entries: []*config.Entry{entry("a", config.Filters{Present: true, Expr: true}), entry("b", config.Filters{}, "a")},
			deps:    [][]int{nil, {0}},
			want:    []tri{triUnknown, triUnknown},
		},
		{
			name:    "a requirement that runs makes an unknown one irrelevant",
			entries: []*config.Entry{entry("a", config.Filters{Present: true, Expr: true}), entry("c", config.Filters{}), entry("b", config.Filters{}, "a", "c")},
			deps:    [][]int{nil, nil, {0, 1}},
			want:    []tri{triUnknown, triYes, triYes},
		},
		{
			name:    "a status requirement may start the job although the other does not run",
			entries: []*config.Entry{entry("a", only("main")), entry("b", config.Filters{}, "a")},
			deps:    [][]int{nil, {0}},
			status:  []bool{false, true},
			want:    []tri{triNo, triUnknown},
		},
		{
			name:    "a requirement that matches no entry could be anything",
			entries: []*config.Entry{entry("b", config.Filters{}, "ghost")},
			deps:    [][]int{nil},
			unres:   []bool{true},
			want:    []tri{triUnknown},
		},
		{
			name:    "a filtered job stays filtered whatever it requires",
			entries: []*config.Entry{entry("a", config.Filters{}), entry("b", only("main"), "a")},
			deps:    [][]int{nil, {0}},
			want:    []tri{triYes, triNo},
		},
		{
			name:    "a cycle is unknown, not a hang",
			entries: []*config.Entry{entry("a", config.Filters{}, "b"), entry("b", config.Filters{}, "a")},
			deps:    [][]int{{1}, {0}},
			want:    []tri{triUnknown, triUnknown},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for i, s := range tc.status {
				tc.entries[i].StatusRequires = s
			}
			unres := tc.unres
			if unres == nil {
				unres = make([]bool, len(tc.entries))
			}
			got := willRun(tc.entries, tc.deps, unres, "feature", "")
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("%s: %v, want %v", tc.entries[i].Name, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"dev-abc1234":      "dev-abc1234",
		"v0.1.0":           "v0.1.0",
		"/usr/local/bin/x": "/usr/local/bin/x",
		"has space":        "'has space'",
		"it's":             `'it'\''s'`,
		"":                 "''",
		"$(rm -rf /)":      "'$(rm -rf /)'",
		"semi;colon":       "'semi;colon'",
		"line\nbreak":      "'line\nbreak'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
