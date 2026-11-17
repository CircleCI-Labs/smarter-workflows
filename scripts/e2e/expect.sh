#!/usr/bin/env bash
# expect.sh: recompute scenario expectations with the local circleci-smarter CLI.
#
# For each row of scenarios-self.tsv and scenarios-demo.tsv, checks out a scratch
# branch in a temporary worktree, applies the change, runs plan in select mode,
# and prints the job set that would run (plus full/blocked outcomes). Use this
# output to update the TSV expectations after changing analyze or the pipelines.
#
#   scripts/e2e/expect.sh [--write]
#
# --write replaces the expectation column in place (creates *.bak first).
# Requires a built circleci-smarter on PATH or builds to /tmp/smarter/bin.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(cd "$here/../.." && pwd)
write=0
if [ "${1:-}" = --write ]; then
  write=1
fi

bin=${PLANNER_BIN:-}
if [ -z "$bin" ]; then
  mkdir -p /tmp/smarter/bin
  if ! CGO_ENABLED=0 go build -trimpath -o /tmp/smarter/bin/circleci-smarter "$root/cmd/circleci-smarter"; then
    echo "expect: failed to build circleci-smarter" >&2
    exit 1
  fi
  bin=/tmp/smarter/bin/circleci-smarter
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/e2e-expect.XXXXXX")
cleanup() {
  local wtpath=""
  while read -r wtpath _; do
    case "$wtpath" in
      "$work"/*) git worktree remove -f "$wtpath" 2>/dev/null || true ;;
    esac
  done < <(git worktree list)
  rm -rf "$work"
}
trap cleanup EXIT

# plan_* flags must stay in sync with the setup jobs in .circleci/config.yml and
# examples/demo/.circleci/config.yml (select mode, mandatory, policy-bundle, etc.).
# We pass --branch scenario/… so workflow filters match real e2e pipelines.

plan_self() {
  local wt=$1 base=$2 branch=$3 head=$4
  (
    cd "$wt"
    mkdir -p /tmp/smarter/plan
    set -- "$bin" plan \
      --config .circleci/continue.yml \
      --workflow ci \
      --default-branch main \
      --mode select \
      --mandatory security-scan \
      --policy-bundle policies \
      --base "$base" \
      --head "$head" \
      --branch "$branch" \
      --report-dir /tmp/smarter/plan \
      --out /tmp/smarter/continue.yml
    if [ -s /tmp/smarter/learned.json ]; then
      set -- "$@" --learned /tmp/smarter/learned.json
    fi
    "$@" 1>&2
  )
}

plan_demo() {
  local wt=$1 base=$2 branch=$3 head=$4
  (
    cd "$wt"
    mkdir -p /tmp/smarter/plan
    set -- "$bin" plan \
      --config examples/demo/.circleci/continue.yml \
      --workflow demo \
      --default-branch main \
      --mode select \
      --mandatory security-scan \
      --policy-bundle policies \
      --full-run-paths 'examples/demo/.circleci/**' \
      --base "$base" \
      --head "$head" \
      --branch "$branch" \
      --report-dir /tmp/smarter/plan \
      --out /tmp/smarter/continue.yml
    if [ -s /tmp/smarter/learned.json ]; then
      set -- "$@" --learned /tmp/smarter/learned.json
    fi
    "$@" 1>&2
  )
}

expect_of() {
  local tgt=$1 name=$2 expect=$3 change=$4 message=$5
  local wt=$work/wt-$tgt-$name
  local branch=scenario/$name-$$
  git worktree add -q --no-track -b "$branch" "$wt" "$base_sha"
  (
    cd "$wt"
    bash -euo pipefail -c "$change" >/dev/null
    git add -A 2>/dev/null
    if [ -z "$(git status --porcelain)" ]; then
      echo "expect: $tgt/$name: change produced no diff" >&2
      exit 1
    fi
    git -c user.name="$(git config user.name)" -c user.email="$(git config user.email)" \
      commit -q -m "${message:-test: expect $name}"
  )

  local head branch
  head=$(git -C "$wt" rev-parse HEAD)
  branch=$(git -C "$wt" branch --show-current)
  local code=0 plan=/tmp/smarter/plan/plan.json
  case $tgt in
    self) plan_self "$wt" "$base_sha" "$branch" "$head" || code=$? ;;
    demo) plan_demo "$wt" "$base_sha" "$branch" "$head" || code=$? ;;
    *) echo "expect: unknown target $tgt" >&2; exit 2 ;;
  esac

  if [ "$code" = 3 ]; then
    echo blocked
    return 0
  fi
  if [ "$code" != 0 ]; then
    echo "expect: $tgt/$name: plan exited $code" >&2
    exit 1
  fi
  if [ ! -s "$plan" ]; then
    echo "expect: $tgt/$name: no plan.json" >&2
    exit 1
  fi
  local full
  full=$(jq -r '.full // false' "$plan")
  if [ "$full" = true ]; then
    local reason
    reason=$(jq -r '.full_reason // "full run"' "$plan")
    echo "full:$reason"
    return 0
  fi

  local jobs
  jobs=$(
    jq -r '
      [.jobs[] | select(.run and .name != "smarter-gate") | .name]
      | sort | join(" ")
    ' "$plan"
  )
  if [ "$tgt" = demo ]; then
    # CircleCI names matrix instances worker-test-<param>, not worker-test.
    jobs=$(printf '%s' "$jobs" | sed 's/\<worker-test\>/worker-test-3.12.16 worker-test-3.13.18/g')
  fi
  printf '%s' "$jobs"
}

process_file() {
  local file=$1 tgt=$2
  local out
  out="$work/$(basename "$file").out"
  : > "$out"
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      ''|'#'*)
        printf '%s\n' "$line" >> "$out"
        continue
        ;;
    esac
    local name expect change message
    IFS=$'\t' read -r name expect change message <<< "$line" || true
    got=$(expect_of "$tgt" "$name" "$expect" "$change" "$message")
    printf '%s\t%s\n' "$name" "$got"
    printf '%s\t%s\t%s\t%s\n' "$name" "$got" "$change" "$message" >> "$out"
  done < "$file"
  if [ "$write" -eq 1 ]; then
    cp "$file" "$file.bak"
    mv "$out" "$file"
    echo "expect: wrote $file (backup at $file.bak)"
  fi
}

cd "$root"
base_ref=origin/main
git fetch -q origin main 2>/dev/null || true
if ! base_sha=$(git rev-parse --verify "$base_ref^{commit}"); then
  echo "expect: cannot resolve $base_ref" >&2
  exit 2
fi
echo "expect: scenarios from $base_ref ($base_sha)"
process_file "$here/scenarios-self.tsv" self
process_file "$here/scenarios-demo.tsv" demo