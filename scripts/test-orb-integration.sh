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
  printf '          ai-review: "off"\n'
} > "$config"

circleci config validate "$config"
echo "test-orb-integration: plan config is valid"

learn_cfg=$work/learn-config.yml
{
  printf 'version: 2.1\n'
  printf 'orbs:\n'
  printf '  smarter:\n'
  sed 's/^/    /' "$packed"
  printf 'workflows:\n'
  printf '  orb-integration-learn:\n'
  printf '    jobs:\n'
  printf '      - smarter/learn:\n'
  printf '          executor: smarter/go\n'
  printf '          source: build\n'
  printf '          config: examples/monorepo/continue.yml\n'
  printf '          workflow: ci\n'
  printf '          limit: 5\n'
  printf '          ai: auto\n'
} > "$learn_cfg"

circleci config validate "$learn_cfg"
echo "test-orb-integration: learn config is valid"

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
    --analyze-report /tmp/smarter/plan/analyze-report.md \
    --out /tmp/smarter/continue.yml \
    --report-dir /tmp/smarter/plan > /dev/null
  test -s /tmp/smarter/continue.yml
  test -s /tmp/smarter/plan/plan.json
  test -s /tmp/smarter/plan/analyze-report.md
  circleci config validate /tmp/smarter/continue.yml
  echo "test-orb-integration: plan on examples/monorepo succeeded"

  hist="$root/internal/backtest/testdata/param-gated/history.json"
  if [ -s "$hist" ]; then
    mkdir -p /tmp/smarter/learn
    "$bin" learn \
      --repo "$root" \
      --config "$root/internal/backtest/testdata/param-gated/config.yml" \
      --workflow ci \
      --history "$hist" \
      --learned-out /tmp/smarter/learned.json \
      --report /tmp/smarter/learn/learn.md \
      --ai off > /dev/null
    test -s /tmp/smarter/learned.json
    echo "test-orb-integration: learn with fixture history succeeded"
  fi
else
  echo "test-orb-integration: skipped plan run (no planner binary)"
fi

echo "test-orb-integration: ok"