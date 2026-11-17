---
page-title: Learn
page-audience: admin
page-description: Refresh the learned layer weekly and measure recall before adoption.
---

# Learn

`circleci-smarter learn` is the scheduled job that refreshes the learned layer (AI mappings and history corrections) and estimates how select mode would behave on recent pipelines.

## Command

```bash
circleci-smarter learn --project-slug gh/org/repo --workflow ci --limit 50 \
  --repo . --config .circleci/continue.yml \
  --learned /tmp/smarter/learned.json --learned-out /tmp/smarter/learned-new.json \
  --report learn.md --report-json learn.json
```

Use `--history FILE` to skip the API and replay a saved history file (no token). `--history-out` stores what was fetched. `--parameters FILE` sets pipeline parameter values for every replayed commit.

AI modes match `analyze` (`auto`, `on`, `off`, `dry-run`). `--min-recall` exits with code 3 when projected recall is below the threshold.

## Report

The Markdown and JSON reports open with what was measured: how many commits were replayed, recall and projected job-time savings, misses before and after this run's corrections, corrections added, incomplete jobs (with reasons), and AI usage (calls, tokens, estimated cost).

## Local trial

Before enabling select mode in CI, run learn against your project slug or a recorded history file. Fix incomplete jobs and review misses until recall and savings meet your bar. Costs are dominated by the optional AI classifier; history fetch uses the CircleCI API only.

## Costs

- **History fetch:** CircleCI API calls only; no job minutes on your project.
- **AI:** Billed per Anthropic usage when `--ai` is not `off`; see the report's token and cost lines (Haiku tier, same pricing note as analyze).

See also [Analyze](analyze.md) for the learned layer format and [Backtest](benchmarks-backtest.md) for how recall is computed.