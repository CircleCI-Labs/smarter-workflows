package planner_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/CircleCI-Labs/smarter-workflows/internal/catalog"
	"github.com/CircleCI-Labs/smarter-workflows/internal/config"
	"github.com/CircleCI-Labs/smarter-workflows/internal/guard"
	"github.com/CircleCI-Labs/smarter-workflows/internal/planner"
)

// layoutJobs defines the jobs that the layouts below use: lint and scan read the
// repository; build persists a workspace; test and e2e attach it; deploy attaches
// it too; smarter-gate is a no-op job, and docker-gate a job that runs steps.
const layoutJobs = `version: 2.1
jobs:
  lint:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - run: make lint
  scan:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - run: make scan
  build:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - run: make build
      - persist_to_workspace: {root: ., paths: [dist]}
  test:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - checkout
      - attach_workspace: {at: .}
      - run: make test
  e2e:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - attach_workspace: {at: .}
      - run: make e2e
  deploy:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - attach_workspace: {at: .}
      - run: ./deploy.sh
  hold:
    type: approval
  smarter-gate:
    type: no-op
  docker-gate:
    docker: [{image: "cimg/base:2026.11"}]
    steps:
      - run: echo gate
`

// layoutOf is a config with the jobs above and one workflow, ci, whose body
// (the `jobs:` list) is given.
func layoutOf(jobs string) string {
	return layoutJobs + "workflows:\n  ci:\n    jobs:\n" + jobs
}

// check runs the layout check, and fails the test on an error.
func check(t *testing.T, src string, o planner.LayoutOptions) *planner.LayoutResult {
	t.Helper()
	res, err := planner.CheckLayout(context.Background(), []byte(src), o)
	if err != nil {
		t.Fatalf("CheckLayout: %v", err)
	}
	return res
}

// brief lists findings as "code job", or "code" for a finding about no job.
func brief(res *planner.LayoutResult) []string {
	var out []string
	for _, f := range res.Findings {
		s := f.Code
		if f.Job != "" {
			s += " " + f.Job
		}
		out = append(out, s)
	}
	return out
}

func wantFindings(t *testing.T, res *planner.LayoutResult, want ...string) {
	t.Helper()
	got := brief(res)
	if !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v\n%+v", got, want, res.Findings)
	}
}

// find returns the finding with the code.
func find(t *testing.T, res *planner.LayoutResult, code string) planner.Finding {
	t.Helper()
	for _, f := range res.Findings {
		if f.Code == code {
			return f
		}
	}
	t.Fatalf("no finding %s in %+v", code, res.Findings)
	return planner.Finding{}
}

const gateOpts = "smarter-gate"

func withGate(o planner.LayoutOptions) planner.LayoutOptions {
	o.GateJob = gateOpts
	return o
}

func TestLayoutOfAGoodWorkflowHasNoFindings(t *testing.T) {
	res := check(t, layoutOf(`
      - lint
      - scan
      - build
      - test:
          requires: [build]
      - hold:
          requires: [test, scan]
      - deploy:
          context: deploy
          requires: [hold]
      - smarter-gate:
          requires: [lint, scan, build, test]
`), withGate(planner.LayoutOptions{Mandatory: []string{"scan"}, RestrictedContexts: []string{"deploy"}}))
	wantFindings(t, res)
	if res.Workflow != "ci" || res.Errors() != 0 {
		t.Errorf("workflow = %q, errors = %d", res.Workflow, res.Errors())
	}
}

func TestLayoutDanglingRequires(t *testing.T) {
	res := check(t, layoutOf(`
      - lint
      - build:
          name: build-linux
      - scan:
          requires: [lnt, build, <<pipeline.parameters.dyn>>]
`), planner.LayoutOptions{})
	// The expression cannot be matched before CircleCI compiles the config.
	wantFindings(t, res, "dangling-requires scan", "dangling-requires scan")
	if f := res.Findings[0]; !strings.Contains(f.Message, "scan requires lnt") || f.Severity != "error" || f.FixHint == "" {
		t.Errorf("first finding = %+v", f)
	}
	// A job that has been renamed by `name:` is required by that name.
	if f := res.Findings[1]; !strings.Contains(f.FixHint, "the entry that runs build is called build-linux") {
		t.Errorf("second finding = %+v", f)
	}
}

func TestLayoutDanglingRequiresOnAJobInsideAGroup(t *testing.T) {
	src := layoutJobs + `job-groups:
  rollout:
    jobs:
      - lint
      - scan:
          requires: [lint]
workflows:
  ci:
    jobs:
      - rollout
      - build:
          requires: [scan]
`
	res := check(t, src, planner.LayoutOptions{})
	wantFindings(t, res, "dangling-requires build")
	if f := res.Findings[0]; !strings.Contains(f.FixHint, "scan is a job inside the job group rollout") || !strings.Contains(f.FixHint, "require the group's invocation (rollout)") {
		t.Errorf("hint = %q", f.FixHint)
	}
}

func TestLayoutCycles(t *testing.T) {
	t.Run("three jobs wait for each other", func(t *testing.T) {
		res := check(t, layoutOf(`
      - lint:
          requires: [test]
      - build:
          requires: [lint]
      - test:
          requires: [build]
      - scan
`), planner.LayoutOptions{})
		wantFindings(t, res, "cycle lint")
		if f := res.Findings[0]; !strings.Contains(f.Message, "lint requires test, test requires build, build requires lint") {
			t.Errorf("message = %q", f.Message)
		}
	})
	t.Run("a job requires itself", func(t *testing.T) {
		res := check(t, layoutOf(`
      - lint:
          requires: [lint]
`), planner.LayoutOptions{})
		wantFindings(t, res, "cycle lint")
		if !strings.Contains(res.Findings[0].Message, "lint requires itself") {
			t.Errorf("message = %q", res.Findings[0].Message)
		}
	})
	t.Run("two separate cycles are two findings, and a job that only hangs off one is not in it", func(t *testing.T) {
		res := check(t, layoutOf(`
      - lint:
          requires: [scan]
      - scan:
          requires: [lint]
      - build:
          requires: [test]
      - test:
          requires: [build]
      - node/after:
          requires: [lint]
`), planner.LayoutOptions{})
		wantFindings(t, res, "cycle lint", "cycle build")
	})
}

func TestLayoutWorkspaces(t *testing.T) {
	t.Run("a job attaches a workspace and none is persisted before it", func(t *testing.T) {
		res := check(t, layoutOf(`
      - lint
      - test:
          requires: [lint]
`), planner.LayoutOptions{})
		wantFindings(t, res, "consumer-before-producer test")
		if f := res.Findings[0]; !strings.Contains(f.FixHint, "such as") && !strings.Contains(f.FixHint, "add a job that persists") {
			t.Errorf("hint = %q", f.FixHint)
		}
	})
	t.Run("the producer is there but not upstream", func(t *testing.T) {
		res := check(t, layoutOf(`
      - build
      - test
`), planner.LayoutOptions{})
		wantFindings(t, res, "consumer-before-producer test")
		if f := res.Findings[0]; !strings.Contains(f.FixHint, "such as build") {
			t.Errorf("the hint does not name the producer: %q", f.FixHint)
		}
	})
	t.Run("the producer is upstream, through another job and an approval", func(t *testing.T) {
		wantFindings(t, check(t, layoutOf(`
      - build
      - lint:
          requires: [build]
      - hold:
          requires: [lint]
      - e2e:
          requires: [hold]
`), planner.LayoutOptions{}))
	})
	t.Run("the steps of the workflow entry count: its post-steps may persist, its pre-steps may attach", func(t *testing.T) {
		wantFindings(t, check(t, layoutOf(`
      - lint:
          post-steps:
            - persist_to_workspace:
                root: .
                paths: [out]
      - scan:
          pre-steps:
            - attach_workspace:
                at: .
          requires: [lint]
      - e2e:
          requires: [scan]
      - node/after:
          pre-steps:
            - attach_workspace:
                at: .
`), planner.LayoutOptions{}))
		wantFindings(t, check(t, layoutOf(`
      - lint
      - scan:
          pre-steps:
            - attach_workspace:
                at: .
          requires: [lint]
`), planner.LayoutOptions{}), "consumer-before-producer scan")
	})
	t.Run("an orb job is unknown inside, so it counts as a producer and is not reported", func(t *testing.T) {
		wantFindings(t, check(t, layoutOf(`
      - node/build
      - test:
          requires: [node/build]
      - node/lint
`), planner.LayoutOptions{}))
	})
	t.Run("a job group is unknown inside too", func(t *testing.T) {
		src := layoutJobs + `job-groups:
  rollout:
    jobs: [lint]
workflows:
  ci:
    jobs:
      - rollout
      - test:
          requires: [rollout]
`
		wantFindings(t, check(t, src, planner.LayoutOptions{}))
	})
	t.Run("an inline orb job is read", func(t *testing.T) {
		src := layoutJobs + `orbs:
  local:
    jobs:
      pack:
        docker: [{image: "cimg/base:2026.11"}]
        steps:
          - persist_to_workspace: {root: ., paths: [pkg]}
      unpack:
        docker: [{image: "cimg/base:2026.11"}]
        steps:
          - attach_workspace: {at: .}
workflows:
  ci:
    jobs:
      - local/unpack
      - local/pack
      - lint:
          requires: [local/pack]
`
		wantFindings(t, check(t, src, planner.LayoutOptions{}), "consumer-before-producer local/unpack")
	})
}

// A layout may be a workflow that names jobs defined in another file. The
// catalog of that file says what they do with workspaces, and what they are.
func TestLayoutUsesTheCatalog(t *testing.T) {
	layout := `version: 2.1
workflows:
  ci:
    jobs:
      - build
      - test:
          requires: [build]
      - deploy
      - smarter-gate:
          requires: [test]
`
	catalogOf := func(t *testing.T, src string) *catalog.Catalog {
		t.Helper()
		cfg, err := config.Parse([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		cat, err := catalog.Build(cfg, "ci", nil)
		if err != nil {
			t.Fatal(err)
		}
		return cat
	}
	cat := catalogOf(t, layoutOf(`
      - build
      - test:
          requires: [build]
      - deploy
      - smarter-gate
`))
	t.Run("without the catalog the jobs are unknown inside", func(t *testing.T) {
		// Nothing is known of build, test and deploy, so nothing is said of their
		// workspaces. The gate is a name that nothing defines.
		wantFindings(t, check(t, layout, withGate(planner.LayoutOptions{})), "gate-missing-requirement deploy", "gate-not-noop smarter-gate")
	})
	t.Run("with the catalog", func(t *testing.T) {
		// deploy attaches a workspace and requires nothing; the gate is a no-op job.
		wantFindings(t, check(t, layout, withGate(planner.LayoutOptions{Catalog: cat})), "consumer-before-producer deploy", "gate-missing-requirement deploy")
	})
	t.Run("an approval of the catalog is an approval of the layout", func(t *testing.T) {
		cat := catalogOf(t, layoutOf(`
      - build
      - hold
      - deploy:
          requires: [hold]
`))
		src := `version: 2.1
workflows:
  ci:
    jobs:
      - build
      - hold:
          requires: [build]
      - deploy:
          context: prod
          requires: [hold]
`
		o := planner.LayoutOptions{RestrictedContexts: []string{"prod"}}
		wantFindings(t, check(t, src, o), "restricted-context-without-approval deploy")
		o.Catalog = cat
		wantFindings(t, check(t, src, o))
	})
}

func TestLayoutRestrictedContexts(t *testing.T) {
	o := planner.LayoutOptions{RestrictedContexts: []string{"deploy", "prod"}}
	t.Run("no approval upstream", func(t *testing.T) {
		res := check(t, layoutOf(`
      - build
      - e2e:
          context: [other, prod]
          requires: [build]
      - deploy:
          context: deploy
          requires: [e2e]
`), o)
		wantFindings(t, res, "restricted-context-without-approval e2e", "restricted-context-without-approval deploy")
		if f := find(t, res, "restricted-context-without-approval"); !strings.Contains(f.Message, "uses the restricted context") || f.FixHint == "" {
			t.Errorf("finding = %+v", f)
		}
	})
	t.Run("an approval upstream, set on the entry or in the definition, and a context of no concern", func(t *testing.T) {
		wantFindings(t, check(t, layoutOf(`
      - build
      - approve-it:
          type: approval
          requires: [build]
      - e2e:
          context: deploy
          requires: [approve-it]
      - hold:
          requires: [e2e]
      - deploy:
          context: prod
          requires: [hold]
      - lint:
          context: other
`), o))
	})
	t.Run("an approval itself runs no code, so it may name a restricted context", func(t *testing.T) {
		wantFindings(t, check(t, layoutOf(`
      - hold:
          context: deploy
`), o))
	})
	t.Run("without restricted contexts there is nothing to check", func(t *testing.T) {
		wantFindings(t, check(t, layoutOf(`
      - deploy:
          context: deploy
`), planner.LayoutOptions{}), "consumer-before-producer deploy")
	})
}

func TestLayoutMandatoryJobs(t *testing.T) {
	res := check(t, layoutOf(`
      - lint
      - scan:
          name: security-scan
`), planner.LayoutOptions{Mandatory: []string{"scan", "security-scan", "lint"}})
	// A job is matched by the name it has in the workflow.
	wantFindings(t, res, "mandatory-missing scan")
	if f := res.Findings[0]; f.Severity != "error" || !strings.Contains(f.Message, "mandatory job scan is not in workflow ci") {
		t.Errorf("finding = %+v", f)
	}
}

func TestLayoutGate(t *testing.T) {
	gate := func(requires string) string {
		return "      - smarter-gate:\n          requires: [" + requires + "]\n"
	}
	t.Run("there is no gate", func(t *testing.T) {
		res := check(t, layoutOf("      - lint\n"), withGate(planner.LayoutOptions{}))
		wantFindings(t, res, "gate-missing smarter-gate")
	})
	t.Run("the gate checks can be switched off", func(t *testing.T) {
		wantFindings(t, check(t, layoutOf("      - lint\n"), planner.LayoutOptions{}))
	})
	t.Run("another name for the gate", func(t *testing.T) {
		res := check(t, layoutOf("      - lint\n      - smarter-gate:\n          name: pr-gate\n          requires: [lint]\n"), planner.LayoutOptions{GateJob: "pr-gate"})
		wantFindings(t, res)
	})
	t.Run("the gate is a job that runs steps", func(t *testing.T) {
		res := check(t, layoutOf("      - lint\n      - docker-gate:\n          name: smarter-gate\n          requires: [lint]\n"), withGate(planner.LayoutOptions{}))
		wantFindings(t, res, "gate-not-noop smarter-gate")
		if f := res.Findings[0]; !strings.Contains(f.Message, "a job that runs steps") || !strings.Contains(f.FixHint, "{type: no-op}") {
			t.Errorf("finding = %+v", f)
		}
	})
	t.Run("a job that will run is not awaited", func(t *testing.T) {
		res := check(t, layoutOf("      - lint\n      - scan\n      - build\n"+gate("lint")), withGate(planner.LayoutOptions{}))
		wantFindings(t, res, "gate-missing-requirement scan", "gate-missing-requirement build")
		if f := res.Findings[0]; !strings.Contains(f.FixHint, "add scan to the requires of smarter-gate") {
			t.Errorf("hint = %q", f.FixHint)
		}
	})
	t.Run("a job is awaited when the gate waits for what comes after it", func(t *testing.T) {
		wantFindings(t, check(t, layoutOf("      - lint\n      - build:\n          requires: [lint]\n      - test:\n          requires: [build]\n"+gate("test")), withGate(planner.LayoutOptions{})))
	})
	t.Run("what the gate does not wait for", func(t *testing.T) {
		res := check(t, layoutOf(`
      - lint
      - hold:
          requires: [lint]
      - scan:
          requires: [hold]
      - node/job
      - node/after:
          requires: [node/job]
      - smarter-gate:
          requires: [lint, node/after]
      - notify:
          requires: [smarter-gate]
`), withGate(planner.LayoutOptions{}))
		// The approval, the job behind it, and the job that comes after the gate
		// are not awaited. A job that the gate waits for through another one is
		// awaited.
		wantFindings(t, res)
	})
	t.Run("a no-op job and a job group", func(t *testing.T) {
		src := layoutJobs + `job-groups:
  rollout:
    jobs: [lint]
workflows:
  ci:
    jobs:
      - scan
      - smarter-gate:
          name: loose
          requires: [scan]
      - rollout
      - smarter-gate:
          requires: [scan]
`
		// A group has to be awaited like any job; a no-op job does not.
		res := check(t, src, withGate(planner.LayoutOptions{}))
		wantFindings(t, res, "gate-missing-requirement rollout")
	})
	t.Run("the gate waits for a job that requires a status other than success", func(t *testing.T) {
		res := check(t, layoutOf(`
      - lint
      - scan:
          requires:
            - lint: failed
      - smarter-gate:
          requires: [scan]
`), withGate(planner.LayoutOptions{}))
		wantFindings(t, res, "gate-awaits-status-job scan")
		if f := res.Findings[0]; !strings.Contains(f.Message, "requires a job status other than success") {
			t.Errorf("message = %q", f.Message)
		}
	})
	t.Run("the gate itself requires a status", func(t *testing.T) {
		res := check(t, layoutOf(`
      - lint
      - smarter-gate:
          requires:
            - lint: [success, failed]
`), withGate(planner.LayoutOptions{}))
		wantFindings(t, res, "gate-awaits-status-job smarter-gate")
	})
	t.Run("a job that waits for a status is not one the gate has to await", func(t *testing.T) {
		wantFindings(t, check(t, layoutOf(`
      - lint
      - scan:
          requires:
            - lint: failed
      - smarter-gate:
          requires: [lint]
`), withGate(planner.LayoutOptions{})))
	})
}

func TestLayoutFilteredApprovalFootgun(t *testing.T) {
	t.Run("the approval is left out on branches, the job is not", func(t *testing.T) {
		res := check(t, layoutOf(`
      - test:
          requires: [build]
      - build
      - hold:
          requires: [build]
          filters:
            branches:
              only: main
      - deploy:
          requires: [hold, test]
`), planner.LayoutOptions{})
		wantFindings(t, res, "filtered-approval-footgun deploy")
		f := res.Findings[0]
		if f.Severity != "warning" || !strings.Contains(f.Message, "requires the approval hold") || !strings.Contains(f.Message, "CircleCI ignores the approval") {
			t.Errorf("finding = %+v", f)
		}
		if res.Errors() != 0 {
			t.Errorf("a warning is not an error: %d errors", res.Errors())
		}
	})
	t.Run("the job has the filters of the approval", func(t *testing.T) {
		wantFindings(t, check(t, layoutOf(`
      - build
      - test:
          requires: [build]
      - hold:
          requires: [build]
          filters:
            branches:
              only: main
      - deploy:
          requires: [hold, test]
          filters:
            branches:
              only: main
`), planner.LayoutOptions{}))
	})
	t.Run("the job requires only the approval, so it does not run where the approval is left out", func(t *testing.T) {
		wantFindings(t, check(t, layoutOf(`
      - build
      - hold:
          requires: [build]
          filters:
            branches:
              only: main
      - deploy:
          requires: [hold]
`), planner.LayoutOptions{}))
	})
	t.Run("regular expressions and tags", func(t *testing.T) {
		res := check(t, layoutOf(`
      - build:
          filters:
            tags:
              only: /^v.*/
            branches:
              ignore: /.*/
      - hold:
          requires: [build]
          filters:
            branches:
              only: main
      - deploy:
          requires: [hold, build]
          filters:
            tags:
              only: /^v.*/
            branches:
              ignore: /.*/
`), planner.LayoutOptions{})
		wantFindings(t, res, "filtered-approval-footgun deploy")
		if !strings.Contains(res.Findings[0].Message, "tag v") {
			t.Errorf("message = %q, want a tag that the regular expression matches", res.Findings[0].Message)
		}
	})
	t.Run("an approval with no filters is always there", func(t *testing.T) {
		wantFindings(t, check(t, layoutOf(`
      - build
      - test:
          requires: [build]
      - hold:
          requires: [build]
      - deploy:
          requires: [hold, test]
`), planner.LayoutOptions{}))
	})
	t.Run("an approval whose own requirements are filtered out is left out as well", func(t *testing.T) {
		res := check(t, layoutOf(`
      - build
      - test:
          requires: [build]
      - package:
          filters:
            branches:
              only: main
      - hold:
          requires: [package]
      - deploy:
          requires: [hold, test]
`), planner.LayoutOptions{})
		wantFindings(t, res, "filtered-approval-footgun deploy")
	})
}

// stubChecker returns a canned result.
type stubChecker struct {
	res guard.Result
	err error
}

func (s stubChecker) Check(context.Context, []byte) (guard.Result, error) { return s.res, s.err }

func TestLayoutPolicyFindings(t *testing.T) {
	src := layoutOf("      - lint\n")
	t.Run("a hard violation is an error and a soft one a warning", func(t *testing.T) {
		res := check(t, src, planner.LayoutOptions{Checker: stubChecker{res: guard.Result{Status: guard.HardFail, Violations: []guard.Violation{
			{Policy: "org", Rule: "required_jobs", Hard: true, Message: "workflow ci is missing   security-scan [smarter:require-job=security-scan]", RequireJobs: []string{"security-scan"}},
			{Policy: "org", Rule: "registry_orbs_pinned", Hard: false, Message: "orb node is not pinned"},
		}}}})
		wantFindings(t, res, "policy:required_jobs security-scan", "policy:registry_orbs_pinned")
		if f := res.Findings[0]; f.Severity != "error" || f.Message != "workflow ci is missing security-scan [smarter:require-job=security-scan]" || !strings.Contains(f.FixHint, "add the job security-scan") {
			t.Errorf("finding = %+v", f)
		}
		if f := res.Findings[1]; f.Severity != "warning" || !strings.Contains(f.FixHint, "policy org") {
			t.Errorf("finding = %+v", f)
		}
		if res.Errors() != 1 {
			t.Errorf("errors = %d, want 1", res.Errors())
		}
	})
	t.Run("a bundle that cannot be evaluated is a failure, not a finding", func(t *testing.T) {
		_, err := planner.CheckLayout(context.Background(), []byte(src), planner.LayoutOptions{Checker: stubChecker{err: errors.New("boom")}})
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("error = %v", err)
		}
		_, err = planner.CheckLayout(context.Background(), []byte(src), planner.LayoutOptions{Checker: stubChecker{res: guard.Result{
			Status: guard.Error, Violations: []guard.Violation{{Rule: "error", Hard: true, Message: "no such rule"}}}}})
		if err == nil || !strings.Contains(err.Error(), "no such rule") {
			t.Errorf("error = %v", err)
		}
	})
}

func TestLayoutDuplicateNames(t *testing.T) {
	res := check(t, layoutOf(`
      - lint
      - scan
      - lint
`), planner.LayoutOptions{})
	wantFindings(t, res, "duplicate-name lint")
}

func TestLayoutErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := planner.CheckLayout(ctx, []byte("a: ["), planner.LayoutOptions{}); err == nil {
		t.Error("an unparseable config is checked")
	}
	two := layoutJobs + "workflows:\n  a:\n    jobs: [lint]\n  b:\n    jobs: [scan]\n"
	_, err := planner.CheckLayout(ctx, []byte(two), planner.LayoutOptions{})
	var ue *planner.UsageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "2 workflows (a, b)") {
		t.Errorf("two workflows and none named: %v", err)
	}
	if res, err := planner.CheckLayout(ctx, []byte(two), planner.LayoutOptions{Workflow: "b"}); err != nil || res.Workflow != "b" {
		t.Errorf("workflow b: %v, %v", res, err)
	}
	if _, err := planner.CheckLayout(ctx, []byte(two), planner.LayoutOptions{Workflow: "c"}); !errors.As(err, &ue) {
		t.Errorf("an unknown workflow: %v", err)
	}
}

// The findings come out in a fixed order, errors first, and the same on every run.
func TestLayoutFindingsAreSortedAndStable(t *testing.T) {
	src := layoutOf(`
      - test
      - build
      - scan:
          requires: [nope]
      - hold:
          requires: [build]
          filters:
            branches:
              only: main
      - deploy:
          requires: [hold, build]
          context: prod
`)
	o := withGate(planner.LayoutOptions{Mandatory: []string{"security-scan"}, RestrictedContexts: []string{"prod"}})
	first := brief(check(t, src, o))
	want := []string{
		"consumer-before-producer test",
		"dangling-requires scan",
		"gate-missing smarter-gate",
		"mandatory-missing security-scan",
		"filtered-approval-footgun deploy",
	}
	if !slices.Equal(first, want) {
		t.Fatalf("findings = %v, want %v", first, want)
	}
	for i := 0; i < 5; i++ {
		if again := brief(check(t, src, o)); !slices.Equal(again, first) {
			t.Fatalf("run %d: %v", i, fmt.Sprint(again))
		}
	}
}
