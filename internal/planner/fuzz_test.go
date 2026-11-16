package planner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/catalog"
	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

// FuzzPlanner feeds the planner configs it was not written for. A panic inside
// the planner is recovered and turned into a full plan (that is the contract),
// so the fuzz target looks for the recovery itself: it is a bug, not an input
// the planner may reject. Run it with
//
//	go test ./internal/planner -run xxx -fuzz FuzzPlanner -fuzztime 30s
func FuzzPlanner(f *testing.F) {
	for _, seed := range []string{
		monorepo,
		"version: 2.1\nworkflows:\n  w:\n    jobs: [a, b]\n",
		"version: 2.1\nworkflows:\n  w:\n    jobs:\n      - a: &x\n          requires: [b]\n      - b: *x\n",
		"version: 2.1\nx: &a [*a]\nworkflows:\n  w:\n    jobs: *a\n",
		"version: 2.1\nworkflows:\n  w:\n    jobs:\n      - a:\n          <<: [{requires: [b]}, {name: c}]\n      - b\n",
		"version: 2.1\nworkflows:\n  w:\n    jobs:\n      - a:\n          matrix: {alias: z, parameters: {p: [1]}}\n          requires: [{b: failed}, c]\n          filters: 'x'\n",
	} {
		f.Add(seed)
	}
	m := monorepoMap()
	f.Fuzz(func(t *testing.T, cfg string) {
		for _, branch := range []string{"feature", "main"} {
			for _, mode := range []planner.Mode{planner.ModeSelect, planner.ModeShadow} {
				o := opts()
				o.Workflow, o.Branch, o.Mode = "", branch, mode
				repo := &fakeRepo{changes: changes("M docs/index.md", "A services/api/new.go", "D e2e/smoke.test.ts"),
					dirs: map[string]map[string]bool{"base1": sourceDirs(), "head1": sourceDirs()}}
				res, err := planner.Run(context.Background(), planner.Input{Config: []byte(cfg), Map: m, Repo: repo}, o)
				if err != nil {
					continue // an unusable config, or one that needs --workflow
				}
				all := res.Report.FullReason + "\n" + strings.Join(res.Report.Warnings, "\n")
				if strings.Contains(all, "planner panic") {
					t.Fatalf("the planner panicked on:\n%s\n%s", cfg, all)
				}
			}
		}
	})
}

// FuzzLayoutAndCatalog feeds the layout check and the catalog configs they were
// not written for. Neither may panic, whatever the shape of the YAML. Run it with
//
//	go test ./internal/planner -run xxx -fuzz FuzzLayoutAndCatalog -fuzztime 30s
func FuzzLayoutAndCatalog(f *testing.F) {
	for _, seed := range []string{
		monorepo, layoutOf("      - lint\n      - smarter-gate:\n          requires: [lint]\n"),
		"version: 2.1\nworkflows:\n  w:\n    jobs: [a, b]\n",
		"version: 2.1\nworkflows:\n  w:\n    jobs:\n      - a:\n          requires: [b]\n      - b:\n          requires: [a]\n",
		"version: 2.1\njob-groups:\n  g:\n    jobs: [x, {y: {requires: [x], name: z}}]\nworkflows:\n  w:\n    jobs:\n      - g:\n          serial-group: s\n      - a:\n          requires: [y]\n",
		"version: 2.1\norbs:\n  o:\n    jobs:\n      j: {machine: true, steps: [checkout, {run: 1}, {run: {command: [x]}}, 5]}\nworkflows:\n  w:\n    jobs:\n      - o/j\n      - a:\n          type: approval\n          filters: {branches: {only: ['/[/', '/^a+$/', '/(?=x)/']}, tags: x}\n          context: [1, {a: b}]\n",
		"version: 2.1\njobs:\n  a: {type: [no-op], docker: x, executor: {name: [e]}, steps: {persist_to_workspace: {paths: 3}}}\nworkflows:\n  w:\n    jobs:\n      - a:\n          matrix: {parameters: {p: {q: 1}}}\n          pre-steps: x\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		cfg, err := config.Parse([]byte(src))
		if err != nil {
			return
		}
		name, err := planner.ChooseWorkflow(cfg, "")
		if err != nil {
			return
		}
		if cat, err := catalog.Build(cfg, name, nil); err == nil {
			if _, err := cat.JSON(); err != nil {
				t.Fatalf("the catalog does not encode: %v", err)
			}
		}
		_, _ = planner.CheckLayout(context.Background(), []byte(src), planner.LayoutOptions{
			GateJob: "smarter-gate", Mandatory: []string{"a"}, RestrictedContexts: []string{"deploy", "1"},
		})
	})
}
