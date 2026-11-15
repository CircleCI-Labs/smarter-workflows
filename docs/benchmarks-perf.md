---
page-title: Performance benchmarks
page-audience: admin
page-description: Planner and trace-script timing on synthetic workloads, how the numbers were taken, and how to rerun them locally.
---

# Performance benchmarks

Snapshot of planner, config I/O and trace-script performance on one machine. Regenerate before comparing across releases.

## Method

**Planner (`testing.B`)** — Package `internal/planner/benchgen` builds synthetic `continue.yml` files at 10, 100, 500 and 2000 workflow jobs in three shapes (wide fan-out, deep chain, diamond), with matrix entries and approvals every 7th / 11th job. Impact maps hold 100 or 10,000 repo-relative files per job; diffs list 1, 50 or 1000 changed paths. Benchmarks call `planner.Run` in select mode with an in-memory repo (no git). `BenchmarkPlanLatency2000` reports plan wall-time p95 at 2000 jobs with a 10k-file map (fan-out, 50-file diff).

**Config (`testing.B`)** — `BenchmarkConfigParse` and `BenchmarkConfigEmit` on the same generated YAML.

**Trace scripts** — `scripts/bench/trace-bench.sh` builds temporary git repos with 1k, 10k and 100k tracked empty files under `f/`, runs `reset-atime.sh` then `collect-trace.sh`, and records wall seconds.

**Driver** — `sh scripts/bench/perf.sh` runs all of the above and prints Markdown tables.

## Targets (2026-11-15)

| Target | Result |
|--------|--------|
| Plan p95 &lt; 2 s at 2000 jobs and 10k-file maps | **0.166 s** p95 (`BenchmarkPlanLatency2000`) |
| `collect-trace` &lt; 10 s at 100k tracked files | **0.355 s** |
| v2 map &lt; 20 MiB and decode &lt; 0.5 s at 2000 jobs × 10k files | **233 KiB**, **0.25 s** decode (`TestBenchMapV2FitsTarget`) |

All targets passed on this run. Largest single-op planner cases (2000 jobs, 10k-file map) are ~0.27–0.50 s per plan in `BenchmarkPlan/` (see full table from `perf.sh`).

## Impact map decode (2000 jobs × 10k files each)

Each job reads 10k paths from a shared pool of 10k repo-relative paths (worst case for v1 path repetition).

| Format | On disk | Decode (median of 3) |
| --- | ---: | ---: |
| v1 JSON | 940 MiB | 6.31 s (`BenchmarkImpactMapParse` / `BenchmarkMapV1Parse2000x10k`) |
| v2 gzip | 233 KiB | 0.25 s (`BenchmarkMapV2Parse2000x10k`) |

v1 maps remain capped at **16 MiB** on disk (hardening); larger or malformed maps mean a full run. v2 uses separate compressed, decompressed, table and per-job index caps (ADR 7).

Reproduce map decode:

```bash
go test ./internal/impactmap -bench='BenchmarkMapV1Parse|BenchmarkMapV2Parse' -benchtime=3x -count=1
go test ./internal/impactmap -run TestBenchMapV2FitsTarget -count=1 -v
go test -run='^$' -bench=BenchmarkImpactMapParse -benchmem ./internal/planner/...
```

## Environment

| Field | Value |
|-------|-------|
| CPU | Intel(R) Xeon(R) 6975P-C |
| Cores | 16 |
| Go | go1.27.4 linux/amd64 |

## Trace scripts (seconds)

| Tracked files | reset-atime | collect-trace |
|---:|---:|---:|
| 1,000 | 1.060 | 0.032 |
| 10,000 | 1.122 | 0.063 |
| 100,000 | 1.464 | 0.355 |

## Planner highlights (2000 jobs, ns/op)

| Benchmark | ns/op | allocs/op |
|---|---:|---:|
| fanout/map100/diff1 | 117,497,799 | 730,285 |
| fanout/map10000/diff1 | 260,066,110 | 744,178 |
| fanout/map10000/diff1000 | 391,603,013 | 749,057 |
| chain/map10000/diff1000 | 503,390,971 | 863,957 |
| diamond/map10000/diff1000 | 389,229,904 | 777,106 |

## Config I/O highlights (2000 jobs, ns/op)

| Benchmark | ns/op | allocs/op |
|---|---:|---:|
| ConfigParse/fanout | 23,449,701 | 239,684 |
| ConfigEmit/fanout | 36,943,698 | 49,867 |

## How to rerun

From the repository root (expect several minutes for the full Go benchmark matrix):

```sh
sh scripts/bench/perf.sh
```

Go only:

```sh
go test -run='^$' -bench='BenchmarkPlan/|BenchmarkConfig|BenchmarkImpactMapParse' -benchmem ./internal/planner/... ./internal/config/...
```

Trace only:

```sh
sh scripts/bench/trace-bench.sh
```