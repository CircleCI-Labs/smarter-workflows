#!/bin/sh
# Inline-orb integration check: install (source build) and plan on the monorepo.
#
# Validates a config that inlines the packed orb and runs smarter/plan with
# source: build. If a planner binary exists at PLANNER_BIN or was built, runs
# plan far enough to write continue.yml; otherwise validates only.
#
#   scripts/test-orb-integration.sh [orb/src]
#   PLANNER_BIN=/path/to/circleci-smarter
set -eu

orb=${1:-orb/src}
root=$(cd "$(dirname "$0")/.." && pwd)

work=$(mktemp -d "${TMPDIR:-/tmp}/orb-integration.XXXXXX")
trap 'rm -rf "$work"' EXIT
trap 'exit 1' HUP INT TERM

packed=$work/orb.yml
circleci orb pack "$orb" > "$packed"

config=$work/config.yml
{
  printf 'version: 2.1\n'
  printf 'orbs:\n'
  printf '  smarter:\n'
  sed 's/^/    /' "$packed"
  printf 'workflows:\n'
  printf '  orb-integration:\n'
  printf '    jobs:\n'
  printf '      - smarter/plan:\n'
  printf '          executor: smarter/go\n'
  printf '          source: build\n'
  printf '          config: examples/monorepo/continue.yml\n'
  printf '          workflow: ci\n'
  printf '          mode: shadow\n'
  printf '          force-full: true\n'
} > "$config"

circleci config validate "$config"
echo "test-orb-integration: config is valid"

bin=${PLANNER_BIN:-}
if [ -z "$bin" ]; then
  mkdir -p /tmp/smarter/bin
  if CGO_ENABLED=0 go build -trimpath -o /tmp/smarter/bin/circleci-smarter "$root/cmd/circleci-smarter" 2>/dev/null; then
    bin=/tmp/smarter/bin/circleci-smarter
  fi
fi

if [ -n "$bin" ] && [ -x "$bin" ]; then
  cd "$root"
  mkdir -p /tmp/smarter/plan
  "$bin" plan \
    --config examples/monorepo/continue.yml \
    --workflow ci \
    --mode shadow \
    --force-full \
    --out /tmp/smarter/continue.yml \
    --report-dir /tmp/smarter/plan > /dev/null
  test -s /tmp/smarter/continue.yml
  test -s /tmp/smarter/plan/plan.json
  circleci config validate /tmp/smarter/continue.yml
  echo "test-orb-integration: plan on examples/monorepo succeeded"
else
  echo "test-orb-integration: skipped plan run (no planner binary)"
fi

echo "test-orb-integration: ok"