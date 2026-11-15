#!/bin/sh
# Test fetch-history parsing against checked-in API fixtures (no network).
set -eu

dir=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=libparse.sh
# shellcheck disable=SC1091
. "$dir/libparse.sh"

fail() {
  echo "test-parse: $1" >&2
  exit 1
}

# Legacy simplified fixtures (flat trigger_parameters keys).
pipeline="$(cat "$dir/testdata/pipeline.json")"
jobs_page="$(cat "$dir/testdata/workflow_jobs.json")"
sha="$(printf '%s\n' "$pipeline" | commit_from_pipeline)"
branch="$(printf '%s\n' "$pipeline" | branch_from_pipeline)"
if [ "$sha" != "abc123deadbeef" ] || [ "$branch" != "feature/docs" ]; then
  fail "legacy pipeline selectors (sha=$sha branch=$branch)"
fi
got="$(printf '%s\n' "$jobs_page" | jobs_from_workflow | mark_flaky_jobs)"
want="$(cat "$dir/testdata/jobs_expected.json")"
if ! printf '%s\n%s\n' "$got" "$want" | jq -e -s '.[0] == .[1]' >/dev/null; then
  fail "legacy job mapping mismatch"
fi

# Real GitHub App API shapes (multiline commit messages in JSON strings).
gha="$(cat "$dir/testdata/pipeline_github_app.json")"
sha="$(printf '%s\n' "$gha" | commit_from_pipeline)"
branch="$(printf '%s\n' "$gha" | branch_from_pipeline)"
if [ "$sha" != "b66ed439ad596cc4d801ed050a0589b732796f88" ] || [ "$branch" != "main" ]; then
  fail "github_app pipeline (sha=$sha branch=$branch)"
fi

pages="$(cat "$dir/testdata/pipelines_page.json")"
token="$(printf '%s\n' "$pages" | next_page_token_from_pipelines)"
if [ -z "$token" ]; then
  fail "pipelines next_page_token missing"
fi
if ! printf '%s\n' "$pages" | jq -e '.items | length == 1' >/dev/null; then
  fail "pipelines_page item count"
fi

# dash echo(1) expands \n inside JSON; printf must still feed valid JSON to jq.
if ! printf '%s\n' "$gha" | jq -e '.trigger_parameters.git.commit_message | contains("Merge pull request")' >/dev/null; then
  fail "printf pipeline JSON is not valid for jq"
fi

jobs_gha="$(cat "$dir/testdata/workflow_jobs_github_app.json")"
got="$(printf '%s\n' "$jobs_gha" | jobs_from_workflow | jq '[.[] | select(.name=="lint" or .name=="security-scan")]')"
want="$(cat "$dir/testdata/jobs_expected_github_app.json")"
if ! printf '%s\n%s\n' "$got" "$want" | jq -e -s '.[0] == .[1]' >/dev/null; then
  fail "github_app job durations"
fi

printf 'test-parse: ok\n'