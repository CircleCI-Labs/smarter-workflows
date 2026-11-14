package config

import (
	"go.yaml.in/yaml/v3"
)

// maxDepth bounds the recursion through nested nodes and commands, so that a
// command that calls itself cannot hang the planner.
const maxDepth = 64

func isNull(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// keyIndex returns the index in m.Content of the key node for key, or -1. A
// parsed config has no merge keys (Parse expands them), so every key is
// written in its own mapping.
func keyIndex(m *yaml.Node, key string) int {
	if m == nil || m.Kind != yaml.MappingNode {
		return -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.Value == key {
			return i
		}
	}
	return -1
}

// lookup returns the value of key in mapping m, or nil.
func lookup(m *yaml.Node, key string) *yaml.Node {
	if i := keyIndex(m, key); i >= 0 {
		return m.Content[i+1]
	}
	return nil
}

// scalarValue returns the text of a scalar node.
func scalarValue(n *yaml.Node) (string, bool) {
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

// stringList reads a scalar or a sequence of scalars.
func stringList(n *yaml.Node) ([]string, bool) {
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
