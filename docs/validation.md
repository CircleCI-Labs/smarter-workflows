# Validation (v0.1, 2026-11-14)

## Test layers

| Layer | What | Result |
|---|---|---|
| Unit and golden | planner, config, diff, impact map, report; 12 example scenarios with golden plans and configs; a fuzz target | pass |
| Robustness | 27 unusual config shapes (approvals mid-graph, two workflows, matrices, orb jobs, status `requires`, anchors and merge keys, null steps, renames, unicode paths). Every emitted config must keep the job graph intact, pass the guardrails, and pass `circleci config validate` | pass (9 bugs found and fixed first) |
| Guardrails | 53 Rego tests and Go fixture tests through CircleCI's policy engine | pass |
| Trace scripts | 43 checks on ext4, tmpfs and the `cimg/base` image; the atime reset tested on CircleCI's docker and machine executors | pass |
| End to end | `scripts/e2e/run-scenarios.sh`: real branches, real pipelines, and an exact comparison of the jobs that ran | 21 scenarios (v0.1; orchestrator re-runs when this branch merges) |

## End-to-end scenarios

Demo monorepo (`examples/demo`, 13 jobs, select mode). The two `worker-test` matrix jobs always run in v0.1.

| Change | Jobs that ran (besides the gate) | Job time |
|---|---|---|
| full run (`.circleci` change or `[smarter full]`) | all 13 | 162–170 s |
| docs only | docs-check, security-scan, worker-test ×2 | 35 s |
| infra only | infra-validate, security-scan, worker-test ×2 | 41 s |
| web service | lint, web-test, web-bundle (orb job, traced), security-scan, worker-test ×2 | 93 s |
| shared schema | lint, api-test, web-test, security-scan, worker-test ×2 | 99 s |
| api service, or a new file in it | lint, api-test, build, e2e (consumes build's workspace), security-scan, worker-test ×2 | 122–170 s |
| a job with `resource_class: xlarge` | nothing: the setup job stops at the guardrails, and the check fails | 0 s |
| a deleted file under `services/web` | lint, web-bundle, security-scan, worker-test ×2 | — |
| a rename under `services/api` | lint, api-test, build, e2e, security-scan, worker-test ×2 | — |
| docs and the API in one commit | docs-check, lint, api-test, build, e2e, security-scan, worker-test ×2 | — |
| only `scripts/e2e.sh` | build, e2e, security-scan, worker-test ×2 | — |
| only `package-lock.json` under web | lint, web-test, web-bundle, security-scan, worker-test ×2 | — |
| only `services/worker` | lint, security-scan, worker-test ×2 | — |

This repository (8 jobs):

| Change | Jobs that ran | Job time |
|---|---|---|
| full run | all 8 | 287 s |
| docs only | security-scan | 47 s |
| one Go package, or a new file in it | go-test, go-lint, build, e2e-plan, security-scan | 197–205 s |
| `policies/` | go-test, policy-test, security-scan | — |
| `orb/orb.yml` | go-test, orb-validate, security-scan | — |

Job time is the sum of the jobs' run times in the planned workflow, a stand-in for compute. It varies by about ±25% from run to run. Each scenario also checked that the workflow's GitHub check reported the expected conclusion. Use `scripts/e2e/run-scenarios.sh --repeat N --csv OUT` for timing medians and a savings table against the full-run scenarios.

## Bugs that validation found and fixed

- A job's `requires` on a filtered-out job: the planner now follows CircleCI's documented rule. A filtered-out requirement is ignored, unless every requirement is filtered out.
- YAML anchors, aliases and merge keys are expanded before pruning. Before the fix, they could leave dangling aliases or duplicate injected steps.
- A null `pre-steps` or `post-steps` is an empty list. Before the fix, it dropped the whole trace.
- A fallback full run still gets the gate and tracing.
- The gate says when it doesn't wait for a job with a status requirement.
- In CI only: git's background maintenance made temp-repo cleanup flaky; the orb is now validated without an API token; an org config policy can require exact image tags.

## Compose eval

Can an agent assemble jobs into a correct workflow? With the [composer skill](../skills/circleci-workflow-composer/SKILL.md), the docs MCP and the CLI checks, Claude Haiku 5.5 was given three cases, and [`examples/compose-eval/check.sh`](../examples/compose-eval/check.sh) graded each answer with no model involved.

| Case | Result | Check rounds |
|---|---|---|
| Compose the full demo workflow from bare job definitions | 18 of 18 checks | 1 |
| Compose the smallest workflow for a set of selected jobs | 5 of 5 | 1 |
| Repair a broken layout (10 findings: a cycle, a dangling requirement, a missing mandatory job, a restricted context without an approval, a gate that wasn't a no-op) | 8 of 8 | 2 |

Its full layout matches the hand-written one, with a slightly cleaner shape:
- every check fans into the no-op gate;
- the approval waits on the gate;
- the deploy keeps its restricted context and gains a `serial-group`.
The answers are in [`examples/compose-eval/answers/haiku-5-5/`](../examples/compose-eval/answers/haiku-5-5/).

## Not validated yet

- Shadow mode on a busy real repository, for real hit and miss rates.
- The approval and deploy path on the default branch beyond the hold.
- Tampering with the cached map (see limitations).
