---
name: circleci-workflow-composer
description: Assemble a set of selected CircleCI jobs into a correct, efficient workflow layout (order, requires, approvals, filters, contexts, conditions, gate) for Smarter Workflows. Load it when you must write or repair a `workflows:` block from a job inventory (`circleci-smarter catalog`), or when `circleci-smarter layout check`, the guardrail bundle or `circleci config validate` reports a layout problem.
---

# CircleCI workflow composer

You wire jobs that already exist into workflows. You do not write jobs, steps or orbs. Load the CircleCI MCP skill `write-config` first (`get_skill` with `skill: "write-config"`, then `reference: "references/orbs.md"`): it covers orbs, functions and `validate_config`. This skill adds only the workflow layer.

Tags such as [cfg#requires] name the source of a rule; the URLs are at the end. `(heuristic)` marks advice that is not a documented rule.

## Inputs

- The job inventory, `circleci-smarter catalog --config .circleci/continue.yml`: the only source of job names, kinds, parameters, workspace use, contexts and filters.
- The guardrail settings, the constants at the top of `policies/guardrails.rego`: mandatory jobs, restricted contexts, resource classes.
- Optional: job durations from green default-branch runs (references/tools.md §3).

## Workflow

1. **Read the catalog.** For each selected job note its kind (build, approval, no-op, release), what it persists or attaches, its contexts and filters, matrix or parameters, orb or local. Copy names exactly. Mark the mandatory jobs and every job that uses a restricted context. If a job you need is not listed, stop and report it.
2. **Decide the stages.** Everything runs in parallel unless an edge has a reason: a data contract (a consumer attaches what a producer persists), fail fast (cheap before expensive), policy (a restricted context needs an approval), order of effect (deploy after every check), shared state (serial-group). Usual order: cheap checks and the mandatory scan as roots, build, expensive checks, gate, approval, deploy, cleanup and notify.
3. **Wire `requires`.** Fan out from producers. Collapse N×M with a no-op coordinator. Require a matrix by its alias and a job group by its name. Use a status other than success only on cleanup, notify and rollback jobs.
4. **Add conditions.** Filters, workflow `when`, step `when`. Give an approval and the jobs behind it the same filters.
5. **Add the gate.** One `type: no-op` job named `smarter-gate`, with no filters, that requires every check job and nothing behind the approval. It is the single required status check.
6. **Check** the whole file, in this order: `circleci config validate FILE`, `circleci-smarter layout check ...`, `circleci-smarter policy check ...` (commands: references/tools.md §4 and §5).
7. **Repair.** Read each finding's code and message (references/tools.md §4 maps a finding to a fix), fix the cause in the layout, and run all three checks again. If a finding survives three repairs, stop and report it. Never pass a check by weakening the layout: do not drop a mandatory job, an approval, a context restriction or a branch filter, and do not edit `policies/`.
8. **Report** the layout in a few lines, the result of each check, and what you could not verify. Leave everything outside the workflow you compose (other workflows, jobs, orbs, comments) as it was.

## Hard rules

Layout
- [ ] Add no job except approvals, no-op coordinators and the gate. Use catalog names verbatim. One entry per job, or distinct `name:` values. [cfg#name]
- [ ] Write only keys that the references or the docs list. A misspelled key on a local job fails to compile; on an orb job it can be ignored without a word (`write-config` skill).
- [ ] Every `requires` item is an entry of the same workflow: its `name:`, a matrix alias or a job-group name. No cycle. The compiler rejects the rest. [cfg#requires] [cfg#matrix-requires]
- [ ] A required job that does not run is ignored, and a job whose requirements are all filtered out does not run either. Work this out for every filtered job and every approval. [cfg#requires]
- [ ] Statuses are `success`, `failed`, `canceled`, `unauthorized`, `not_run` and `terminal`. A job that requires `X: failed` or `X: terminal` gets the filters of X: the docs do not say what happens when X is filtered out. (heuristic) [cfg#requires]

Approvals, contexts, mandatory jobs
- [ ] An approval is `type: approval` on a workflow entry, with a unique name and no steps. Every job after it requires it. Place it after all checks, directly before the first job that must not run unapproved. [wf#approval]
- [ ] An approval and every job behind it carry the same filters. Filter only the approval and the job runs unapproved; filter only the job and the workflow waits on hold. [cfg#requires] [wf#approval]
- [ ] Keep the contexts the catalog gives a job and add none it does not need; with several, later ones win. [ctx#combine]
- [ ] A restricted context goes only on the jobs the bundle lists, behind an approval, with `filters.branches.only` in map form. Keep such jobs out of job groups. [ctx#approve] (`policies/guardrails.rego`)
- [ ] Mandatory jobs are plain entries (not in a job group), unfiltered, with no `requires` unless they need another job's output, so no earlier failure keeps them from running. [cfg#requires] (`policies/guardrails.rego`)

Gate
- [ ] The gate is `type: no-op` with no filters. It requires every check job, directly or through coordinators, and nothing behind the approval, nor any cleanup or notify job. At least one of its requirements always runs. [cook#gate] [cfg#requires]

Data
- [ ] A job that attaches a workspace is downstream of every job it needs data from. Concurrent producers persist disjoint paths. A cache never carries this run's output between jobs. [ws#overview] [cache#race]

Conditions
- [ ] Conditions read pipeline values and parameters, never environment variables. Never put `<< >>` inside a bare expression. A `when` step takes a logic statement, not an expression. [cfg#logic] [cook#when]

Speed and cost
- [ ] Put the quickest jobs first, and add an edge only for a reason: a needless edge lengthens the critical path. [wf#examples] [support#workflow-review]
- [ ] Use `serial-group` for deploys and other shared state, and `max_auto_reruns` only for jobs known to be flaky. [cfg#serial-group] [rerun#wf]

Planner v0.1
- [ ] Prefer what the planner can evaluate: map branch and tag filters, and `requires` by success. A matrix, an expression filter, a non-literal workflow `when` or another status makes it run more, or leaves a job out of the gate. (`docs/spec/v0.1.md` steps 1, 5, 6; `docs/limitations.md`)

## Which reference to read

| When you | Read |
|---|---|
| wire `requires`, use a status, filter a job or an approval, use a matrix or a job group | references/requires-and-statuses.md |
| shape the graph: fan-out, coordinator, gate, fail fast, critical path, approval, deploy, serial-group, reruns | references/shaping-patterns.md |
| write `when`, `unless`, filters or step conditions | references/conditions.md |
| fix an order because of workspaces, caches or artifacts | references/data-flow.md |
| call a tool or read a finding | references/tools.md |
| want a complete, valid layout to copy | examples/ |

Examples, each validated with `circleci config validate`: `01-small.yml` (parallel checks, one build, a gate); `02-medium-fanout-workspaces.yml` (workspaces, fan-out, a matrix by alias); `03-deploy-gated.yml` (approval, restricted context, serial-group, cleanup, notify, gate).

## Tools

| Need | Use |
|---|---|
| Job inventory | `circleci-smarter catalog --config F [--map M] [--workflow W]` |
| Layout findings | `circleci-smarter layout check --config F [--catalog C] [--policy-bundle D] [--mandatory LIST] [--restricted-contexts LIST]` |
| Guardrails | `circleci-smarter policy check --config F --bundle policies --format json` |
| Compile | `circleci config validate F`, or MCP `validate_config` (pass `org` for private or URL orbs) |
| What CircleCI documents | docs MCP `search_circle_ci_knowledge_sources`: one complete, generic sentence |
| Job durations | MCP `list_runs`, `list_run_workflows`, `list_workflow_jobs` (`started_at`, `ended_at`) |
| Flaky jobs | MCP `list_flaky_tests` |
| Orb job parameters | MCP `get_orb_source`, before you use an orb job |

`catalog` and `layout check` are built alongside this skill: if `--help` differs from references/tools.md, `--help` wins. Never print environment variables or secrets.

[cfg#requires]: https://circleci.com/docs/reference/configuration-reference/#requires
[cfg#name]: https://circleci.com/docs/reference/configuration-reference/#name
[cfg#matrix-requires]: https://circleci.com/docs/reference/configuration-reference/#dependencies-and-matrix-jobs
[cfg#logic]: https://circleci.com/docs/reference/configuration-reference/#logic-statements
[cfg#serial-group]: https://circleci.com/docs/reference/configuration-reference/#serial-group
[wf#approval]: https://circleci.com/docs/guides/orchestrate/workflows/#holding-a-workflow-for-a-manual-approval
[wf#examples]: https://circleci.com/docs/guides/orchestrate/workflows/#workflows-configuration-examples
[cook#gate]: https://circleci.com/docs/guides/orchestrate/orchestration-cookbook/#simplify-github-required-status-checks-with-no-op-jobs
[cook#when]: https://circleci.com/docs/guides/orchestrate/orchestration-cookbook/#combine-pipeline-parameters-with-event-filters-in-workflow-conditions
[ctx#approve]: https://circleci.com/docs/guides/security/contexts/#approve-jobs-that-use-restricted-contexts
[ctx#combine]: https://circleci.com/docs/guides/security/contexts/#combine-contexts
[ws#overview]: https://circleci.com/docs/guides/orchestrate/workspaces/#overview
[cache#race]: https://circleci.com/docs/guides/optimize/caching/#caching-race-conditions
[rerun#wf]: https://circleci.com/docs/guides/orchestrate/automatic-reruns/#automatic-workflow-reruns
[support#workflow-review]: https://support.circleci.com/en/articles/15090869-review-each-workflow-for-improvement-opportunities
