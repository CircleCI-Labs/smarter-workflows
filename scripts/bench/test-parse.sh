#!/bin/sh
# Test fetch-history parsing against checked-in API fixtures (no network).
set -eu

dir=$(cd "$(dirname "$0")" && pwd)
pipeline="$(cat "$dir/testdata/pipeline.json")"
jobs_page="$(cat "$dir/testdata/workflow_jobs.json")"

sha="$(printf '%s' "$pipeline" | jq -r '.vcs.revision // .trigger_parameters["git.checkout_sha"] // empty')"
branch="$(printf '%s' "$pipeline" | jq -r '.vcs.branch // .trigger_parameters["git.branch"] // ""')"

if [ "$sha" != "abc123deadbeef" ]; then
  echo "test-parse: unexpected commit sha: $sha" >&2
  exit 1
fi
if [ "$branch" != "feature/docs" ]; then
  echo "test-parse: unexpected branch: $branch" >&2
  exit 1
fi

got="$(printf '%s' "$jobs_page" | jq '
  [.items[] | select(.type == "build") | {
    name,
    status: (if .status == "success" then "success" elif .status == "failed" then "failed" else .status end),
    duration_s: ((.duration // 0) / 1000),
    rerun_success: (.rerun_success // false)
  }]
  | map(
      if .status == "failed" and (.rerun_success // false) then . + {flaky: true} | del(.rerun_success)
      else . | del(.rerun_success)
      end
    )
')"
want="$(cat "$dir/testdata/jobs_expected.json")"
if ! printf '%s\n%s' "$got" "$want" | jq -e -s '.[0] == .[1]' >/dev/null; then
  echo "test-parse: job mapping mismatch" >&2
  echo "got: $got" >&2
  echo "want: $want" >&2
  exit 1
fi

echo "test-parse: ok"