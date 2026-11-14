package config

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// maxNodes caps the tree that expand may build. An alias becomes a copy of
// what it names, so aliases of aliases can turn a small file into an enormous
// tree. A real config stays far below the cap.
const maxNodes = 1_000_000

// expand makes the document stand on its own: every alias becomes a copy of the
// node it names, every merge key (`<<`) is merged into its mapping, and no
// anchor is left. Afterwards no two places in the tree share a node, so an edit
// in one place cannot reach another, and the document means what it meant
// before.
func expand(doc *yaml.Node) error {
	x := &expander{active: map[*yaml.Node]bool{}}
	for i, c := range doc.Content {
		cp, err := x.node(c)
		if err != nil {
			return err
		}
		doc.Content[i] = cp
	}
	return nil
}

type expander struct {
	made int // nodes built so far
	// active holds the nodes being copied right now. An alias that names one of
	// them would copy the node into itself.
	active map[*yaml.Node]bool
}

// node returns an independent copy of n, with its aliases and merge keys
// expanded.
func (x *expander) node(n *yaml.Node) (*yaml.Node, error) {
	if n.Kind == yaml.AliasNode {
		target := n.Alias
		switch {
		case target == nil:
			return nil, fmt.Errorf("line %d: the alias *%s names no anchor", n.Line, n.Value)
		case x.active[target]:
			return nil, fmt.Errorf("line %d: the alias *%s names a node that contains it", n.Line, n.Value)
		}
		cp, err := x.node(target)
		if err != nil {
			return nil, err
		}
		// The copy stands where the alias stood, so it takes the alias's
		// comments and not the ones written at the anchor.
		cp.HeadComment, cp.LineComment, cp.FootComment = n.HeadComment, n.LineComment, n.FootComment
		return cp, nil
	}

	if x.made++; x.made > maxNodes {
		return nil, fmt.Errorf("the config expands to more than %d nodes: its aliases are nested too deeply", maxNodes)
	}
	x.active[n] = true
	defer delete(x.active, n)

	cp := *n
	cp.Anchor = ""
	cp.Content = nil
	if n.Kind == yaml.MappingNode {
		if err := x.mapping(n, &cp); err != nil {
			return nil, err
		}
		return &cp, nil
	}
	for _, c := range n.Content {
		cc, err := x.node(c)
		if err != nil {
			return nil, err
		}
		cp.Content = append(cp.Content, cc)
	}
	return &cp, nil
}

// mapping fills cp, the copy of the mapping n, with n's pairs in their order.
// A merge key is replaced by the pairs it brings, at its own position. A key
// that n writes itself wins over a merged one wherever the merge key stands,
// and of two merged mappings the earlier one wins.
func (x *expander) mapping(n, cp *yaml.Node) error {
	taken := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := named(n.Content[i]); !isMergeKey(k) {
			if id, ok := keyID(k); ok {
				taken[id] = true
			}
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if !isMergeKey(k) {
			kc, err := x.node(k)
			if err != nil {
				return err
			}
			vc, err := x.node(v)
			if err != nil {
				return err
			}
			cp.Content = append(cp.Content, kc, vc)
			continue
		}
		sources, err := x.mergeSources(v)
		if err != nil {
			return err
		}
		head := k.HeadComment
		for _, src := range sources {
			for j := 0; j+1 < len(src.Content); j += 2 {
				if id, ok := keyID(src.Content[j]); ok {
					if taken[id] {
						continue
					}
					taken[id] = true
				}
				if head != "" { // the comment above `<<` goes on the first pair it brings
					if c := src.Content[j].HeadComment; c != "" {
						head += "\n" + c
					}
					src.Content[j].HeadComment, head = head, ""
				}
				cp.Content = append(cp.Content, src.Content[j], src.Content[j+1])
			}
		}
	}
	return nil
}

// mergeSources returns the mappings the value of a merge key names, expanded,
// the one that takes precedence first.
func (x *expander) mergeSources(v *yaml.Node) ([]*yaml.Node, error) {
	bad := fmt.Errorf("line %d: a merge key needs a mapping or a list of mappings", v.Line)
	switch t := named(v); t.Kind {
	case yaml.MappingNode:
		m, err := x.node(v)
		if err != nil {
			return nil, err
		}
		return []*yaml.Node{m}, nil
	case yaml.SequenceNode:
		var out []*yaml.Node
		for _, item := range t.Content {
			if named(item).Kind != yaml.MappingNode {
				return nil, bad
			}
			m, err := x.node(item)
			if err != nil {
				return nil, err
			}
			out = append(out, m)
		}
		return out, nil
	}
	return nil, bad
}

// named is the node an alias names, or n itself.
func named(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return n.Alias
	}
	return n
}

func isMergeKey(k *yaml.Node) bool {
	return k.Kind == yaml.ScalarNode && k.Tag == "!!merge"
}

// keyID identifies a scalar key by its tag and text, so that the string "1" and
// the number 1 are two keys. A key that is not a scalar has no identity.
func keyID(k *yaml.Node) (string, bool) {
	if k.Kind != yaml.ScalarNode {
		return "", false
	}
	return k.Tag + "\x00" + k.Value, true
}
