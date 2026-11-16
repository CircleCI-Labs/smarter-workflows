---
page-title: Backtest
page-audience: admin
page-description: Recall and job-time savings when the planner is replayed against historical CI runs, and the limits of that replay.
---

# Backtest

The `backtest` command replays historical CI runs against **today's** impact map and reports how often select mode would still have run failing jobs (recall) and how much job time it would have skipped (savings).

## Method

For each record in a history file, the planner runs in **select mode** with the recorded `base`, `commit`, `branch`, and `changed` paths. That reuses `internal/planner` exactly as `plan --mode select` does; backtest does not duplicate selection logic.

For each job that ran in CI:

| Outcome | Meaning |
|--------|---------|
| **Caught** | The job failed and select mode would still run it. |
| **Miss** | The job failed and select mode would skip it. |
| **Unmatched** | The job failed but its name is not in today's workflow (renamed or removed); excluded from recall. |
| **Flaky** | The job failed then passed on a rerun of the same commit; excluded from recall. |

**Recall** is `caught / (caught + misses)` over non-flaky failures in jobs that still exist in the config.

**Savings** sums `duration_s` over **matched** jobs that ran historically but would be skipped, divided by matched recorded job time. Unmatched job time is reported separately.

## History file format

A JSON array of commits:

```json
[
  {
    "commit": "sha",
    "base": "merge-base sha",
    "branch": "feature/x",
    "changed": [{ "path": "pkg/foo.go", "status": "M" }],
    "jobs": [
      { "name": "go-test", "status": "failed", "duration_s": 120.5 }
    ]
  }
]
```

Optional per-commit `parameters` overrides the `--parameters` file for that pipeline (same semantics as `plan --parameters`).

Build a history file with `circleci-smarter history fetch` (or the [`fetch-history.sh`](../scripts/bench/fetch-history.sh) wrapper). Set `CIRCLE_TOKEN` or `CIRCLECI_TOKEN`; optional `CIRCLECI_HOST` for server installs. The command pages pipelines newest first, resolves job outcomes (including **flaky** when a job failed then passed on a workflow rerun), and fills `base` / `changed` from a local `--repo` clone. Commits missing from the clone are skipped. Fixture parsing is tested by [`scripts/bench/test-parse.sh`](../scripts/bench/test-parse.sh) and `go test ./internal/history/...`.

```sh
circleci-smarter history fetch \
  --project-slug circleci/org/repo \
  --workflow ci \
  --limit 200 \
  --repo . \
  --out history.json
```

## Command

```sh
circleci-smarter backtest \
  --history history.json \
  --map impact-map.json.gz \
  --config .circleci/continue.yml \
  --workflow ci \
  --parameters pipeline-params.json \
  --mandatory security-scan \
  --format md
```

`--parameters` is a JSON object of pipeline parameter values, applied to every commit unless a history record carries its own `parameters` object. Use the same file you would pass to `plan --parameters` when the continuation config gates workflows or jobs on `<< pipeline.parameters.* >>`.

`--format json` writes the machine-readable report (used for golden tests).

`--strict` makes backtest warnings fatal (exit 1). Warnings print on stderr and at the top of the report:

- **Workflow disabled:** when `--parameters` turn off the workflow (`when`/`unless` false), recall is meaningless — every commit is treated as if the workflow did not run.
- **Job name drift:** when more than half of recorded job outcomes do not match any job in today's workflow (renamed jobs or wrong `--workflow`).

## Scheduled benchmark on main

The `benchmark` workflow in `.circleci/continue.yml` runs when `pipeline.trigger.type` is `schedule` (the schedule is configured on the project in CircleCI, not in this file). It uses the `smarter-workflows-ai` context so `fetch-history.sh` can call the API with `<< pipeline.project.slug >>` (GitHub App projects use the `circleci/org/project` form). It restores the latest impact map cache, fetches the last 200 `ci` pipelines, runs backtest, and stores `backtest.md` and `backtest.json` as artifacts.

## Limits of replay

- **One map for all commits.** The map reflects the repo layout at its revision (usually recent `main`). Older commits that touched different paths or job names are approximate.
- **Changed files only.** History carries a file list, not full git trees. Adds/deletes may miss directory moves the live planner would see via `git ls-tree`.
- **Workflow drift.** Failures in jobs removed from the config or renamed are **unmatched** (listed separately, not in recall). Savings count only jobs present in today's workflow.
- **Parameter gates.** Without `--parameters`, workflow `when` expressions that depend on pipeline parameters may be unknown, which forces a full run and makes savings look like 0%.
- **No guardrail replay.** Backtest does not pass `--policy-bundle`; historical guardrail repairs are not simulated.
- **Flaky detection** depends on CircleCI job rerun metadata when history is mined; hand-written fixtures set `"flaky": true` explicitly.

Use backtest to compare maps and tuning over time, not as a guarantee for every past commit.

## Measured: this repository (2026-11-15)

Replayed **79** commits from the project's `ci` workflow against the impact map from main at commit `83d7d6082`:

| Metric | Value |
| --- | --- |
| Failures (non-flaky) | 5 |
| Caught | 5 |
| Misses | 0 |
| Flaky (excluded) | 0 |
| **Recall** | **100%** |
| Historical job time | 13 737 s |
| Would have skipped | 949 s |
| **Savings** | **6.9%** |

History here is mostly Go and `.circleci/` changes, which reach most jobs, so skipped time stays small. Replaying the [demo monorepo](../examples/demo/README.md) history would not measure much: those pipelines already ran in select mode when the traces were taken.