#!/bin/sh
# Unit tests for scripts/github-slug.sh.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=scripts/github-slug.sh
. "$here/github-slug.sh"

pass=0
fail=0

ok() { pass=$((pass + 1)); printf 'ok - %s\n' "$1"; }
not_ok() { fail=$((fail + 1)); printf 'not ok - %s\n' "$1"; }

# slug URL WANT
assert_slug() {
  name=$1
  url=$2
  want=$3
  got=$(slug_from_clone_url "$url") || got=
  if [ "$got" = "$want" ]; then ok "$name"; else not_ok "$name (got $got, want $want)"; fi
}

assert_slug "https with .git" "https://github.com/CircleCI-Labs/smarter-workflows.git" CircleCI-Labs/smarter-workflows
assert_slug "https without .git" "https://github.com/CircleCI-Labs/smarter-workflows" CircleCI-Labs/smarter-workflows
assert_slug "ssh with .git" "git@github.com:CircleCI-Labs/smarter-workflows.git" CircleCI-Labs/smarter-workflows
assert_slug "ssh without .git" "git@github.com:CircleCI-Labs/smarter-workflows" CircleCI-Labs/smarter-workflows

# env: CIRCLE username is not the GitHub owner
unset GITHUB_REPOSITORY
export CIRCLE_PROJECT_USERNAME=CircleCI-Labs-Standalone
export CIRCLE_PROJECT_REPONAME=smarter-workflows
export CIRCLE_REPOSITORY_URL=git@github.com:CircleCI-Labs/smarter-workflows.git
got=$(github_slug_from_env)
if [ "$got" = CircleCI-Labs/smarter-workflows ]; then
  ok "slug from CIRCLE_REPOSITORY_URL when CIRCLE_PROJECT_USERNAME differs"
else
  not_ok "slug from CIRCLE_REPOSITORY_URL (got $got)"
fi

export GITHUB_REPOSITORY=CircleCI-Labs/smarter-workflows
unset CIRCLE_REPOSITORY_URL
got=$(github_slug_from_env)
if [ "$got" = CircleCI-Labs/smarter-workflows ]; then ok "GITHUB_REPOSITORY wins"; else not_ok "GITHUB_REPOSITORY"; fi

unset GITHUB_REPOSITORY CIRCLE_REPOSITORY_URL
if github_slug_from_env 2>/dev/null; then
  not_ok "missing env should fail"
else
  ok "missing env fails"
fi

export GITHUB_REPOSITORY=CircleCI-Labs/smarter-workflows
got=$(github_https_remote_from_env)
if [ "$got" = https://github.com/CircleCI-Labs/smarter-workflows.git ]; then
  ok "https remote from GITHUB_REPOSITORY"
else
  not_ok "https remote from GITHUB_REPOSITORY (got $got)"
fi

unset GITHUB_REPOSITORY
export CIRCLE_REPOSITORY_URL=https://github.com/CircleCI-Labs/smarter-workflows
got=$(github_https_remote_from_env)
if [ "$got" = https://github.com/CircleCI-Labs/smarter-workflows.git ]; then
  ok "https remote from https clone URL"
else
  not_ok "https remote from https clone URL (got $got)"
fi

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]