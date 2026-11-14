# Shaping patterns

Layout patterns for a workflow, each with a small YAML example. Tags such as [cook#coordinator] are sources; the URLs are at the end of the file.
`(compiler-checked)` means confirmed with `circleci config validate` or `config process` (CLI 1.6.0). `(heuristic)` means advice, not a documented rule.

| Situation | Pattern |
|---|---|
| Several jobs need one job's output | §1 fan-out and fan-in |
| N jobs feed M jobs | §2 coordinator |
| A pull request needs one required check | §3 gate |
| Cheap checks should stop expensive jobs | §4 fail fast |
| The workflow is slow | §5 critical path |
| A deploy needs a person | §6 approval |
| Something must run after a deploy, pass or fail | §7 cleanup and notify |
| Deploys must not overlap; a set of jobs repeats | §8 serial-group, job-groups |
| A job is known to be flaky | §9 automatic reruns |
| A job needs secrets, or must be limited to some people | §10 contexts |

## 0. Job types you place

| `type` | What you do |
|---|---|
| `build` (default) | place it as the catalog has it |
| `approval` | create it in a workflow entry. It needs no definition under `jobs:`, and steps given to it never run. [wf#approval] |
| `no-op` | create it under `jobs:` as `type: no-op`: no steps, no credits. Use it for coordinators and the gate. [cook#coordinator] |
| `release` | leave it as the catalog has it: it binds the pipeline to a deploy plan (`plan_name`, optional `validation`). [cfg#type] |

## 1. Fan-out and fan-in

```yaml
workflows:
  ci:
    jobs:
      - build
      - integration-test:
          requires: [build]
      - e2e:
          requires: [build]
      - package:
          requires: [integration-test, e2e]
```

- Fan out from a producer whose output the others need, or from nothing (roots). Fan in with one `requires` list. [wf#fan]
- A producer feeding a consumer through a workspace is a data contract: the edge is mandatory (data-flow.md).

## 2. Coordinators collapse N×M requires

```yaml
jobs:
  tests-done:
    type: no-op

workflows:
  ci:
    jobs:
      - build
      - unit-test:
          requires: [build]
      - integration-test:
          requires: [build]
      - e2e:
          requires: [build]
      - tests-done:
          requires: [unit-test, integration-test, e2e]
      - package:
          requires: [tests-done]
      - publish-report:
          requires: [tests-done]
```

- Without `tests-done`, `package` and `publish-report` would each list the three tests: 6 edges instead of 3 + 2. [cook#coordinator]
- A coordinator is a barrier: every job behind it waits for all of its requirements. A job that needs only one of them requires that one, or it waits for the slowest. (heuristic)
- It passes only success. A failed producer stops everything behind it. If all producers are filtered out, it and everything behind it do not run. [cfg#requires]
- It adds no workspace isolation: a job behind it still attaches the layers of every upstream job.
- Name it after the stage it closes (`tests-done`).

## 3. The gate: one required status check

```yaml
jobs:
  smarter-gate:
    type: no-op

workflows:
  ci:
    jobs:
      - lint
      - unit-test
      - security-scan
      - build:
          requires: [lint, unit-test]
      - smarter-gate:
          requires: [lint, unit-test, security-scan, build]
```

- One gate per workflow, `type: no-op`, named `smarter-gate`. It succeeds only if everything it requires succeeds, so branch protection needs one check instead of one per job. [cook#gate]
- It requires every check job, directly or through coordinators that require them. Do not list only the leaves: if a leaf is filtered out, a check that is no longer listed stops holding the gate. (heuristic)
- It does not require the approval, the deploy, cleanup or notify jobs: it would wait for a person, or for an outcome that does not mean "the checks passed". (`docs/spec/v0.1.md`, step 6)
- It has no filters, and at least one requirement that always runs (the mandatory scan). A gate whose requirements are all filtered out does not run, and no check reports. [cfg#requires]
- Which check to require in GitHub:
  - GitHub Checks reports one check per workflow, named by the workflow (a GitHub App project adds the first 8 characters of the pipeline definition id). Give the workflow a name no other pipeline of the project uses. [status#checks]
  - Commit statuses report one status per job. A no-op job shows as `ci/circleci: <workflow_name>/<job_name>`, a regular job as `ci/circleci: <job_name>`. [status#names]
  - Branch protection is set in GitHub, not in config: report the exact name to the person who sets it.

## 4. Fail fast

```yaml
workflows:
  ci:
    jobs:
      - lint
      - unit-test
      - e2e:
          requires: [lint, unit-test]
```

- Cheap, quick checks (lint, format, type check, unit tests, the scan) are roots and run together. Move the quickest jobs to the start. [wf#examples] [support#workflow-review]
- Make an expensive job wait for a cheap one when the cheap job is short next to it and fails often enough to matter. The edge costs the cheap job's duration each time it passes. (heuristic)
- An edge is free when the expensive job already waits for something longer, such as a build: a job that finishes earlier adds no wait (§5). (heuristic)
- Do not chain checks for tidiness. Every needless edge lengthens the critical path.
- A setup job at the start can run preflight checks and fill a workspace for the jobs behind it. [wf#examples]

## 5. Critical path

```
finish(job)   = duration(job) + max(finish(r) for r in requires(job)), or 0 without requires
workflow time = max(finish(job));  critical path = the chain that reaches it
```

| Job | Minutes | Requires | finish |
|---|---|---|---|
| lint | 1 | | 1 |
| unit-test | 4 | | 4 |
| build | 6 | | 6 |
| integration-test | 8 | build | 14 |
| e2e | 12 | build, lint, unit-test | 18 |
| package | 3 | integration-test, e2e | 21 |

The workflow takes 21 minutes: build, e2e, package. lint and unit-test are off the path, so their edges to e2e are free. `integration-test` is slow but off the path too.

- Get durations from the CircleCI MCP (tools.md §3): `started_at` and `ended_at` of each job in a green default-branch run. Use the median of several runs: this repository's own job times varied by about 25% from run to run (`docs/validation.md`). Treat them as run times: a job's wait for resources may not be in them (unverified).
- To shorten the path, in this order (heuristic): drop edges that have no reason (§4); split or parallelize the longest job; move jobs that no check needs (packaging, reports, docs) behind the gate; make parallel jobs finish at similar times, because the workflow lasts as long as its last job. [support#workflow-review]

## 6. Deploy after every check, behind an approval

```yaml
jobs:
  smarter-gate:
    type: no-op

workflows:
  release:
    jobs:
      - build
      - smarter-gate:
          requires: [build]
      - hold-deploy:
          type: approval
          requires: [smarter-gate]
          filters:
            branches:
              only: main
      - deploy:
          context: deploy
          requires: [hold-deploy]
          filters:
            branches:
              only: main
```

- Place the approval after every check (it requires the gate) and directly before the first job that must not run unapproved. A person approves a verified build. [wf#approval]
- Every job after the approval must require it. The deploy requires it directly. [wf#approval]
- Same filters on the approval and the deploy (requires-and-statuses.md §3).
- The restricted context goes on the deploy only, with `filters.branches.only` in map form. Anyone with push access can approve; the context restriction decides who can deploy. A deploy approved by someone outside the context's group ends `unauthorized`. [ctx#approve]
- One approval per protected environment: a second environment gets its own approval after the first deploy.
- A held workflow is not terminal; CircleCI cancels it after 90 days. [wf#states]

## 7. Cleanup with `terminal`, notify with `failed`

```yaml
workflows:
  release:
    jobs:
      - build
      - deploy:
          requires: [build]
      - cleanup:
          requires:
            - deploy: terminal
      - notify-failure:
          requires:
            - deploy: failed
```

- `terminal` runs the job whatever happened to `deploy`; `failed` only when it failed. [cfg#requires] [wf#ignore]
- Give both the filters of `deploy`. Keep them out of the gate.
- To tidy up after a failed step inside one job, use a step `when: always` or `on_fail`, or `teardown` (conditions.md §5). No extra job is needed for that.

## 8. serial-group for deploys, job-groups for reusable sets

```yaml
workflows:
  release:
    jobs:
      - build
      - hold-deploy:
          type: approval
          requires: [build]
      - deploy:
          serial-group: << pipeline.project.slug >>/deploy
          requires: [hold-deploy]
```

- Use `serial-group` for jobs that touch shared state: deploys, migrations, shared test environments. The same string is one queue across the projects and pipelines of the organization. [cfg#serial-group] [serial#groups]
- The key has at most 512 characters, is not blank, and uses letters, digits, `.`, `-`, `_` and `/`. Pipeline values are allowed. Put the environment in it (`.../deploy-prod`). [cfg#serial-group]
- A job waits at most five hours, then CircleCI cancels it. A job from an older pipeline is skipped when a newer pipeline's job is waiting or running, so an older pipeline does not deploy over a newer one. [cfg#serial-group]
- Jobs behind the serial job run only if it succeeded. The unlock runs whatever the outcome. [serial#expansion]
- Put the approval before the serial job, so a person's delay does not hold the lock.

A job group names a set of jobs that repeats, or that must be serialized as one unit:

```yaml
job-groups:
  release-set:
    jobs:
      - deploy
      - smoke-test:
          requires: [deploy]

workflows:
  release:
    jobs:
      - build
      - hold-deploy:
          type: approval
          requires: [build]
      - release-set:
          serial-group: << pipeline.project.slug >>/release
          requires: [hold-deploy]
      - cleanup:
          requires:
            - release-set: [success, not_run]
```

- With `serial-group` on the invocation, no other holder of that key can start until the whole group is done. [serial#job-groups]
- The restrictions on groups (no `context`, `matrix`, nesting, `parameters`) are in requires-and-statuses.md §5.

## 9. Automatic reruns for known-flaky jobs

```yaml
workflows:
  ci:
    max_auto_reruns: 2
    jobs:
      - build
      - e2e:
          requires: [build]
```

```yaml
jobs:
  e2e:
    docker:
      - image: cimg/base:2026.11
    steps:
      - checkout
      - run:
          name: Run the end-to-end suite
          command: ./scripts/e2e.sh
          max_auto_reruns: 2
          auto_rerun_delay: 30s
```

- Use it only where flakiness is known: `list_flaky_tests` lists tests that failed and then passed in the same pipeline. A rerun costs credits and hides the flake. Prefer fixing the test, and say so in your report. (heuristic)
- Workflow level: `max_auto_reruns` is 1 to 5. It reruns the failed jobs, and the jobs they blocked, of the original workflow. It does not rerun cancelled workflows, manual reruns or pipelines older than 90 days. [rerun#wf] [rerun#limits]
- Step level: `run` steps only, in the long form with `command:`. `max_auto_reruns` is 1 to 5. `auto_rerun_delay` ("10s", "2m", at most 10 minutes) needs `max_auto_reruns`, exists on steps only (the compiler rejects it on a workflow, compiler-checked), and cannot appear inside `teardown`. [cfg#step-reruns] [rerun#step]
- With both, the step reruns first, then the workflow. [rerun#wf]
- The workflow key is yours. A step key changes a job definition: add it only when you own the job.

## 10. Contexts

```yaml
workflows:
  release:
    jobs:
      - build:
          context: build-tools
      - deploy:
          context:
            - build-tools
            - deploy
          requires: [build]
```

- `context` is a name or a list. With several, later contexts override earlier ones for the same variable. A config may use at most 100 contexts. [ctx#combine] [cfg#context]
- Keep the contexts the catalog gives each job and add none it does not need: a context hands every variable in it to the job. (heuristic)
- The platform checks restrictions when the workflow runs, and every kind must pass: security groups (any one group), project restrictions (any one project) and expression restrictions (every expression). A failed check ends the job `unauthorized`. Only organization admins set them. [ctx#how]
- The guardrail bundle adds layout rules: a restricted context only on its listed jobs and branches, and only behind an approval (`policies/guardrails.rego`). It does not replace the platform's restrictions: ask an admin to set them on the context. [ctx#restrict]
- Do not compute a context name with `<< >>`: the bundle cannot check it against the restricted list (`policies/guardrails.rego`).
- A job-group invocation cannot take `context` (the compiler rejects it): see requires-and-statuses.md §5.

[cfg#type]: https://circleci.com/docs/reference/configuration-reference/#type
[cfg#context]: https://circleci.com/docs/reference/configuration-reference/#context
[cfg#requires]: https://circleci.com/docs/reference/configuration-reference/#requires
[cfg#serial-group]: https://circleci.com/docs/reference/configuration-reference/#serial-group
[cfg#step-reruns]: https://circleci.com/docs/reference/configuration-reference/#automatic-step-reruns
[wf#approval]: https://circleci.com/docs/guides/orchestrate/workflows/#holding-a-workflow-for-a-manual-approval
[wf#fan]: https://circleci.com/docs/guides/orchestrate/workflows/#fan-outfan-in-workflow
[wf#examples]: https://circleci.com/docs/guides/orchestrate/workflows/#workflows-configuration-examples
[wf#ignore]: https://circleci.com/docs/guides/orchestrate/workflows/#ignore-the-outcome-of-an-upstream-job
[wf#states]: https://circleci.com/docs/guides/orchestrate/workflows/#states
[cook#coordinator]: https://circleci.com/docs/guides/orchestrate/orchestration-cookbook/#use-no-op-jobs-to-create-a-cleaner-workflow-graph
[cook#gate]: https://circleci.com/docs/guides/orchestrate/orchestration-cookbook/#simplify-github-required-status-checks-with-no-op-jobs
[ctx#approve]: https://circleci.com/docs/guides/security/contexts/#approve-jobs-that-use-restricted-contexts
[ctx#combine]: https://circleci.com/docs/guides/security/contexts/#combine-contexts
[ctx#how]: https://circleci.com/docs/guides/security/contexts/#how-restrictions-combine
[ctx#restrict]: https://circleci.com/docs/guides/security/contexts/#restrict-a-context
[serial#groups]: https://circleci.com/docs/guides/orchestrate/controlling-serial-execution-across-your-organization/#how-serial-groups-work
[serial#job-groups]: https://circleci.com/docs/guides/orchestrate/controlling-serial-execution-across-your-organization/#job-groups
[serial#expansion]: https://circleci.com/docs/guides/orchestrate/controlling-serial-execution-across-your-organization/#how-job-group-expansion-works
[rerun#wf]: https://circleci.com/docs/guides/orchestrate/automatic-reruns/#automatic-workflow-reruns
[rerun#step]: https://circleci.com/docs/guides/orchestrate/automatic-reruns/#automatic-step-reruns
[rerun#limits]: https://circleci.com/docs/guides/orchestrate/automatic-reruns/#limitations
[status#checks]: https://circleci.com/docs/guides/integration/status-updates/#github-checks
[status#names]: https://circleci.com/docs/guides/integration/status-updates/#commit-status-update-naming
[support#workflow-review]: https://support.circleci.com/en/articles/15090869-review-each-workflow-for-improvement-opportunities
