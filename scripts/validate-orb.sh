#!/bin/sh
# validate-orb.sh [ORB]: validates an orb by inlining it in a throwaway config.
#
# `circleci config validate` needs CIRCLE_TOKEN (smarter-workflows-ci in CI).
# This script writes a config that defines ORB
# inline as `smarter`, adds a workflow that runs its plan job, and validates
# that config. A broken orb makes the config invalid.
#
#   scripts/validate-orb.sh [orb/src]
#
# ORB is the orb's source directory (orb-tools layout: @orb.yml, commands/,
# jobs/, executors/, examples/, scripts/), which is packed first with
# `circleci orb pack`, or a packed orb file. The default is orb/src.
#
# The plan job has no required parameters. The workflow passes the two that this
# repo's own setup uses, so the executor name and an enum are checked as well.
set -eu

orb=${1:-orb/src}
# The orb used to be one file, orb/orb.yml. That name still works: it means the
# source directory next to it.
if [ ! -e "$orb" ] && [ "${orb##*/}" = orb.yml ] && [ -d "${orb%orb.yml}src" ]; then
  orb=${orb%orb.yml}src
fi
if [ ! -e "$orb" ]; then
  echo "validate-orb: no such file or directory: $orb" >&2
  exit 2
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/validate-orb.XXXXXX")
trap 'rm -rf "$work"' EXIT
trap 'exit 1' HUP INT TERM

if [ -d "$orb" ]; then
  packed="$work/orb.yml"
  circleci orb pack "$orb" > "$packed"
else
  packed=$orb
fi

config="$work/config.yml"
{
  printf 'version: 2.1\n'
  printf 'orbs:\n'
  printf '  smarter:\n'
  sed 's/^/    /' "$packed"
  printf 'workflows:\n'
  printf '  validate-orb:\n'
  printf '    jobs:\n'
  printf '      - smarter/plan:\n'
  printf '          executor: smarter/go\n'
  printf '          source: build\n'
  printf '          resource_class: medium\n'
  printf '          learned-key-prefix: smarter-learned-v1\n'
  printf '          ai-review: shadow\n'
  printf '  validate-orb-learn:\n'
  printf '    jobs:\n'
  printf '      - smarter/learn:\n'
  printf '          executor: smarter/go\n'
  printf '          source: build\n'
  printf '          resource_class: medium\n'
  printf '          learned-key-prefix: smarter-learned-v1\n'
  printf '          limit: 10\n'
  printf '          project-slug: gh/CircleCI-Labs/smarter-workflows\n'
  printf '          ai: "off"\n'
  printf '          mandatory: security-scan\n'
  printf '          policy-bundle: policies\n'
} > "$config"

circleci config validate "$config"
echo "validate-orb: $orb is valid"