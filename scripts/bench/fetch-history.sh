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
# Needs CIRCLE_TOKEN (or CIRCLECI_TOKEN). Delegates to circleci-smarter history fetch.

set -eu

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

if [ -z "${CIRCLE_TOKEN:-}" ] && [ -n "${CIRCLECI_TOKEN:-}" ]; then
  export CIRCLE_TOKEN="$CIRCLECI_TOKEN"
fi

smarter_bin() {
  if command -v circleci-smarter >/dev/null 2>&1; then
    command -v circleci-smarter
    return
  fi
  if [ -x /tmp/smarter/bin/circleci-smarter ]; then
    echo /tmp/smarter/bin/circleci-smarter
    return
  fi
  if [ -x /tmp/workspace/circleci-smarter ]; then
    echo /tmp/workspace/circleci-smarter
    return
  fi
  echo "fetch-history: circleci-smarter is required" >&2
  exit 1
}

BIN="$(smarter_bin)"
exec "$BIN" history fetch \
  --project-slug "$PROJECT_SLUG" \
  --workflow "$WORKFLOW" \
  --limit "$LIMIT" \
  --repo "$CLONE_DIR" \
  --out "$OUT"