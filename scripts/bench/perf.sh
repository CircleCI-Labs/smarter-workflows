#!/bin/sh
# perf.sh: planner config benchmarks; prints Markdown.
set -u

here=$(cd "$(dirname "$0")" && pwd) || exit 2
root=$(cd "$here/../.." && pwd) || exit 2
cd "$root" || exit 2

goversion=$(go version | sed 's/go version //')
cpu_model=$(awk -F: '/model name/ {gsub(/^ +/,"",$2); print $2; exit}' /proc/cpuinfo)
cpu_cores=$(nproc 2>/dev/null || getconf _NPROCESSORS_ONLN)

echo "## Smarter Workflows performance ($(date -u +%Y-%m-%dT%H:%MZ))"
echo
echo "| Field | Value |"
echo "|---|---|"
echo "| CPU | $cpu_model |"
echo "| Cores | $cpu_cores |"
echo "| Go | $goversion |"
echo

bench_tmp=$(mktemp)
trap 'rm -f "$bench_tmp"' EXIT

echo "Running Go benchmarks (this may take several minutes)..."
if ! go test -run='^$' \
  -bench='BenchmarkPlan/|BenchmarkConfig|BenchmarkImpactMapParse' \
  -benchmem -count=1 \
  ./internal/planner/... ./internal/config/... \
  2>"$bench_tmp.err" | tee "$bench_tmp"; then
  echo "go test benchmarks failed; see $bench_tmp.err" >&2
fi

echo
echo "Measuring plan p95 at 2000 jobs / 10k-file map..."
p95_tmp=$(mktemp)
if go test -run='^$' -bench=BenchmarkPlanLatency2000 -benchtime=30x -count=1 ./internal/planner/ 2>"$p95_tmp.err" | tee "$p95_tmp"; then
  p95_out=$(grep BenchmarkPlanLatency2000 "$p95_tmp" | tail -1)
else
  p95_out=$(tail -1 "$p95_tmp.err")
fi
plan_p95=$(echo "$p95_out" | awk '{for (i = 1; i <= NF; i++) if ($i == "p95_sec") print $(i - 1)}')
plan_p95=${plan_p95:-unknown}
rm -f "$p95_tmp" "$p95_tmp.err"

echo
echo "### Planner (ns/op, selected)"
echo
echo "| Benchmark | ns/op | B/op | allocs/op |"
echo "|---|---:|---:|---:|"
grep '^BenchmarkPlan/' "$bench_tmp" | while read -r line; do
  name=$(echo "$line" | awk '{print $1}')
  ns=$(echo "$line" | awk '{print $3}')
  b=$(echo "$line" | awk '{print $5}')
  a=$(echo "$line" | awk '{print $7}')
  echo "| $name | $ns | $b | $a |"
done

echo
echo "### Config parse / emit (ns/op)"
echo
echo "| Benchmark | ns/op | B/op | allocs/op |"
echo "|---|---:|---:|---:|"
grep -E '^BenchmarkConfig' "$bench_tmp" | while read -r line; do
  name=$(echo "$line" | awk '{print $1}')
  ns=$(echo "$line" | awk '{print $3}')
  b=$(echo "$line" | awk '{print $5}')
  a=$(echo "$line" | awk '{print $7}')
  echo "| $name | $ns | $b | $a |"
done

echo
echo "### Targets"
echo
plan_ok=no
case $plan_p95 in
  '' | unknown) plan_note="could not parse p95 ($p95_out)" ;;
  *)
    if awk -v p="$plan_p95" 'BEGIN { exit !(p < 2.0) }'; then
      plan_ok=yes
      plan_note="plan p95 ${plan_p95}s < 2s (2000 jobs, fanout, 10k-file map)"
    else
      plan_note="**MISS**: plan p95 ${plan_p95}s (target < 2s)"
    fi
    ;;
esac
echo "- $plan_note"

if [ "$plan_ok" = no ] && [ "$plan_p95" != unknown ] && [ -n "$plan_p95" ]; then
  prof=$(mktemp -d "${TMPDIR:-/tmp}/smarter-plan-prof.XXXXXX")
  echo
  echo "Profiling plan hotspot (2000 jobs)..."
  go test -run='^$' -bench=BenchmarkPlanLatency2000 -benchtime=5x -cpuprofile="$prof/cpu.prof" ./internal/planner/ >/dev/null 2>&1 || true
  if [ -f "$prof/cpu.prof" ]; then
    echo
    echo "#### Plan CPU profile (top 15)"
    echo '```'
    go tool pprof -top -nodecount=15 "$prof/cpu.prof" 2>/dev/null || true
    echo '```'
    echo "Proposed fix (do not apply in this worktree): review top frames in impact-map file matching and config pruning for large maps."
  fi
  rm -rf "$prof"
fi

echo
echo "Rerun: \`sh scripts/bench/perf.sh\` from the repository root."