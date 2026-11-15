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

# api_get prints JSON on success; warns on stderr and returns non-zero on failure.
api_get() {
  _err="$(mktemp)"
  if out="$(circleci api "$1" 2>"$_err")"; then
    rm -f "$_err"
    printf '%s\n' "$out"
    return 0
  fi
  printf 'fetch-history: warning: %s: %s\n' "$1" "$(tr '\n' ' ' <"$_err")" >&2
  rm -f "$_err"
  return 1
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

# jobs_for_pipeline collects every workflow named WORKFLOW in the pipeline (reruns)
# and merges job outcomes for flaky detection.
jobs_for_pipeline() {
  pipe_id="$1"
  wf_page=""
  if ! wf_page="$(api_get "api/v2/pipeline/${pipe_id}/workflow")"; then
    return 1
  fi
  wfs="$(printf '%s\n' "$wf_page" | workflows_named "$WORKFLOW")"
  count="$(printf '%s\n' "$wfs" | jq 'length')"
  if [ "$count" -eq 0 ]; then
    return 1
  fi
  bundle="[]"
  i=0
  while [ "$i" -lt "$count" ]; do
    wf="$(printf '%s\n' "$wfs" | jq -c ".[$i]")"
    wf_id="$(printf '%s\n' "$wf" | jq -r '.id')"
    wf_at="$(printf '%s\n' "$wf" | jq -r '.created_at')"
    jobs_page=""
    if ! jobs_page="$(api_get "api/v2/workflow/${wf_id}/job")"; then
      printf 'fetch-history: warning: workflow %s jobs skipped\n' "$wf_id" >&2
      i=$((i + 1))
      continue
    fi
    slice="$(printf '%s\n' "$jobs_page" | jq -c --arg at "$wf_at" '{created_at: $at, items: .items}')"
    bundle="$(printf '%s\n' "$bundle" | jq -c --argjson s "$slice" '. + [$s]')"
    i=$((i + 1))
  done
  if [ "$(printf '%s\n' "$bundle" | jq 'length')" -eq 0 ]; then
    return 1
  fi
  printf '%s\n' "$bundle" | merge_workflow_reruns
}

record_one() {
  pipeline_json="$1"
  jobs_json="$2"
  sha="$(printf '%s\n' "$pipeline_json" | commit_from_pipeline)"
  branch="$(printf '%s\n' "$pipeline_json" | branch_from_pipeline)"
  base="$(merge_base "$sha")"
  changed="$(changed_json "$base" "$sha")"
  jq -n \
    --arg commit "$sha" \
    --arg base "$base" \
    --arg branch "$branch" \
    --argjson changed "$changed" \
    --argjson jobs "$jobs_json" \
    '{commit: $commit, base: $base, branch: $branch, changed: $changed, jobs: $jobs}'
}

# --- main: live API (not run in CI without a token) ---

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT INT HUP
printf '%s\n' '[]' | jq '.' >"$tmp"
next_token=""
seen=0

while [ "$seen" -lt "$LIMIT" ]; do
  page=""
  if ! page="$(api_get "api/v2/project/${PROJECT_SLUG}/pipeline${next_token:+?page-token=${next_token}}")"; then
    printf 'fetch-history: warning: pipeline list failed; stopping\n' >&2
    break
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
    jobs=""
    if ! jobs="$(jobs_for_pipeline "$pipe_id")"; then
      printf 'fetch-history: warning: pipeline %s skipped\n' "$pipe_id" >&2
      continue
    fi
    entry="$(record_one "$pipeline" "$jobs")"
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