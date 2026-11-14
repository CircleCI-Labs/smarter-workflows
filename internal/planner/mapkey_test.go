package planner_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

// mapSaveCache returns the save_cache step of the map job of a traced plan.
func mapSaveCache(t *testing.T, res *planner.Result) map[string]any {
	t.Helper()
	cfg := decodeConfig(t, res.Config)
	job, ok := cfg["jobs"].(map[string]any)["smarter-impact-map"].(map[string]any)
	if !ok {
		t.Fatalf("the plan has no map job:\n%s", res.Config)
	}
	for _, s := range job["steps"].([]any) {
		if m, ok := s.(map[string]any); ok {
			if save, ok := m["save_cache"].(map[string]any); ok {
				return save
			}
		}
	}
	t.Fatal("the map job has no save_cache step")
	return nil
}

func TestMapKeyPrefixIsTheSaveCacheKeyOfTheMapJob(t *testing.T) {
	tests := []struct {
		name   string
		prefix string // empty keeps the default
		want   string
	}{
		{"default", "", `smarter-impact-v1-{{ checksum ".smarter-key" }}`},
		{"explicit default", "smarter-impact-v1", `smarter-impact-v1-{{ checksum ".smarter-key" }}`},
		{"another pipeline of the project", "smarter-impact-v1-demo", `smarter-impact-v1-demo-{{ checksum ".smarter-key" }}`},
		{"dots and underscores", "demo_map.v2", `demo_map.v2-{{ checksum ".smarter-key" }}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := traceOpts()
			if tc.prefix != "" {
				o.MapKeyPrefix = tc.prefix
			}
			res := plan(t, traceInput(), o)
			save := mapSaveCache(t, res)
			if save["key"] != tc.want {
				t.Errorf("save_cache key = %q, want %q", save["key"], tc.want)
			}
			// Only the key changes: the path and the other steps stay.
			if got := toStrings(save["paths"]); !slices.Equal(got, []string{"/tmp/smarter/impact-map.json"}) {
				t.Errorf("save_cache paths = %v", got)
			}
			// No pipeline parameters or new expressions come in with the prefix.
			noNewExpressions(t, []byte(traceConfig), res.Config)
		})
	}
}

func TestMapKeyPrefixDefaultIsUnchanged(t *testing.T) {
	if got := planner.DefaultOptions().MapKeyPrefix; got != "smarter-impact-v1" {
		t.Fatalf("default prefix = %q", got)
	}
	if planner.DefaultMapKeyPrefix != "smarter-impact-v1" {
		t.Fatalf("DefaultMapKeyPrefix = %q", planner.DefaultMapKeyPrefix)
	}
	// A plan without a map job does not mention the prefix at all.
	o := traceOpts()
	o.MapKeyPrefix = "smarter-impact-v1-demo"
	o.Trace = planner.TraceOff
	res := plan(t, traceInput(), o)
	if strings.Contains(string(res.Config), "smarter-impact-v1-demo") {
		t.Errorf("an untraced plan carries the prefix:\n%s", res.Config)
	}
}

func TestMapKeyPrefixMustBeUsable(t *testing.T) {
	for _, prefix := range []string{
		"",
		" ",
		"-demo",
		".demo",
		"has space",
		"with,comma",
		"slash/ed",
		"colon:ed",
		`quote"d`,
		"{{ checksum \"x\" }}",
		"demo\n",
		"<< pipeline.id >>",
	} {
		o := planner.DefaultOptions()
		o.MapKeyPrefix = prefix
		if err := o.Validate(); err == nil || !strings.Contains(err.Error(), "map key prefix") {
			t.Errorf("prefix %q: Validate() = %v, want a map key prefix error", prefix, err)
		}
		// planner.Run reports it as a usage error, before it reads anything.
		o = traceOpts()
		o.MapKeyPrefix = prefix
		_, err := planner.Run(t.Context(), traceInput(), o)
		var ue *planner.UsageError
		if !errors.As(err, &ue) {
			t.Errorf("prefix %q: Run() error = %v, want a usage error", prefix, err)
		}
	}
	for _, prefix := range []string{"a", "0", "smarter-impact-v1", "smarter-impact-v1-demo", "Team_A.map-2"} {
		o := planner.DefaultOptions()
		o.MapKeyPrefix = prefix
		if err := o.Validate(); err != nil {
			t.Errorf("prefix %q: Validate() = %v", prefix, err)
		}
	}
}
