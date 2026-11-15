#!/bin/sh
# Build a backtest history JSON from CircleCI pipeline and job APIs plus git.
#
# Usage: fetch-history.sh PROJECT_SLUG WORKFLOW LIMIT OUT.json [CLONE_DIR]
#
# PROJECT_SLUG is the CircleCI project slug (for example circleci/org/repo).
# WORKFLOW is the workflow name in the continuation config (for example ci).
# LIMIT is how many recent pipelines to consider (newest first).
# OUT.json is where the history array is written.
# CLONE_DIR is an optional local git clone; default: the current directory.
#
# Needs CIRCLE_TOKEN (the CircleCI CLI reads this). CIRCLECI_TOKEN from CI
# contexts is copied to CIRCLE_TOKEN when the latter is unset. Also needs git,
# jq, and the circleci CLI on PATH.

set -eu

dir=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=libparse.sh
# shellcheck disable=SC1091
. "$dir/libparse.sh"

if [ -z "${CIRCLE_TOKEN:-}" ] && [ -n "${CIRCLECI_TOKEN:-}" ]; then
  export CIRCLE_TOKEN="$CIRCLECI_TOKEN"
fi

usage() {
  echo "usage: fetch-history.sh PROJECT_SLUG WORKFLOW LIMIT OUT.json [CLONE_DIR]" >&2
}

if [ "$#" -lt 4 ] || [ "$#" -gt 5 ]; then
  usage
  exit 2
fi

PROJECT_SLUG="$1"
WORKFLOW="$2"
LIMIT="$3"
OUT="$4"
CLONE_DIR="${5:-.}"

if ! command -v jq >/dev/null 2>&1; then
  echo "fetch-history: jq is required" >&2
  exit 1
fi
if ! command -v circleci >/dev/null 2>&1; then
  echo "fetch-history: circleci CLI is required" >&2
  exit 1
fi

api_get() {
  circleci api "$1"
}

default_branch() {
  ref="$(git -C "$CLONE_DIR" symbolic-ref -q refs/remotes/origin/HEAD 2>/dev/null || true)"
  case "$ref" in
    */*) basename "$ref" ;;
    *) echo "main" ;;
  esac
}

merge_base() {
  commit="$1"
  def="$(default_branch)"
  git -C "$CLONE_DIR" merge-base "origin/${def}" "$commit" 2>/dev/null || true
}

# changed_json prints a JSON array of {path, status} for BASE..COMMIT.
changed_json() {
  base="$1"
  commit="$2"
  if [ -z "$base" ]; then
    printf '%s\n' '[]'
    return
  fi
  git -C "$CLONE_DIR" diff --name-status "$base" "$commit" | jq -Rs '
    split("\n")[:-1]
    | map(split("\t"))
    | map(select(length >= 2))
    | map(
        .[0] as $st
        | .[1] as $path
        | if ($st | startswith("R")) then {path: $path, status: "M"}
          elif $st == "A" or $st == "M" or $st == "D" then {path: $path, status: $st}
          else empty
          end
      )
  '
}

record_one() {
  pipeline_json="$1"
  jobs_page="$2"
  sha="$(printf '%s\n' "$pipeline_json" | commit_from_pipeline)"
  branch="$(printf '%s\n' "$pipeline_json" | branch_from_pipeline)"
  base="$(merge_base "$sha")"
  changed="$(changed_json "$base" "$sha")"
  jobs="$(printf '%s\n' "$jobs_page" | jobs_from_workflow | mark_flaky_jobs)"
  jq -n \
    --arg commit "$sha" \
    --arg base "$base" \
    --arg branch "$branch" \
    --argjson changed "$changed" \
    --argjson jobs "$jobs" \
    '{commit: $commit, base: $base, branch: $branch, changed: $changed, jobs: $jobs}'
}

# --- main: live API (not run in CI without a token) ---

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT INT HUP
printf '%s\n' '[]' | jq '.' >"$tmp"
next_token=""
seen=0

while [ "$seen" -lt "$LIMIT" ]; do
  if [ -n "$next_token" ]; then
    page="$(api_get "api/v2/project/${PROJECT_SLUG}/pipeline?page-token=${next_token}")"
  else
    page="$(api_get "api/v2/project/${PROJECT_SLUG}/pipeline")"
  fi

  n="$(printf '%s\n' "$page" | jq '.items | length')"
  if [ "$n" -eq 0 ]; then
    break
  fi

  i=0
  while [ "$i" -lt "$n" ] && [ "$seen" -lt "$LIMIT" ]; do
    pipeline="$(printf '%s\n' "$page" | jq -c ".items[$i]")"
    i=$((i + 1))
    sha="$(printf '%s\n' "$pipeline" | commit_from_pipeline)"
    if [ -z "$sha" ]; then
      continue
    fi
    pipe_id="$(printf '%s\n' "$pipeline" | jq -r '.id')"
    wf_page="$(api_get "api/v2/pipeline/${pipe_id}/workflow")"
    wf_id="$(printf '%s\n' "$wf_page" | jq -r --arg w "$WORKFLOW" '.items[] | select(.name == $w) | .id' | head -n 1)"
    if [ -z "$wf_id" ] || [ "$wf_id" = "null" ]; then
      continue
    fi
    jobs_page="$(api_get "api/v2/workflow/${wf_id}/job")"
    # Enrich failed jobs with rerun_success from the job detail API.
    jobs_page="$(printf '%s\n' "$jobs_page" | jq '
      .items |= map(
        if .status == "failed" then . + {rerun_success: false} else . end
      )
    ')"
    for jid in $(printf '%s\n' "$jobs_page" | jq -r '.items[] | select(.status=="failed") | .id'); do
      detail="$(api_get "api/v2/job/${jid}")"
      ok="$(printf '%s\n' "$detail" | job_detail_rerun_success)"
      jobs_page="$(printf '%s\n' "$jobs_page" | jq --arg id "$jid" --argjson ok "$ok" '
        .items |= map(if .id == $id then . + {rerun_success: $ok} else . end)
      ')"
    done
    entry="$(record_one "$pipeline" "$jobs_page")"
    jq --argjson e "$entry" '. + [$e]' "$tmp" >"${tmp}.new" && mv "${tmp}.new" "$tmp"
    seen=$((seen + 1))
  done

  next_token="$(printf '%s\n' "$page" | next_page_token_from_pipelines)"
  if [ -z "$next_token" ]; then
    break
  fi
done

cp "$tmp" "$OUT"
printf 'fetch-history: wrote %s commits to %s\n' "$seen" "$OUT" >&2