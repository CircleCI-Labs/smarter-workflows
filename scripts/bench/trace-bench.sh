#!/bin/sh
# trace-bench.sh: time reset-atime.sh and collect-trace.sh on synthetic git repos.
# Used by scripts/bench/perf.sh; not a gate on its own.
set -u

here=$(cd "$(dirname "$0")" && pwd) || exit 2
root=$(dirname "$(dirname "$here")")
reset_sh=$root/internal/tracescript/reset-atime.sh
collect_sh=$root/internal/tracescript/collect-trace.sh

if [ ! -f "$reset_sh" ] || [ ! -f "$collect_sh" ]; then
  echo "missing trace scripts" >&2
  exit 2
fi

base=${SMARTER_BENCH_TMP:-${TMPDIR:-/tmp}}
work=$(mktemp -d "$base/smarter-trace-bench.XXXXXX") || exit 2
trap 'rm -rf "$work"' EXIT

# make_repo N OUTDIR: git repo with N tracked files (empty, under f/).
make_repo() {
  n=$1
  out=$2
  mkdir -p "$out/f"
  (
    cd "$out" || exit 1
    export GIT_CONFIG_NOSYSTEM=1
    export GIT_CONFIG_GLOBAL=/dev/null
    export DEVBOX_GIT_ALLOW_ANY_AUTHOR=1
    git init -q
    git config user.email bench@example.invalid
    git config user.name "Bench"
    git config commit.gpgsign false
    git config gc.auto 0
    git config maintenance.auto false
    awk -v n="$n" 'BEGIN { for (i = 0; i < n; i++) printf "f/%05d.dat\n", i }' | xargs touch
    git add f
    git commit -q -m "bench"
  )
}

# seconds_one CMD...: wall time in seconds (three decimal places).
seconds_one() {
  start=$(date +%s.%N)
  # shellcheck disable=SC2068
  "$@" > /dev/null 2>&1
  end=$(date +%s.%N)
  awk -v s="$start" -v e="$end" 'BEGIN { printf "%.3f", e - s }'
}

printf 'trace_bench\tfiles\treset_sec\tcollect_sec\n'
for n in 1000 10000 100000; do
  repo=$work/repo-$n
  make_repo "$n" "$repo"
  (
    cd "$repo" || exit 1
    export CIRCLE_WORKING_DIRECTORY="$repo"
    export SMARTER_TRACE_DIR="$repo/traces"
    mkdir -p "$SMARTER_TRACE_DIR"
    reset_sec=$(seconds_one sh "$reset_sh")
    # Read a handful of files so the trace is not empty (negligible vs find).
    i=0
    while [ "$i" -lt 10 ]; do
      cat "f/$(printf '%05d' "$i").dat" > /dev/null
      i=$((i + 1))
    done
    export CIRCLE_JOB=bench-job
    export CIRCLE_NODE_INDEX=0
    collect_sec=$(seconds_one sh "$collect_sh")
    printf 'trace_bench\t%d\t%s\t%s\n' "$n" "$reset_sec" "$collect_sec"
  )
done