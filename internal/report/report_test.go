package report_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/report"
)

func sample() *report.Plan {
	return &report.Plan{
		Mode: "select", Workflow: "ci",
		Base: "b1", Head: "h1", Branch: "feature",
		MapRevision: "m1", MapCreatedAt: "2026-11-12T18:30:00Z",
		Changed: []report.Change{{Path: "services/api/main.go", Status: "M"}},
		Jobs: []report.Job{
			{Name: "api-test", Run: true, Reasons: []string{"reads services/api/main.go"}},
			{Name: "docs", Run: false, Reasons: []string{"inputs unchanged"}},
		},
		Gate:       &report.Gate{Name: "smarter-gate", Requires: []string{"api-test"}},
		Guardrails: report.Guardrails{Status: "PASS"},
	}
}

func TestJSONMatchesTheSpecShape(t *testing.T) {
	data, err := sample().JSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "version": 1,
  "mode": "select",
  "full": false,
  "full_reason": "",
  "workflow": "ci",
  "base": "b1",
  "head": "h1",
  "branch": "feature",
  "map_revision": "m1",
  "map_created_at": "2026-11-12T18:30:00Z",
  "changed": [
    {
      "path": "services/api/main.go",
      "status": "M"
    }
  ],
  "jobs": [
    {
      "name": "api-test",
      "run": true,
      "reasons": [
        "reads services/api/main.go"
      ]
    },
    {
      "name": "docs",
      "run": false,
      "reasons": [
        "inputs unchanged"
      ]
    }
  ],
  "gate": {
    "name": "smarter-gate",
    "requires": [
      "api-test"
    ]
  },
  "guardrails": {
    "status": "PASS",
    "repairs": [],
    "violations": []
  },
  "warnings": []
}
`
	if string(data) != want {
		t.Fatalf("--- want\n%s\n--- got\n%s", want, data)
	}
}

func TestJSONEmptyListsAreNotNull(t *testing.T) {
	p := &report.Plan{Mode: "shadow", Jobs: []report.Job{{Name: "a"}}, Trace: &report.Trace{MapJob: "m"}, Gate: &report.Gate{Name: "g"}}
	data, err := p.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "null") {
		t.Fatalf("null in output:\n%s", data)
	}
	var back report.Plan
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Version != 1 || back.Trace.MapJob != "m" {
		t.Fatalf("round trip: %+v", back)
	}
}

func TestJSONDoesNotEscapeHTML(t *testing.T) {
	p := sample()
	p.Warnings = []string{"a <b> & c"}
	data, err := p.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "a <b> & c") {
		t.Fatalf("HTML escaped:\n%s", data)
	}
}

func TestGateIsOmittedWhenThereIsNone(t *testing.T) {
	p := sample()
	p.Gate = nil
	data, _ := p.JSON()
	if strings.Contains(string(data), `"gate"`) {
		t.Fatalf("gate present:\n%s", data)
	}
}

func TestMarkdown(t *testing.T) {
	many := make([]report.Change, 53)
	for i := range many {
		many[i] = report.Change{Path: fmt.Sprintf("dir/file%02d.go", i), Status: "M"}
	}
	tests := []struct {
		name string
		mod  func(p *report.Plan)
		want string
	}{
		{
			name: "select mode",
			mod:  func(p *report.Plan) {},
			want: `**Smarter Workflows, select mode:** 1 of 2 jobs running.

| Job | Run | Reason |
|---|---|---|
| api-test | run | reads services/api/main.go |
| docs | skip | inputs unchanged |

**Changed files (1)**

- M services/api/main.go
`,
		},
		{
			name: "shadow mode says every job runs",
			mod:  func(p *report.Plan) { p.Mode = "shadow" },
			want: `**Smarter Workflows, shadow mode:** every job runs; select mode would run 1 of 2.

| Job | Select mode | Reason |
|---|---|---|
| api-test | run | reads services/api/main.go |
| docs | skip | inputs unchanged |

**Changed files (1)**

- M services/api/main.go
`,
		},
		{
			name: "full run with its reason, no changes listed",
			mod: func(p *report.Plan) {
				p.Full, p.FullReason = true, "default branch main"
				p.Changed = nil
				p.Jobs = []report.Job{{Name: "a", Run: true, Reasons: []string{"full run"}}}
			},
			want: `**Smarter Workflows, select mode:** 1 of 1 jobs running. Full run: default branch main.

| Job | Run | Reason |
|---|---|---|
| a | run | full run |
`,
		},
		{
			name: "only the first reason shows, and cells are escaped",
			mod: func(p *report.Plan) {
				p.Jobs = []report.Job{{Name: "a|b", Run: true, Reasons: []string{"reads x|y\nz", "second"}}, {Name: "c", Run: false}}
				p.Changed = nil
			},
			want: `**Smarter Workflows, select mode:** 1 of 2 jobs running.

| Job | Run | Reason |
|---|---|---|
| a\|b | run | reads x\|y z |
| c | skip |  |
`,
		},
		{
			name: "guardrails and warnings",
			mod: func(p *report.Plan) {
				p.Changed = nil
				p.Guardrails = report.Guardrails{
					Status:  "SOFT_FAIL",
					Repairs: []string{"require-job=security-scan"},
					Violations: []report.Violation{
						{Policy: "org", Rule: "resource_class", Message: "large is not allowed", Hard: false},
						{Policy: "org", Rule: "required_jobs", Message: "missing\nsecurity-scan", Hard: true},
					},
				}
				p.Warnings = []string{"the map is old"}
			},
			want: `**Smarter Workflows, select mode:** 1 of 2 jobs running.

| Job | Run | Reason |
|---|---|---|
| api-test | run | reads services/api/main.go |
| docs | skip | inputs unchanged |

**Guardrails: SOFT_FAIL**

- repaired: require-job=security-scan
- soft violation, resource_class: large is not allowed
- hard violation, required_jobs: missing security-scan

**Warnings**

- the map is old
`,
		},
		{
			name: "a repaired plan shows its repair even though it passes",
			mod: func(p *report.Plan) {
				p.Changed = nil
				p.Guardrails = report.Guardrails{Status: "PASS", Repairs: []string{"require-job=security-scan"}}
			},
			want: `**Smarter Workflows, select mode:** 1 of 2 jobs running.

| Job | Run | Reason |
|---|---|---|
| api-test | run | reads services/api/main.go |
| docs | skip | inputs unchanged |

**Guardrails: PASS**

- repaired: require-job=security-scan
`,
		},
		{
			name: "no policy bundle: no guardrails section",
			mod: func(p *report.Plan) {
				p.Changed = nil
				p.Guardrails.Status = "SKIPPED"
			},
			want: `**Smarter Workflows, select mode:** 1 of 2 jobs running.

| Job | Run | Reason |
|---|---|---|
| api-test | run | reads services/api/main.go |
| docs | skip | inputs unchanged |
`,
		},
		{
			name: "at most 50 changed files",
			mod:  func(p *report.Plan) { p.Changed = many },
			want: func() string {
				var b strings.Builder
				b.WriteString("**Smarter Workflows, select mode:** 1 of 2 jobs running.\n\n| Job | Run | Reason |\n|---|---|---|\n| api-test | run | reads services/api/main.go |\n| docs | skip | inputs unchanged |\n\n**Changed files (53)**\n\n")
				for i := 0; i < 50; i++ {
					fmt.Fprintf(&b, "- M dir/file%02d.go\n", i)
				}
				b.WriteString("- ... and 3 more\n")
				return b.String()
			}(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := sample()
			tc.mod(p)
			if got := p.Markdown(); got != tc.want {
				t.Fatalf("--- want\n%s\n--- got\n%s", tc.want, got)
			}
		})
	}
}

func TestWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "plan")
	if err := report.Write(dir, sample()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plan.json", "plan.md"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(b) == 0 {
			t.Fatalf("%s: %v (%d bytes)", name, err, len(b))
		}
	}
}
