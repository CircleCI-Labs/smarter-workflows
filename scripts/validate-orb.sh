#!/bin/sh
# validate-orb.sh [ORB]: validates an orb without a CircleCI API token.
#
# `circleci orb validate` needs a token, and CI has none. `circleci config
# validate` does not: it compiles a config, and an orb defined inside the config
# needs no lookup. So this script writes a throwaway config that defines ORB
# inline as `smarter`, adds a workflow that runs its plan job, and validates
# that config. A broken orb makes the config invalid.
#
#   scripts/validate-orb.sh [orb/orb.yml]
#
# The plan job has no required parameters. The workflow passes the two that this
# repo's own setup uses, so the executor name and an enum are checked as well.
set -eu

orb=${1:-orb/orb.yml}
if [ ! -f "$orb" ]; then
  echo "validate-orb: no such file: $orb" >&2
  exit 2
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/validate-orb.XXXXXX")
trap 'rm -rf "$work"' EXIT
trap 'exit 1' HUP INT TERM

config="$work/config.yml"
{
  printf 'version: 2.1\n'
  printf 'orbs:\n'
  printf '  smarter:\n'
  sed 's/^/    /' "$orb"
  printf 'workflows:\n'
  printf '  validate-orb:\n'
  printf '    jobs:\n'
  printf '      - smarter/plan:\n'
  printf '          executor: smarter/go\n'
  printf '          source: build\n'
} > "$config"

circleci config validate "$config"
echo "validate-orb: $orb is valid"
