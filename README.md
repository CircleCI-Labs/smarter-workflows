# Smarter Workflows: run only the CircleCI jobs that a change can affect

A planner for CircleCI setup workflows. For each change it runs only the jobs that change can reach, and it checks its plan against your guardrails (config policies) before it continues the pipeline.

[![CircleCI](https://dl.circleci.com/status-badge/img/circleci/98KhFjn8YyZY9qoBGmzCBs/AEZsEwRynPFf5UojjW6akf/tree/main.svg?style=shield)](https://dl.circleci.com/status-badge/redirect/circleci/98KhFjn8YyZY9qoBGmzCBs/AEZsEwRynPFf5UojjW6akf/tree/main)
[![Release](https://img.shields.io/github/v/release/CircleCI-Labs/smarter-workflows?include_prereleases&label=release)](https://github.com/CircleCI-Labs/smarter-workflows/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](./LICENSE)
[![CircleCI Community](https://img.shields.io/badge/community-CircleCI%20Discuss-343434.svg)](https://discuss.circleci.com/)

> [!NOTE]
> **CircleCI Labs experiment, pre-release (0.x).** Not an official CircleCI product, not supported by CircleCI support, and no SLA. [Support](#support) says where to go.

## Why use it

- **No job edits.** v0.3 builds the impact map with `circleci-smarter analyze` (static analysis, optional AI, and corrections from history). A weekly `learn` job refreshes a cached learned layer; every `plan` merges that layer at the branch tip.
- **Skips a job only when proven.** Static analysis must understand every step, and AI-only paths need confidence, a matching job hash, and valid inputs (ADR 8). Otherwise the job runs. Mandatory jobs always run; anything unusual means a full run.
- **A gate that always reports.** Every planned workflow ends in a `smarter-gate` job, a no-op job that costs no credits. Even when everything else is skipped, the workflow still runs and its GitHub check still reports, so a skipped job never leaves a pull request waiting.
- **Guardrails first.** Every plan is checked locally against the same Rego bundle that you can enforce org-wide. The planner repairs what it can (a missing security scan, for example) and falls back to a full run otherwise.
- **Native to CircleCI.** A setup workflow, the continuation API, config policies and orbs. The catalog is your own `continue.yml`, with no new schema. One Go binary, which also works as `circleci smarter`.
- **Tools for agents.** `circleci-smarter catalog` prints the jobs of a workflow as JSON, and `circleci-smarter layout check` checks how they wait for each other, so that an agent can compose a workflow and know it is sound ([composer skill](skills/circleci-workflow-composer/SKILL.md)).

## Adoption (three steps)

1. **Trial locally.** Run `circleci-smarter learn` with a read-only CircleCI token (or a saved history file) and read the report. Fix incomplete jobs until projected recall looks acceptable.
2. **Shadow in CI.** Add the orb `plan` job on every pipeline (`mode: shadow`) and a weekly `learn` job on a schedule trigger ([orb example](orb/src/examples/shadow-learn-schedule.yml)).
3. **Select when convinced.** Switch `plan` to `mode: select` when shadow runs and the learn report agree.

## How it decides

Each step must resolve to paths the planner understands at `HEAD`. A mapped directory covers everything under it; `.` covers the whole repository (so `gofmt -l .` makes `go-lint` run on every change). Opaque or unknown steps make the job run. The learned layer adds AI inputs and history corrections but never removes static proof. Plan-time `--ai-review` may **add** jobs only. See [How it works](docs/how-it-works.md), [Learn](docs/learn.md), and [Analyze](docs/analyze.md).

## Who it is for

Teams with a CircleCI pipeline of many jobs, such as a monorepo with several services, where most changes touch only part of it. Setup is one `continue.yml` and one setup job. It sits between a hand-written path filter, whose globs drift from what the jobs really read, and test selection, which works inside one job.

Use something else when:

- You want to pick the tests inside a job: use Smarter Testing or the tool your job already uses. Smarter Workflows skips whole jobs, and the two compose ([decision 1](docs/adr/0001-select-jobs-not-tests.md)).
- You cannot run analysis in the setup job: without a map, the planner runs every job.
- You need perfect coverage on day one: the first pipelines are static-only until `learn` runs ([Limitations](docs/limitations.md)).

## Quickstart

You need a CircleCI project with setup workflows turned on (Project Settings, Advanced).

1. Move your pipeline to `.circleci/continue.yml` (the catalog; unchanged otherwise).
1. Add the orb setup from [shadow-learn-schedule.yml](orb/src/examples/shadow-learn-schedule.yml): `plan` on each push, `learn` when `pipeline.trigger.type == schedule`, with a context for `CIRCLECI_TOKEN` (and optional `ANTHROPIC_API_KEY`).
1. Create a schedule trigger in project settings (GitHub App: **Triggers → Schedule**; OAuth: scheduled pipelines).
1. Read `smarter-plan/plan.md` and `smarter-learn/learn.md` artifacts; switch `mode` to `select` when they agree.

Until the orb is published, copy [`.circleci/config.yml`](.circleci/config.yml). Details: [quickstart](docs/quickstart.md).

## Results on this repository

This repository plans its own pipeline (8 jobs). Measured on real CircleCI in 2026-11-15 (three runs per scenario, medians): a **docs-only** change ran `docs-site-build` and the mandatory `security-scan` — **45 s** of job time versus **537 s** for a full run (~**92%** less). A change confined to one Go package (`internal/report`) ran six jobs and spent **423 s** versus **537 s** full (~**21%** less). On the [demo monorepo](examples/demo/README.md), a docs-only change median was **22 s** versus **154 s** full (~**86%** less).

Replaying **79** commits of this repo's history against today's impact map in select mode: **100%** recall on failures (5/5 caught, 0 misses) and **6.9%** historical job-time savings — most commits touch Go or `.circleci/`, which many jobs read. [Benchmarks](docs/benchmarks.md) has the full tables; [Validation](docs/validation.md) describes the end-to-end runs.

## Documentation

Read the docs at [circleci-labs.github.io/smarter-workflows](https://circleci-labs.github.io/smarter-workflows/). They follow `main`.

| You want to | Read |
|---|---|
| Set it up | [Quickstart](docs/quickstart.md) |
| Understand how it decides | [How it works](docs/how-it-works.md), [Glossary](GLOSSARY.md) |
| Judge the risk | [Limitations](docs/limitations.md), [Validation](docs/validation.md), [Threat model](docs/threat-model.md) |
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
| End to end: the demo monorepo (13 jobs) and this repository (8 jobs) | Tested | **21** scenarios, **3** runs each on real pipelines; **57/63** job-set checks passed on the first batch; two scenario definitions were fixed and verified on a rerun ([Validation](docs/validation.md)) |
| Composing a workflow with an agent | One run | Three cases, graded by a script with no model involved. One model run passed all three |
| Shadow mode on a busy real repository | Not validated | No real hit and miss rates yet |
| The approval and deploy path on the default branch, beyond the hold | Not validated | Not exercised |
| Tampering with the cached impact map | Not validated | A forged map could skip jobs. Mandatory jobs and guardrails do not depend on the map |

Known limits:

- One workflow is planned. The other workflows of `continue.yml` pass through.
- Matrix jobs, job groups, and jobs with no map entry always run.
- The learned layer lives in the project cache; tampering is possible until you trust branch protection and contexts ([Threat model](docs/threat-model.md)).

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
