package config

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// CheckoutPath is the path argument of a checkout step, or "" for the default.
func CheckoutPath(step *yaml.Node) (ok bool, path string) {
	if !isCheckout(step) {
		return false, ""
	}
	if step.Kind == yaml.ScalarNode {
		return true, ""
	}
	if step.Kind == yaml.MappingNode {
		body := step.Content[1]
		if p, ok := scalarValue(lookup(body, "path")); ok {
			return true, p
		}
		return true, ""
	}
	return false, ""
}

// stepsHaveCheckout reports whether steps contain a checkout step, or invoke a
// local command (of scope) that contains one, recursively.
func (s scope) stepsHaveCheckout(steps *yaml.Node, seen map[string]bool, depth int) bool {
	at, _, ok := s.locateCheckout(steps, seen, depth)
	return ok && at >= 0
}

// stepCommandName is the local command a step invokes (scalar or key), or "".
func stepCommandName(st *yaml.Node) (string, bool) {
	switch st.Kind {
	case yaml.ScalarNode:
		if st.Value == "checkout" {
			return "", false
		}
		return st.Value, st.Value != ""
	case yaml.MappingNode:
		if len(st.Content) < 2 || st.Content[0].Kind != yaml.ScalarNode {
			return "", false
		}
		key := st.Content[0].Value
		if key == "checkout" {
			return "", false
		}
		return key, true
	}
	return "", false
}

// locateCheckout scans steps in order for the first checkout: a top-level step
// or a local command whose definition contains one (recursively). at is the
// index of that step or command invocation; path is from checkout: {path: ...}
// when set.
func (s scope) locateCheckout(steps *yaml.Node, seen map[string]bool, depth int) (at int, path string, ok bool) {
	if steps == nil || steps.Kind != yaml.SequenceNode || depth > maxDepth {
		return -1, "", false
	}
	for i, st := range steps.Content {
		if has, p := CheckoutPath(st); has {
			return i, p, true
		}
		if cmd, isCmd := stepCommandName(st); isCmd {
			if body := s.command(cmd); body != nil && !seen[cmd] {
				seen[cmd] = true
				if sub := lookup(body, "steps"); sub != nil {
					if _, p, subOK := s.locateCheckout(sub, seen, depth+1); subOK {
						return i, p, true
					}
				}
			}
		}
	}
	return -1, "", false
}

// InsertAfterCheckout inserts step right after the first checkout the job will
// run: a top-level checkout step, or a local command invocation whose
// definition contains checkout (recursively). checkoutPath is written on the
// inserted step as CIRCLE_WORKING_DIRECTORY when non-empty.
func (c *Config) InsertAfterCheckout(job string, step *yaml.Node) (bool, error) {
	def := c.jobDef(job)
	if def == nil {
		return false, fmt.Errorf("job %q is not defined in this config", job)
	}
	steps := lookup(def, "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode {
		return false, nil
	}
	sc := c.rootScope()
	at, path, ok := sc.locateCheckout(steps, map[string]bool{}, 0)
	if !ok {
		return false, nil
	}
	insertAt := at + 1
	if insertAt < len(steps.Content) && sameNode(steps.Content[insertAt], step) {
		return true, nil
	}
	if path != "" {
		setStepEnv(step, "CIRCLE_WORKING_DIRECTORY", path)
	}
	steps.Style &^= yaml.FlowStyle
	steps.Content = append(steps.Content[:insertAt], append([]*yaml.Node{step}, steps.Content[insertAt:]...)...)
	return true, nil
}

func setStepEnv(step *yaml.Node, key, val string) {
	if step.Kind != yaml.MappingNode || len(step.Content) < 2 {
		return
	}
	run := step.Content[1]
	if run.Kind != yaml.MappingNode {
		return
	}
	env := lookup(run, "environment")
	if env == nil {
		env = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		run.Content = append(run.Content, Str("environment"), env)
	}
	if env.Kind == yaml.MappingNode {
		if keyIndex(env, key) < 0 {
			env.Content = append(env.Content, Str(key), Str(val))
		}
	}
}

// JobCheckoutPath is the path of the first checkout of the job, if any.
func (c *Config) JobCheckoutPath(job string) string {
	def := c.jobDef(job)
	if def == nil {
		return ""
	}
	steps := lookup(def, "steps")
	_, path, ok := c.rootScope().locateCheckout(steps, map[string]bool{}, 0)
	if !ok {
		return ""
	}
	return path
}

// JobHasCheckout reports whether the job runs a checkout step directly or
// through a local command.
func (c *Config) JobHasCheckout(job string) bool {
	def := c.jobDef(job)
	if def == nil {
		return false
	}
	steps := lookup(def, "steps")
	_, _, ok := c.rootScope().locateCheckout(steps, map[string]bool{}, 0)
	return ok
}
