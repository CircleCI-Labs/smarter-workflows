// Workflow when/unless evaluation follows CircleCI logic statements and
// expressions: https://circleci.com/docs/reference/configuration-reference/#logic-statements
package planner

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
)

// pipelineEnv is what a workflow condition may read: pipeline values and
// parameters passed to plan.
type pipelineEnv struct {
	Branch, Tag, TriggerType string
	Parameters               map[string]any
}

// evalWorkflowRuns is step 1 for a workflow's when/unless. triUnknown means a
// full run; triNo means the workflow does not run in this pipeline.
func evalWorkflowRuns(wfNode *yaml.Node, env pipelineEnv) tri {
	when := lookupCondition(wfNode, "when", triYes, env)
	unless := lookupCondition(wfNode, "unless", triNo, env)
	if when == triUnknown || unless == triUnknown {
		return triUnknown
	}
	if when == triNo || unless == triYes {
		return triNo
	}
	return triYes
}

func lookupCondition(wfNode *yaml.Node, key string, absent tri, env pipelineEnv) tri {
	v := configLookup(wfNode, key)
	if v == nil {
		return absent
	}
	return evalConditionNode(v, env)
}

func configLookup(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func evalConditionNode(n *yaml.Node, env pipelineEnv) tri {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!bool" {
			if n.Value == "true" {
				return triYes
			}
			return triNo
		}
		s := strings.TrimSpace(n.Value)
		switch s {
		case "true":
			return triYes
		case "false":
			return triNo
		}
		if s == "" {
			return triUnknown
		}
		if t, ok := parseTemplate(s); ok {
			v, known := resolveTemplate(t, env)
			if !known {
				return triUnknown
			}
			return truthy(v)
		}
		v, known := evalExpression(s, env)
		if !known {
			return triUnknown
		}
		return truthy(v)
	case yaml.MappingNode:
		if len(n.Content) != 2 || n.Content[0].Kind != yaml.ScalarNode {
			return triUnknown
		}
		switch n.Content[0].Value {
		case "and":
			return evalLogicAndOr(n.Content[1], env, true)
		case "or":
			return evalLogicAndOr(n.Content[1], env, false)
		case "not":
			v := evalConditionNode(n.Content[1], env)
			switch v {
			case triYes:
				return triNo
			case triNo:
				return triYes
			default:
				return triUnknown
			}
		case "equal":
			return evalLogicEqual(n.Content[1], env)
		case "matches":
			return evalLogicMatches(n.Content[1], env)
		default:
			return triUnknown
		}
	default:
		return triUnknown
	}
}

func evalLogicAndOr(n *yaml.Node, env pipelineEnv, and bool) tri {
	var items []*yaml.Node
	switch n.Kind {
	case yaml.SequenceNode:
		items = n.Content
	case yaml.ScalarNode, yaml.MappingNode:
		items = []*yaml.Node{n}
	default:
		return triUnknown
	}
	if len(items) == 0 {
		return triUnknown
	}
	seen := false
	for _, it := range items {
		v := evalConditionNode(it, env)
		if v == triUnknown {
			if and {
				return triUnknown
			}
			continue
		}
		seen = true
		if and && v == triNo {
			return triNo
		}
		if !and && v == triYes {
			return triYes
		}
	}
	if !seen {
		return triUnknown
	}
	if and {
		return triYes
	}
	return triNo
}

func evalLogicEqual(n *yaml.Node, env pipelineEnv) tri {
	left, right, ok := logicPair(n)
	if !ok {
		return triUnknown
	}
	a, knownA := evalValueNode(left, env)
	b, knownB := evalValueNode(right, env)
	if !knownA || !knownB {
		return triUnknown
	}
	if scalarEqual(a, b) {
		return triYes
	}
	return triNo
}

func evalLogicMatches(n *yaml.Node, env pipelineEnv) tri {
	if n == nil || n.Kind != yaml.MappingNode {
		return triUnknown
	}
	var patternNode, valueNode *yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		switch n.Content[i].Value {
		case "pattern":
			patternNode = n.Content[i+1]
		case "value":
			valueNode = n.Content[i+1]
		}
	}
	if patternNode == nil || valueNode == nil {
		return triUnknown
	}
	pat, ok := scalarString(patternNode)
	if !ok {
		return triUnknown
	}
	val, known := evalValueNode(valueNode, env)
	if !known {
		return triUnknown
	}
	s := scalarStringValue(val)
	re, err := parsePattern(pat)
	if err != nil {
		return triUnknown
	}
	if re.MatchString(s) {
		return triYes
	}
	return triNo
}

func logicPair(n *yaml.Node) (left, right *yaml.Node, ok bool) {
	if n == nil {
		return nil, nil, false
	}
	switch n.Kind {
	case yaml.SequenceNode:
		if len(n.Content) != 2 {
			return nil, nil, false
		}
		return n.Content[0], n.Content[1], true
	case yaml.MappingNode:
		if len(n.Content) == 4 && n.Content[0].Value == "value" && n.Content[2].Value == "pattern" {
			return n.Content[1], n.Content[3], true
		}
	}
	return nil, nil, false
}

func evalValueNode(n *yaml.Node, env pipelineEnv) (any, bool) {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!bool" || n.Tag == "!!int" || n.Tag == "!!float" {
			return decodeScalar(n)
		}
		s := strings.TrimSpace(n.Value)
		if t, ok := parseTemplate(s); ok {
			return resolveTemplate(t, env)
		}
		if strings.HasPrefix(s, "pipeline.") {
			return pipelineValue(s, env)
		}
		if v, ok := evalExpressionValue(s, env); ok {
			return v, true
		}
		return s, true
	case yaml.MappingNode, yaml.SequenceNode:
		v := evalConditionNode(n, env)
		switch v {
		case triYes:
			return true, true
		case triNo:
			return false, true
		default:
			return nil, false
		}
	default:
		return nil, false
	}
}

func decodeScalar(n *yaml.Node) (any, bool) {
	switch n.Tag {
	case "!!bool":
		return n.Value == "true", true
	case "!!int":
		i, err := strconv.Atoi(n.Value)
		if err != nil {
			return nil, false
		}
		return i, true
	case "!!float":
		f, err := strconv.ParseFloat(n.Value, 64)
		if err != nil {
			return nil, false
		}
		return f, true
	default:
		return n.Value, true
	}
}

var templateRE = regexp.MustCompile(`^<<\s*(.+?)\s*>>$`)

func parseTemplate(s string) (string, bool) {
	m := templateRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

func resolveTemplate(inner string, env pipelineEnv) (any, bool) {
	switch inner {
	case "pipeline.git.branch":
		return env.Branch, true
	case "pipeline.git.tag":
		return env.Tag, true
	case "pipeline.trigger.type":
		return env.TriggerType, true
	}
	const pfx = "pipeline.parameters."
	if strings.HasPrefix(inner, pfx) {
		name := strings.TrimPrefix(inner, pfx)
		if name == "" || env.Parameters == nil {
			return nil, false
		}
		v, ok := env.Parameters[name]
		if !ok {
			return nil, false
		}
		return v, true
	}
	return nil, false
}

func truthy(v any) tri {
	if isTruthy(v) {
		return triYes
	}
	return triNo
}

func isTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case jsonNumber:
		return x.String() != "0" && x.String() != "0.0"
	case float64:
		return x != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	default:
		return true
	}
}

// jsonNumber is satisfied by encoding/json.Number.
type jsonNumber interface {
	String() string
}

func scalarEqual(a, b any) bool {
	return scalarStringValue(a) == scalarStringValue(b)
}

func scalarStringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case jsonNumber:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	default:
		return ""
	}
}

func scalarString(n *yaml.Node) (string, bool) {
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

func parsePattern(pat string) (*regexp.Regexp, error) {
	pat = strings.TrimSpace(pat)
	if strings.HasPrefix(pat, "/") {
		i := strings.LastIndex(pat, "/")
		if i <= 0 {
			return nil, errors.New("bad pattern")
		}
		body := pat[1:i]
		return regexp.Compile(body)
	}
	return regexp.Compile(regexp.QuoteMeta(pat))
}

// evalExpression parses a bare CircleCI expression string (workflow when/unless).
func evalExpression(s string, env pipelineEnv) (any, bool) {
	v, known := evalExpressionValue(s, env)
	if !known {
		return nil, false
	}
	return v, true
}

func evalExpressionValue(s string, env pipelineEnv) (any, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	v, ok := parseOrExpr(s, env)
	if !ok {
		return nil, false
	}
	return v, true
}

func parseOrExpr(s string, env pipelineEnv) (any, bool) {
	parts, ok := splitExpr(s, " or ")
	if !ok {
		return nil, false
	}
	var anyTrue, anyKnown bool
	for _, p := range parts {
		v, known := parseAndExpr(strings.TrimSpace(p), env)
		if !known {
			continue
		}
		anyKnown = true
		if isTruthy(v) {
			anyTrue = true
		}
	}
	if !anyKnown {
		return nil, false
	}
	return anyTrue, true
}

func parseAndExpr(s string, env pipelineEnv) (any, bool) {
	parts, ok := splitExpr(s, " and ")
	if !ok {
		return nil, false
	}
	known := false
	for _, p := range parts {
		v, ok := parseNotExpr(strings.TrimSpace(p), env)
		if !ok {
			return nil, false
		}
		known = true
		if !isTruthy(v) {
			return false, true
		}
	}
	if !known {
		return nil, false
	}
	return true, true
}

func parseNotExpr(s string, env pipelineEnv) (any, bool) {
	for strings.HasPrefix(s, "not ") {
		inner, ok := parseNotExpr(strings.TrimSpace(s[4:]), env)
		if !ok {
			return nil, false
		}
		return !isTruthy(inner), true
	}
	return parseCompareExpr(s, env)
}

func parseCompareExpr(s string, env pipelineEnv) (any, bool) {
	for _, op := range []string{" starts-with ", " == ", " != "} {
		if i := strings.Index(s, op); i >= 0 {
			left := strings.TrimSpace(s[:i])
			right := strings.TrimSpace(s[i+len(op):])
			lv, okL := parseValueAtom(left, env)
			rv, okR := parseValueAtom(right, env)
			if !okL || !okR {
				return nil, false
			}
			ls, rs := scalarStringValue(lv), scalarStringValue(rv)
			switch strings.TrimSpace(op) {
			case "==":
				return ls == rs, true
			case "!=":
				return ls != rs, true
			case "starts-with":
				return strings.HasPrefix(ls, rs), true
			}
		}
	}
	return parseValueAtom(s, env)
}

func parseValueAtom(s string, env pipelineEnv) (any, bool) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1], true
		}
	}
	if strings.HasPrefix(s, "pipeline.") {
		return pipelineValue(s, env)
	}
	return nil, false
}

func pipelineValue(s string, env pipelineEnv) (any, bool) {
	switch s {
	case "pipeline.git.branch":
		return env.Branch, true
	case "pipeline.git.tag":
		return env.Tag, true
	case "pipeline.trigger.type":
		return env.TriggerType, true
	}
	const pfx = "pipeline.parameters."
	if strings.HasPrefix(s, pfx) {
		name := strings.TrimPrefix(s, pfx)
		if name == "" || env.Parameters == nil {
			return nil, false
		}
		v, ok := env.Parameters[name]
		return v, ok
	}
	return nil, false
}

// splitExpr splits s on sep outside quotes.
func splitExpr(s, sep string) ([]string, bool) {
	var parts []string
	depth := 0
	start := 0
	inQuote := byte(0)
	for i := 0; i < len(s); i++ {
		switch {
		case inQuote != 0:
			if s[i] == inQuote {
				inQuote = 0
			}
		case s[i] == '"' || s[i] == '\'':
			inQuote = s[i]
		default:
			if i+len(sep) <= len(s) && s[i:i+len(sep)] == sep && depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + len(sep)
				i += len(sep) - 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(s[start:]))
	if len(parts) == 0 {
		return nil, false
	}
	return parts, true
}

// workflowConditionUnknown reports whether the workflow has when/unless the
// planner could not reduce to a literal boolean without pipeline context.
func workflowConditionUnknown(wf *config.Workflow) bool {
	switch wf.Condition() {
	case config.NoCondition, config.LiteralCondition:
		return false
	default:
		return true
	}
}
