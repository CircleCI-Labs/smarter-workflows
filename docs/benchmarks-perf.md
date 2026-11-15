---
page-title: Performance benchmarks
page-audience: admin
page-description: Planner and trace-script timing on synthetic workloads, how the numbers were taken, and how to rerun them locally.
---

# Performance benchmarks

This page will hold the results of the planner performance benchmarks: plan time and allocations on large synthetic configs and impact maps, and the runtime of the trace scripts on large tracked-file trees.

The benchmark harness lives under `scripts/bench/`. Until the first run lands on `main`, there is no table here. When it does, each row will name the workload, the median or p95 time, and how to reproduce it on your machine (CPU model, Go version, and the exact command).