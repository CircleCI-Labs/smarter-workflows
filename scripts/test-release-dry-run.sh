#!/bin/sh
# Local release dry run: all platform binaries and SHA256SUMS, no sign, no upload.
#
#   scripts/test-release-dry-run.sh
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
out=$(mktemp -d "${TMPDIR:-/tmp}/smarter-release-dry.XXXXXX")
trap 'rm -rf "$out"' EXIT

export RELEASE_OUT="$out"
export RELEASE_SKIP_SIGN=1
export CIRCLE_TAG=v0.0.1
export CIRCLE_PROJECT_USERNAME=CircleCI-Labs-Standalone
export CIRCLE_PROJECT_REPONAME=smarter-workflows
export CIRCLE_REPOSITORY_URL=git@github.com:CircleCI-Labs/smarter-workflows.git

. "$root/scripts/github-slug.sh"
got=$(github_slug_from_env)
test "$got" = CircleCI-Labs/smarter-workflows

"$root/scripts/release.sh"

test -s "$out/SHA256SUMS"
lines=$(wc -l < "$out/SHA256SUMS" | tr -d ' ')
test "$lines" = 5
for osarch in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64.exe; do
  f=$out/circleci-smarter_v0.0.1_$osarch
  test -x "$f" || test -f "$f"
done
test ! -e "$out/SHA256SUMS.sigstore.json"

printf 'test-release-dry-run: ok (%s)\n' "$out"