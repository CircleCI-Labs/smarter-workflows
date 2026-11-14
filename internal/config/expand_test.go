package config_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
)

// Parse expands aliases and merge keys, so what comes out has none, and means
// what the input meant.
func TestParseExpandsAliasesAndMergeKeys(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{
			name: "an alias becomes a copy and the anchor is dropped",
			in: `a: &x
  - one
  - two
b: *x
c: [*x, *x]
`,
			want: `a:
  - one
  - two
b:
  - one
  - two
c: [[one, two], [one, two]]
`,
		},
		{
			name: "a scalar anchor",
			in: `image: &img cimg/base:2026.11
a: {docker: [{image: *img}]}
`,
			want: `image: cimg/base:2026.11
a: {docker: [{image: 'cimg/base:2026.11'}]}
`,
		},
		{
			name: "an alias that holds another alias",
			in: `a: &a {k: v}
b: &b [*a, *a]
c: *b
`,
			want: `a: {k: v}
b: [{k: v}, {k: v}]
c: [{k: v}, {k: v}]
`,
		},
		{
			name: "a merge key brings its pairs where it stands",
			in: `base: &base
  docker: [{image: x}]
  environment: {A: "1"}
job:
  steps: [checkout]
  <<: *base
  parallelism: 2
`,
			want: `base:
  docker: [{image: x}]
  environment: {A: "1"}
job:
  steps: [checkout]
  docker: [{image: x}]
  environment: {A: "1"}
  parallelism: 2
`,
		},
		{
			name: "a key written in the mapping wins, before or after the merge key",
			in: `base: &base {a: merged, b: merged, c: merged}
before:
  a: own
  <<: *base
after:
  <<: *base
  b: own
`,
			want: `base: {a: merged, b: merged, c: merged}
before:
  a: own
  b: merged
  c: merged
after:
  a: merged
  c: merged
  b: own
`,
		},
		{
			name: "of a list of mappings the first wins",
			in: `one: &one {a: 1, b: 1}
two: &two {b: 2, c: 2}
job:
  <<: [*one, *two]
  d: 4
`,
			want: `one: {a: 1, b: 1}
two: {b: 2, c: 2}
job:
  a: 1
  b: 1
  c: 2
  d: 4
`,
		},
		{
			name: "a merge key that holds an inline mapping, and a merged mapping that merges again",
			in: `deep: &deep {x: deep, y: deep}
mid: &mid
  <<: *deep
  y: mid
job:
  <<: [*mid, {z: inline}]
`,
			want: `deep: {x: deep, y: deep}
mid:
  x: deep
  y: mid
job:
  x: deep
  y: mid
  z: inline
`,
		},
		{
			name: "a merge key in a flow mapping",
			in: `base: &base {a: 1}
job: {<<: *base, b: 2}
`,
			want: `base: {a: 1}
job: {a: 1, b: 2}
`,
		},
		{
			name: "the string \"<<\" is a key like any other",
			in: `base: &base {a: 1}
job:
  "<<": plain
  b: *base
`,
			want: `base: {a: 1}
job:
  "<<": plain
  b: {a: 1}
`,
		},
		{
			name: "comments: an alias keeps its own, and the one above a merge key goes on the first pair it brings",
			in: `image: &img cimg/base # pinned
base: &base
  # about a
  a: 1
job:
  # the defaults
  <<: *base
  docker: *img # the same image
`,
			want: `image: cimg/base # pinned
base:
  # about a
  a: 1
job:
  # the defaults
  # about a
  a: 1
  docker: cimg/base # the same image
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := mustParse(t, tc.in)
			got := mustMarshal(t, c)
			if got != tc.want {
				t.Fatalf("--- want\n%s\n--- got\n%s", tc.want, got)
			}
			// Whatever the text, the data are what the YAML library makes of the
			// input, aliases and merges resolved.
			var before, after any
			if err := yaml.Unmarshal([]byte(tc.in), &before); err != nil {
				t.Fatalf("the input is not valid YAML: %v", err)
			}
			if err := yaml.Unmarshal([]byte(got), &after); err != nil {
				t.Fatalf("the output is not valid YAML: %v", err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("the data changed\n--- in\n%s\n--- out\n%s", tc.in, got)
			}
			if strings.Contains(got, "&") || strings.Contains(got, "*") || strings.Contains(got, "<<:") && !strings.Contains(tc.in, `"<<"`) {
				t.Errorf("the output still has an anchor, an alias or a merge key:\n%s", got)
			}
		})
	}
}

func TestParseRefusesWhatCannotBeExpanded(t *testing.T) {
	var bomb strings.Builder
	bomb.WriteString("l0: &l0 [a, b, c, d, e, f, g, h, i, j]\n")
	for i := 1; i <= 8; i++ {
		fmt.Fprintf(&bomb, "l%d: &l%d [", i, i)
		for j := 0; j < 10; j++ {
			if j > 0 {
				bomb.WriteString(", ")
			}
			fmt.Fprintf(&bomb, "*l%d", i-1)
		}
		bomb.WriteString("]\n")
	}
	tests := []struct{ name, in, want string }{
		{"an alias to a node that holds it", "a: &x [*x]\n", "names a node that contains it"},
		{"an alias to the mapping it sits in", "a: &x {b: *x}\n", "names a node that contains it"},
		{"a merge key holding a scalar", "a:\n  <<: 1\n", "a merge key needs a mapping"},
		{"a merge key holding nothing", "a:\n  <<:\n  b: 1\n", "a merge key needs a mapping"},
		{"a merge key holding a list with a scalar", "a:\n  <<: [{b: 1}, 2]\n", "a merge key needs a mapping"},
		{"aliases nested ten ways, eight levels deep", bomb.String(), "expands to more than"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Parse([]byte(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "parse config") {
				t.Fatalf("error = %v, want one that says %q", err, tc.want)
			}
		})
	}
}
