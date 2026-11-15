# Smarter Workflows: run only the CircleCI jobs that a change can affect

A planner for CircleCI setup workflows. For each change it runs only the jobs that change can reach, and it checks its plan against your guardrails (config policies) before it continues the pipeline.

[![CircleCI](https://dl.circleci.com/status-badge/img/gh/CircleCI-Labs/smarter-workflows/tree/main.svg?style=shield)](https://dl.circleci.com/status-badge/redirect/gh/CircleCI-Labs/smarter-workflows/tree/main)
[![Release](https://img.shields.io/github/v/release/CircleCI-Labs/smarter-workflows?include_prereleases&label=release)](https://github.com/CircleCI-Labs/smarter-workflows/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](./LICENSE)
[![CircleCI Community](https://img.shields.io/badge/community-CircleCI%20Discuss-343434.svg)](https://discuss.circleci.com/)

> [!NOTE]
> **CircleCI Labs experiment, pre-release (0.x).** Not an official CircleCI product, not supported by CircleCI support, and no SLA. [Support](#support) says where to go.

## Why use it

- **Learns from your default branch.** Every full run there traces the repo files each job reads, and builds an *impact map*. A branch uses the map: a job whose inputs the change does not touch is skipped.
- **Skips a job only when proven.** A job runs unless the map shows that the change cannot reach it. Mandatory jobs always run, and anything unusual means a full run.
- **A gate that always reports.** Every planned workflow ends in a `smarter-gate` job, a no-op job that costs no credits. Even when everything else is skipped, the workflow still runs and its GitHub check still reports, so a skipped job never leaves a pull request waiting.
- **Guardrails first.** Every plan is checked locally against the same Rego bundle that you can enforce org-wide. The planner repairs what it can (a missing security scan, for example) and falls back to a full run otherwise.
- **Native to CircleCI.** A setup workflow, the continuation API, config policies and orbs. The catalog is your own `continue.yml`, with no new schema. One Go binary, which also works as `circleci smarter`.
- **Tools for agents.** `circleci-smarter catalog` prints the jobs of a workflow as JSON, and `circleci-smarter layout check` checks how they wait for each other, so that an agent can compose a workflow and know it is sound ([composer skill](skills/circleci-workflow-composer/SKILL.md)).

## How it works

1. **The default branch teaches.** Every push there gets a full run. The planner adds two steps to each job: one that resets file access times after `checkout`, and one that records which tracked files the job read and which directories it listed. A last job, `smarter-impact-map`, merges the traces into the impact map and saves it to the project cache.
2. **A branch gets a plan.** The plan job restores the map of the merge-base (or the newest one), and compares the changed files with what each job read. It keeps the jobs the change can reach, the mandatory jobs, and the jobs they need.
3. **The guardrails check the plan.** The planner evaluates the Rego bundle in `policies/` on the config it generated. It repairs a violation it can fix, such as a missing mandatory job, and otherwise runs everything.
4. **The pipeline continues** with the pruned workflow and the `smarter-gate` job.

Start in **shadow mode**: plan and report, but run everything. Switch to **select mode** when the reports convince you. [How it works](docs/how-it-works.md) has the details, and [When it runs everything](docs/how-it-works.md#when-it-runs-everything) lists every case that gets a full run.

## Who it is for

Teams with a CircleCI pipeline of many jobs, such as a monorepo with several services, where most changes touch only part of it. Setup is one `continue.yml` and one setup job. It sits between a hand-written path filter, whose globs drift from what the jobs really read, and test selection, which works inside one job.

Use something else when:

- You want to pick the tests inside a job: use Smarter Testing or the tool your job already uses. Smarter Workflows skips whole jobs, and the two compose ([decision 1](docs/adr/0001-select-jobs-not-tests.md)).
- Your jobs do not run on Linux docker or machine executors: the planner cannot trace them, so they always run.
- You need rules learned from history, or a model that decides: neither is in v0.1 ([Limitations](docs/limitations.md)).

## Quickstart

You need a CircleCI project with setup workflows turned on (Project Settings, Advanced).

1. Move your pipeline (jobs, workflows, orbs, executors) to `.circleci/continue.yml`. That file is the catalog the planner picks from, and nothing else in it changes.
1. **Once the orb is published**, replace `.circleci/config.yml` with the setup job:

   ```yaml
   version: 2.1
   setup: true
   orbs:
     smarter: cci-labs/smarter-workflows@0.1
   workflows:
     plan:
       jobs:
         - smarter/plan:
             config: .circleci/continue.yml
             workflow: ci
             mode: shadow
             mandatory: security-scan
   ```

   `workflow` is the workflow of `continue.yml` to plan, and `mandatory` lists the jobs that run on every change.
1. Push to the default branch. The first full run traces every job and saves the first impact map.
1. Push a branch, and read `smarter-plan/plan.md` in the artifacts of the plan job. Shadow mode still runs everything.
1. Change `mode` to `select` when the reports convince you.

Until the orb is published, copy the setup job of this repository's [`.circleci/config.yml`](.circleci/config.yml), which builds the planner from source. The [quickstart](docs/quickstart.md) has every step, the branch protection check, and the optional guardrails.

## Results on this repository

This repository plans its own pipeline. On 2026-11-14, with 8 jobs, a docs-only branch ran 1 job (the mandatory security scan), and a change to one Go package ran 5. Job time for the docs-only case dropped from 280 s to 46 s. The pipeline has since gained a docs site build job, which reads the docs. [How it works](docs/how-it-works.md#results-on-this-repository-2026-11-14) has the table.

## Documentation

Read the docs at [circleci-labs.github.io/smarter-workflows](https://circleci-labs.github.io/smarter-workflows/). They follow `main`.

| You want to | Read |
|---|---|
| Set it up | [Quickstart](docs/quickstart.md) |
| Understand how it decides | [How it works](docs/how-it-works.md), [Glossary](GLOSSARY.md) |
| Judge the risk | [Limitations](docs/limitations.md), [Validation](docs/validation.md) |
| Try it on an example | [Demo monorepo](examples/demo/README.md), [end-to-end scenarios](scripts/e2e/run-scenarios.sh) |
| Compose workflows with an agent | [Composer skill](skills/circleci-workflow-composer/SKILL.md), [Compose eval](examples/compose-eval/README.md) |
| Look up a command or a file format | [Spec v0.1](docs/spec/v0.1.md) |
| See why it works this way | [Decisions](docs/adr/) |

## What is tested

The project is pre-release (0.x). [Validation](docs/validation.md) lists every layer, its numbers and every gap. If a page claims more than this table shows, please open an issue.

| What | Status | How it is checked |
|---|---|---|
| Planner, config, diff, impact map and report | Tested | Unit and golden tests: 12 example scenarios with golden plans and configs, and a fuzz target |
| 27 unusual config shapes (approvals mid-graph, two workflows, matrices, orb jobs, anchors, renames and more) | Tested | Every emitted config must keep the job graph intact, pass the guardrails, and pass `circleci config validate`. Nine bugs were found and fixed first |
| Guardrail bundle | Tested | 53 Rego tests, and Go fixture tests through CircleCI's policy engine |
| Trace scripts | Tested | 43 checks on ext4, tmpfs and the `cimg/base` image, and the access time reset on CircleCI's docker and machine executors |
| End to end: the demo monorepo (13 jobs) and this repository (8 jobs) | Tested | Real branches and real pipelines, with an exact comparison of the jobs that ran: 13 of 13 scenarios, three times |
| Composing a workflow with an agent | One run | Three cases, graded by a script with no model involved. One model run passed all three |
| Shadow mode on a busy real repository | Not validated | No real hit and miss rates yet |
| The approval and deploy path on the default branch, beyond the hold | Not validated | Not exercised |
| Tampering with the cached impact map | Not validated | A forged map could skip jobs. Mandatory jobs and guardrails do not depend on the map |

Known limits:

- One workflow is planned. The other workflows of `continue.yml` pass through.
- Matrix jobs, job groups, and jobs that are not traced always run.
- The impact map lives in the project cache, and any branch can write a cache entry.

[Limitations](docs/limitations.md) lists every limit, and says why each one is safe.

## Support

This is a CircleCI Labs project: a project that field engineers at CircleCI maintain. It is **not** an official CircleCI product, and CircleCI support does not cover it. Field engineers handle issues on a best-effort basis, without an SLA.

- Open a [GitHub issue](https://github.com/CircleCI-Labs/smarter-workflows/issues) for a bug or a feature request. Read the [docs](https://circleci-labs.github.io/smarter-workflows/) first, and include the `plan.md` and `plan.json` of the plan job ([Contributing](CONTRIBUTING.md#reporting-a-bug)).
- Ask the community on [CircleCI Discuss](https://discuss.circleci.com/).
- Problems with the CircleCI platform go to [CircleCI support](https://support.circleci.com/hc/en-us).
- To report a security vulnerability, read [SECURITY.md](SECURITY.md).

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) for how to report a bug, propose a change and run the checks. The docs follow the [CircleCI docs style](https://circleci.com/docs/contributors/docs-style/style-and-voice/).

## License

MIT. Refer to [LICENSE](./LICENSE).
