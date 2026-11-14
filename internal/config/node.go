package config

import (
	"go.yaml.in/yaml/v3"
)

// maxDepth bounds recursion through aliases and nested nodes, so that a
// self-referencing anchor cannot hang the planner.
const maxDepth = 64

// deref follows alias nodes to the node they name. It returns nil for an alias
// cycle.
func deref(n *yaml.Node) *yaml.Node {
	for i := 0; n != nil && n.Kind == yaml.AliasNode; i++ {
		if i >= maxDepth {
			return nil
		}
		n = n.Alias
	}
	return n
}

func isMergeKey(k *yaml.Node) bool {
	return k.Kind == yaml.ScalarNode && k.Tag == "!!merge"
}

func isNull(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// lookup returns the value of key in mapping m. It follows aliases and YAML
// merge keys ("<<"); explicit keys win over merged ones.
func lookup(m *yaml.Node, key string) *yaml.Node {
	return lookupDepth(m, key, 0)
}

func lookupDepth(m *yaml.Node, key string, depth int) *yaml.Node {
	m = deref(m)
	if m == nil || m.Kind != yaml.MappingNode || depth > maxDepth {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && !isMergeKey(k) && k.Value == key {
			return m.Content[i+1]
		}
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if !isMergeKey(m.Content[i]) {
			continue
		}
		v := deref(m.Content[i+1])
		if v == nil {
			continue
		}
		switch v.Kind {
		case yaml.MappingNode:
			if r := lookupDepth(v, key, depth+1); r != nil {
				return r
			}
		case yaml.SequenceNode:
			for _, item := range v.Content {
				if r := lookupDepth(item, key, depth+1); r != nil {
					return r
				}
			}
		}
	}
	return nil
}

// ownIndex returns the index in m.Content of the key node for an explicit
// (not merged) key, or -1.
func ownIndex(m *yaml.Node, key string) int {
	if m == nil || m.Kind != yaml.MappingNode {
		return -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && !isMergeKey(k) && k.Value == key {
			return i
		}
	}
	return -1
}

// setOwn sets an explicit key, replacing its value or appending the pair.
func setOwn(m *yaml.Node, key string, v *yaml.Node) {
	if i := ownIndex(m, key); i >= 0 {
		m.Content[i+1] = v
		return
	}
	m.Content = append(m.Content, Str(key), v)
}

// delOwn removes an explicit key.
func delOwn(m *yaml.Node, key string) {
	if i := ownIndex(m, key); i >= 0 {
		m.Content = append(m.Content[:i], m.Content[i+2:]...)
	}
}

// scalarValue returns the text of a scalar node (aliases followed).
func scalarValue(n *yaml.Node) (string, bool) {
	n = deref(n)
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

// stringList reads a scalar or a sequence of scalars.
func stringList(n *yaml.Node) ([]string, bool) {
	n = deref(n)
	if n == nil {
		return nil, false
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return []string{n.Value}, true
	case yaml.SequenceNode:
		out := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			s, ok := scalarValue(c)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

// clone deep-copies a node. Aliases in the copy are replaced by copies of what
// they name, and anchors are dropped, so the copy stands alone.
func clone(n *yaml.Node) *yaml.Node {
	return cloneDepth(n, 0)
}

func cloneDepth(n *yaml.Node, depth int) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.AliasNode {
		target := deref(n)
		if target == nil || depth > maxDepth {
			// A cycle: keep a null so the result is still valid YAML.
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
		}
		cp := cloneDepth(target, depth+1)
		cp.HeadComment, cp.LineComment, cp.FootComment = n.HeadComment, n.LineComment, n.FootComment
		return cp
	}
	cp := *n
	cp.Anchor = ""
	cp.Alias = nil
	cp.Content = nil
	for _, c := range n.Content {
		cp.Content = append(cp.Content, cloneDepth(c, depth+1))
	}
	return &cp
}

// walk calls fn for n and every node below it, without following aliases.
func walk(n *yaml.Node, fn func(*yaml.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Content {
		walk(c, fn)
	}
}
