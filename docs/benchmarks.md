---
page-title: Benchmarks
page-audience: admin
page-description: Overview of end-to-end CI savings, historical backtest recall, and local performance targets for the planner and trace scripts.
---

# Benchmarks

Measured results for v0.1: real CircleCI pipelines for job selection, a replay of this repository's history against today's map, and synthetic workloads for planner and trace timing.

## Method

**End to end** — `scripts/e2e/run-scenarios.sh` pushes a one-commit branch per scenario, waits for the setup workflow and the continued workflow, and compares the jobs that ran with the expected set. Each scenario runs three times on the same base commit; medians use the per-target full-run scenarios as the baseline. Details: [Validation](validation.md#end-to-end-scenarios).

**Backtest** — `circleci-smarter backtest` replays a JSON history file in select mode with a fixed impact map. Details: [Backtest](benchmarks-backtest.md).

**Performance** — Go `testing.B` benchmarks and `scripts/bench/perf.sh` on generated configs and temporary git repos. Details: [Performance benchmarks](benchmarks-perf.md).

## End-to-end savings

**Job seconds** are the sum of each job's reported run time in the continued workflow (including `smarter-gate`). They are a stand-in for compute time, not credits: resource class affects credits, and queue time is excluded. On the [demo monorepo](../examples/demo/README.md), the two `worker-test` matrix jobs always run in v0.1, so they appear in every row below.

Median job seconds per scenario (three runs each, base commit on main, 2026-11-15). Full-run medians: **154 s** (demo, 13 jobs) and **537 s** (this repository, 8 jobs).

| Scenario | Target | Median job s | Full median s | Saved |
| --- | --- | ---: | ---: | ---: |
| docs-only | demo | 22 | 154 | 86% |
| infra-only | demo | 32 | 154 | 79% |
| web-only | demo | 60 | 154 | 61% |
| worker-only | demo | 57 | 154 | 63% |
| e2e-script | demo | 59 | 154 | 62% |
| web-deleted | demo | 65 | 154 | 58% |
| web-lockfile | demo | 68 | 154 | 56% |
| shared-lib | demo | 107 | 154 | 31% |
| api-rename | demo | 113 | 154 | 27% |
| api-only | demo | 138 | 154 | 10% |
| added-file-api | demo | 130 | 154 | 16% |
| docs-and-api | demo | 131 | 154 | 15% |
| circleci-change | demo | 146 | 154 | 5% |
| smarter-full-message | demo | 162 | 154 | −5% |
| guardrail-blocked | demo | — | 154 | — |
| self-docs-only | self | 45 | 537 | 92% |
| self-internal-report | self | 423 | 537 | 21% |
| self-added-file-internal-report | self | 402 | 537 | 25% |
| self-smarter-full-message | self | 537 | 537 | 0% |
| self-policies | self | — | 537 | — |
| self-orb | self | — | 537 | — |

Rows marked — did not get a comparable median in the first batch (guardrail stops before jobs run; two self scenarios had incorrect expectations or a broken change step). After fixing those scenarios, [Validation](validation.md#end-to-end-scenarios) describes the rerun.

## Backtest recall

On **79** commits from this repository's `ci` workflow, replayed against today's impact map in select mode:

| Metric | Value |
| --- | --- |
| Failures (non-flaky) | 5 |
| Caught (would still run) | 5 |
| Misses (would skip) | 0 |
| **Recall** | **100%** |
| Historical job time | 13 737 s |
| Would have skipped | 949 s |
| **Job-time savings** | **6.9%** |

Most commits here touch Go or `.circleci/`, paths that many jobs read, so savings stay modest. A backtest of the demo monorepo is not informative: that project's history was already produced under select mode. Caveats and limits: [Backtest](benchmarks-backtest.md).

## Performance

Synthetic workloads on one machine (2026-11-15). All documented targets passed:

| Target | Result |
| --- | --- |
| Plan p95 &lt; 2 s at 2000 jobs, 10k-file map | **0.166 s** |
| `collect-trace` &lt; 10 s at 100k tracked files | **0.355 s** |
| v2 map &lt; 20 MiB and decode &lt; 0.5 s at 2000×10k | **233 KiB**, **0.25 s** decode |

Full tables and how to rerun: [Performance benchmarks](benchmarks-perf.md).