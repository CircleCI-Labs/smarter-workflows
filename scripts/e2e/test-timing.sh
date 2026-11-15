#!/usr/bin/env bash
# test-timing.sh: unit checks for scripts/e2e/timing.sh against API fixtures.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/e2e/timing.sh
source "$here/timing.sh"
# shellcheck source=scripts/e2e/summary.sh
source "$here/summary.sh"

samples=$here/testdata/api-samples
fail() {
  echo "test-timing: $*" >&2
  exit 1
}

want_eq() {
  local got=$1 want=$2 msg=$3
  [ "$got" = "$want" ] || fail "$msg: got $got, want $want"
}

jobs_json=$(< "$samples/jobs.json")
want_eq "$(sum_job_run_seconds "$jobs_json")" "174" "sum of demo planned jobs from jobs.json"

wfs_json=$(< "$samples/workflows.json")
want_eq "$(workflow_wall_seconds "demo-plan" "$wfs_json")" "52" "demo-plan workflow wall from workflows.json"
want_eq "$(workflow_wall_seconds "demo" "$wfs_json")" "$E2E_TIMING_NA" "running workflow without stopped_at"

mix_json=$(< "$here/testdata/timestamps-mix.json")
want_eq "$(sum_job_run_seconds "$mix_json")" "3" "mixed .fracZ and plain Z timestamps"

want_eq "$(sum_job_run_seconds '{"items":[]}')" "$E2E_TIMING_NA" "empty job list"

timing=$(mktemp "${TMPDIR:-/tmp}/e2e-timing-test.XXXXXX")
trap 'rm -f "$timing"' EXIT
csv=
: > "$timing"
print_savings_summary || fail "print_savings_summary without --csv must not fail under set -e"
csv=/tmp/e2e-timing-test-csv.$$.csv
print_savings_summary | grep -qx 'no timing data' || fail "expected no timing data with empty timing file"

echo "test-timing: ok"