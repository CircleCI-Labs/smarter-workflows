package planner

import (
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"go.yaml.in/yaml/v3"
)

func mustWFNode(t *testing.T, body string) *yaml.Node {
	t.Helper()
	body = strings.TrimSpace(body)
	if body != "" && !strings.HasPrefix(body, "    ") {
		body = "    " + body
	}
	cfg, err := config.Parse([]byte("version: 2.1\nworkflows:\n  w:\n" + body + "\n    jobs: [a]\n"))
	if err != nil {
		t.Fatal(err)
	}
	wf, err := cfg.Workflow("w")
	if err != nil {
		t.Fatal(err)
	}
	return wf.Node()
}

func mkEnv(params map[string]any, branch string) pipelineEnv {
	return pipelineEnv{Branch: branch, Parameters: params}
}

func TestEvalWorkflowRuns(t *testing.T) {
	tests := []struct {
		name string
		body string
		pe   pipelineEnv
		want tri
	}{
		{"no condition", "", mkEnv(nil, "main"), triYes},
		{"when true", "when: true", mkEnv(nil, "main"), triYes},
		{"when false", "when: false", mkEnv(nil, "main"), triNo},
		{"unless false", "unless: false", mkEnv(nil, "main"), triYes},
		{"unless true", "unless: true", mkEnv(nil, "main"), triNo},
		{"param template true", "when: << pipeline.parameters.run >>", mkEnv(map[string]any{"run": true}, "main"), triYes},
		{"param template false", "when: << pipeline.parameters.run >>", mkEnv(map[string]any{"run": false}, "main"), triNo},
		{"missing param", "when: << pipeline.parameters.run >>", mkEnv(nil, "main"), triUnknown},
		{"branch eq", `when: pipeline.git.branch == "main"`, mkEnv(nil, "main"), triYes},
		{"branch ne", `when: pipeline.git.branch == "main"`, mkEnv(nil, "feature"), triNo},
		{"branch starts-with", `when: pipeline.git.branch starts-with "feature"`, mkEnv(nil, "feature/x"), triYes},
		{"not param", "    when:\n      not: << pipeline.parameters.dry >>", mkEnv(map[string]any{"dry": false}, "main"), triYes},
		{"and", "    when:\n      and:\n        - true\n        - << pipeline.parameters.x >>", mkEnv(map[string]any{"x": true}, "main"), triYes},
		{"or short-circuit unknown", "    when:\n      or:\n        - << pipeline.parameters.missing >>\n        - true", mkEnv(nil, "main"), triYes},
		{"equal branch", "    when:\n      equal:\n        - main\n        - pipeline.git.branch", mkEnv(nil, "main"), triYes},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := mustWFNode(t, tc.body)
			got := evalWorkflowRuns(node, tc.pe)
			if got != tc.want {
				t.Fatalf("evalWorkflowRuns = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvalWorkflowRunsLogicMatches(t *testing.T) {
	body := `    when:
      matches:
        pattern: /^feat.*/
        value: pipeline.git.branch`
	node := mustWFNode(t, body)
	if got := evalWorkflowRuns(node, mkEnv(nil, "feature/foo")); got != triYes {
		t.Fatalf("got %v", got)
	}
	if got := evalWorkflowRuns(node, mkEnv(nil, "main")); got != triNo {
		t.Fatalf("got %v", got)
	}
}
