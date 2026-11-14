package planner_test

import (
	"context"
	"strings"
	"testing"

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
		monorepo, traceConfig,
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
				o.Workflow, o.Branch, o.Mode, o.Trace, o.TraceOrbJobs = "", branch, mode, planner.TraceOn, true
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
