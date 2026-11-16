---
page-title: Validation
page-audience: admin
page-description: The test layers, the end-to-end scenarios, the bugs that validation found, and the compose eval of v0.1, and what is not validated yet.
---

# Validation

What was tested in v0.1, and what each layer found. Unit, robustness and guardrail results are from 2026-11-14; end-to-end and backtest numbers are from 2026-11-15. Runtime tracing was removed in v0.3 (ADR 8); the trace-script layer and atime checks below are historical only and no longer gate merges.

## Test layers

| Layer | What | Result |
|---|---|---|
| Unit and golden | planner, config, diff, impact map, report; 12 example scenarios with golden plans and configs; a fuzz target | pass |
| Robustness | 27 unusual config shapes (approvals mid-graph, two workflows, matrices, orb jobs, status `requires`, anchors and merge keys, null steps, renames, unicode paths). Every emitted config must keep the job graph intact, pass the guardrails, and pass `circleci config validate` | pass (9 bugs found and fixed first) |
| Guardrails | 53 Rego tests and Go fixture tests through CircleCI's policy engine | pass |
| Trace scripts (v0.1 only; removed v0.3) | 43 checks on ext4, tmpfs and the `cimg/base` image; the atime reset tested on CircleCI's docker and machine executors | pass (historical) |
| End to end | `scripts/e2e/run-scenarios.sh`: real branches, real pipelines, and an exact comparison of the jobs that ran | pass (57/63 job-set checks on first batch; two scenario definitions fixed and rerun — see below) |

## End-to-end scenarios

Twenty-one scenarios on two targets: the [demo monorepo](../examples/demo/README.md) (16 scenarios, 13 jobs, select mode) and this repository (5 scenarios, 8 jobs). Each scenario ran **three times** on the same base commit (**63** pipeline runs total). Every run checked the jobs that ran (besides `smarter-gate`), guardrail outcome where relevant, and the continued workflow's GitHub check conclusion.

**First batch (63 runs): 57 passed** the job-set check. **Six did not:**

| Scenario | What went wrong |
| --- | --- |
| `self-policies` (×3) | The scenario expected only `go-test`, `policy-test`, `release-dry-run`, and `security-scan`. Adding `policies/NOTES.md` also satisfies `build`, `go-lint`, and `e2e-plan`, which list `policies/` or depend on `build`'s workspace — by design. |
| `self-orb` (×3) | The change step edited `orb/orb.yml`, which no longer exists; the orb lives under `orb/src/@orb.yml`. The branch was never pushed. |

**After fixing the scenarios:** `self-policies` was rerun once with the expanded expected job set (`build`, `e2e-plan`, `go-lint`, `go-test`, `policy-test`, `release-dry-run`, `security-scan`) and **passed**. `self-orb` was rerun once against `orb/src/@orb.yml`; the planner ran **`orb-validate`** and **`security-scan`** only (Go inputs unchanged), matching the corrected expectation. The first batch therefore reported **57/63** job-set matches; with the two definition fixes verified on real pipelines, all **21** scenarios are accounted for. See the savings table in [Benchmarks](benchmarks.md#end-to-end-savings).

The two `worker-test` matrix jobs always run on the demo target in v0.1.

### Demo target (median job seconds, three runs)

| Change | Jobs that ran (besides the gate) | Median job s |
|---|---|---:|
| full run (`.circleci` change or `[smarter full]`) | all 13 | 154–162 |
| docs only | docs-check, security-scan, worker-test ×2 | 22 |
| infra only | infra-validate, security-scan, worker-test ×2 | 32 |
| web service | lint, web-test, web-bundle, security-scan, worker-test ×2 | 60 |
| shared schema | lint, api-test, web-test, security-scan, worker-test ×2 | 107 |
| api service, or a new file in it | lint, api-test, build, e2e, security-scan, worker-test ×2 | 130–138 |
| rename under `services/api` | lint, api-test, build, e2e, security-scan, worker-test ×2 | 113 |
| docs and the API in one commit | docs-check, lint, api-test, build, e2e, security-scan, worker-test ×2 | 131 |
| only `scripts/e2e.sh` | build, e2e, security-scan, worker-test ×2 | 59 |
| lockfile under web (no unit-test job) | lint, web-bundle, security-scan, worker-test ×2 | 68 |
| deleted file under `services/web` | lint, web-bundle, web-test, security-scan, worker-test ×2 | 65 |
| only `services/worker` | lint, security-scan, worker-test ×2 | 57 |
| `resource_class: xlarge` | nothing: setup stops at guardrails | — |

### This repository (median job seconds, three runs)

| Change | Jobs that ran (besides the gate) | Median job s |
|---|---|---:|
| full run (`[smarter full]`) | all 8 | 537 |
| docs only | docs-site-build, security-scan | 45 |
| one Go package, or a new file under it | go-test, go-lint, build, e2e-plan, release-dry-run, security-scan | 402–423 |
| `policies/` (file added) | build, e2e-plan, go-lint, go-test, policy-test, release-dry-run, security-scan | (see rerun above) |
| orb source only | orb-validate, security-scan | (see rerun above) |

**Job seconds** sum each job's run time in the continued workflow. They approximate compute, not credits (resource class matters), and exclude queue time. Use `scripts/e2e/run-scenarios.sh --repeat N --csv OUT` to reproduce medians and the savings table against each target's full-run scenario.

## Bugs that validation found and fixed

- A job's `requires` on a filtered-out job: the planner now follows CircleCI's documented rule. A filtered-out requirement is ignored, unless every requirement is filtered out.
- YAML anchors, aliases and merge keys are expanded before pruning. Before the fix, they could leave dangling aliases or duplicate injected steps.
- A null `pre-steps` or `post-steps` is an empty list (relevant when v0.1 injected trace steps).
- A fallback full run still gets the gate.
- The gate says when it does not wait for a job with a status requirement.
- In CI only: git's background maintenance made temp-repo cleanup flaky; the orb is now validated without an API token; some orgs' config policies allow only exact image tags, so the language images of the pipeline carry a tag and no digest.

## Compose eval

The [compose eval](../examples/compose-eval/README.md) asks: can an agent assemble jobs into a correct workflow? With the [composer skill](../skills/circleci-workflow-composer/SKILL.md), the docs MCP and the CLI checks, Claude Haiku 5.5 was given three cases, and [`examples/compose-eval/check.sh`](../examples/compose-eval/check.sh) graded each answer with no model involved.

| Case | Result | Check rounds |
|---|---|---|
| Compose the full demo workflow from bare job definitions | 18 of 18 checks | 1 |
| Compose the smallest workflow for a set of selected jobs | 5 of 5 | 1 |
| Repair a broken layout (10 findings: a cycle, a dangling requirement, a missing mandatory job, a restricted context without an approval, a gate that was not a no-op) | 8 of 8 | 2 |

Its full layout matches the hand-written one, with a slightly cleaner shape:

- every check fans into the no-op gate;
- the approval waits on the gate;
- the deploy keeps its restricted context and gains a `serial-group`.

The answers are in [`examples/compose-eval/answers/haiku-5-5/`](../examples/compose-eval/answers/haiku-5-5/).

## Not validated yet

- Shadow mode on a busy real repository, for real hit and miss rates.
- The approval and deploy path on the default branch beyond the hold.
- Tampering with the cached map (see limitations).
