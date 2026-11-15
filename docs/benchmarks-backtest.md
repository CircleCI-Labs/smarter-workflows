# Backtest: recall and job-time savings

The `backtest` command replays historical CI runs against **today's** impact map and reports how often select mode would still have run failing jobs (recall) and how much job time it would have skipped (savings).

## Method

For each record in a history file, the planner runs in **select mode** with the recorded `base`, `commit`, `branch`, and `changed` paths. That reuses `internal/planner` exactly as `plan --mode select` does; backtest does not duplicate selection logic.

For each job that ran in CI:

| Outcome | Meaning |
|--------|---------|
| **Caught** | The job failed and select mode would still run it. |
| **Miss** | The job failed and select mode would skip it. |
| **Flaky** | The job failed then passed on a rerun of the same commit; excluded from recall. |

**Recall** is `caught / (caught + misses)` over non-flaky failures.

**Savings** sums `duration_s` over jobs that ran historically but would be skipped, divided by total recorded job time.

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

Build a history file with [`scripts/bench/fetch-history.sh`](../scripts/bench/fetch-history.sh) (needs a CircleCI API token and a git clone). Fixture parsing is tested by [`scripts/bench/test-parse.sh`](../scripts/bench/test-parse.sh).

## Command

```sh
circleci-smarter backtest \
  --history history.json \
  --map impact-map.json \
  --config .circleci/continue.yml \
  --workflow ci \
  --mandatory security-scan \
  --format md
```

`--format json` writes the machine-readable report (used for golden tests).

## Scheduled benchmark on main

The `benchmark` workflow in `.circleci/continue.yml` runs weekly on `main` (and can be triggered manually). It uses the `smarter-workflows-ai` context for API access, restores the latest impact map cache, fetches the last 200 `ci` pipelines, runs backtest, and stores `backtest.md` and `backtest.json` as artifacts.

## Limits of replay

- **One map for all commits.** The map reflects the repo layout at its revision (usually recent `main`). Older commits that touched different paths or job names are approximate.
- **Changed files only.** History carries a file list, not full git trees. Adds/deletes may miss directory moves the live planner would see via `git ls-tree`.
- **Workflow drift.** Jobs removed from the config or renamed are ignored in recall; savings count only jobs present in both history and the current workflow.
- **No guardrail replay.** Backtest does not pass `--policy-bundle`; historical guardrail repairs are not simulated.
- **Flaky detection** depends on CircleCI job rerun metadata when history is mined; hand-written fixtures set `"flaky": true` explicitly.

Use backtest to compare maps and tuning over time, not as a guarantee for every past commit.