package config

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// The builders below make the nodes the planner injects. They keep the key
// order the caller gives, which a Go map would not.

// Str is a string scalar. The encoder quotes it when it must.
func Str(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// Literal is a string scalar that is written as a block literal when it has
// more than one line.
func Literal(s string) *yaml.Node {
	n := Str(s)
	for _, r := range s {
		if r == '\n' {
			n.Style = yaml.LiteralStyle
			break
		}
	}
	return n
}

// Bool is a boolean scalar.
func Bool(b bool) *yaml.Node {
	v := "false"
	if b {
		v = "true"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v}
}

// Seq is a block sequence.
func Seq(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
}

// Strs is a sequence of string scalars.
func Strs(items ...string) *yaml.Node {
	nodes := make([]*yaml.Node, len(items))
	for i, s := range items {
		nodes[i] = Str(s)
	}
	return Seq(nodes...)
}

// Map is a block mapping built from alternating keys and values. A key is a
// string; a value is a *yaml.Node, a string or a bool. Map panics on any other
// type, which is a programming error.
func Map(kv ...any) *yaml.Node {
	if len(kv)%2 != 0 {
		panic("config.Map: odd number of arguments")
	}
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			panic(fmt.Sprintf("config.Map: key %d is %T, want string", i/2, kv[i]))
		}
		var val *yaml.Node
		switch v := kv[i+1].(type) {
		case *yaml.Node:
			val = v
		case string:
			val = Str(v)
		case bool:
			val = Bool(v)
		default:
			panic(fmt.Sprintf("config.Map: value of %q is %T", key, kv[i+1]))
		}
		m.Content = append(m.Content, Str(key), val)
	}
	return m
}

// RunStep builds a `run` step. Empty name, shell and when are left out.
func RunStep(name, shell, when, command string) *yaml.Node {
	args := Map()
	add := func(k string, v *yaml.Node) { args.Content = append(args.Content, Str(k), v) }
	if name != "" {
		add("name", Str(name))
	}
	if when != "" {
		add("when", Str(when))
	}
	if shell != "" {
		add("shell", Str(shell))
	}
	add("command", Literal(command))
	return Map("run", args)
}
