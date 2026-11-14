package config

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

// After Parse, no two places of the tree hold the same node, and the tree has
// no alias, anchor or merge key: nothing an edit touches is shared.
func TestParsedTreesShareNothing(t *testing.T) {
	sources := map[string]string{
		"anchors and aliases": `version: 2.1
x-steps: &steps
  - checkout
  - run: &cmd make
jobs:
  a: {docker: &docker [{image: x}], steps: *steps}
  b: {docker: *docker, steps: *steps}
workflows:
  w:
    jobs:
      - a: &args {context: c}
      - b: *args
`,
		"merge keys": `version: 2.1
defaults: &defaults
  docker: [{image: x}]
  environment: &env {A: "1"}
jobs:
  a:
    <<: *defaults
    environment: {<<: *env, B: "2"}
  b:
    <<: [*defaults, {resource_class: small}]
workflows:
  w:
    jobs: [a, b]
`,
		"nested": `a: &a [x, y]
b: &b [*a, *a]
c: &c {k: *b, l: *b}
d: [*c, *c]
`,
	}
	for name, src := range sources {
		t.Run(name, func(t *testing.T) {
			c, err := Parse([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			seen := map[*yaml.Node]bool{}
			var walk func(n *yaml.Node)
			walk = func(n *yaml.Node) {
				switch {
				case seen[n]:
					t.Errorf("line %d: a node is in the tree twice (%q)", n.Line, n.Value)
					return
				case n.Kind == yaml.AliasNode:
					t.Errorf("line %d: an alias is left", n.Line)
				case n.Anchor != "":
					t.Errorf("line %d: the anchor &%s is left", n.Line, n.Anchor)
				case isMergeKey(n):
					t.Errorf("line %d: a merge key is left", n.Line)
				}
				seen[n] = true
				for _, child := range n.Content {
					walk(child)
				}
			}
			walk(c.doc)
		})
	}
}
