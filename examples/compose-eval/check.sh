#!/usr/bin/env bash
# check.sh: judge one compose-eval answer without a model.
#
#   examples/compose-eval/check.sh CASE FILE      (CASE is 1, 2 or 3)
#
# Runs circleci config validate, circleci-smarter layout check (mandatory
# security-scan, restricted context deploy, the guardrail bundle) and the case's
# own assertions on the catalog of the answer. Prints one line per check and
# exits 1 if any fails. Needs circleci-smarter (SMARTER_BIN), the circleci CLI
# (CIRCLECI_BIN) and jq.
set -uo pipefail

case_no=${1:?usage: check.sh CASE FILE}
file=${2:?usage: check.sh CASE FILE}
smarter=${SMARTER_BIN:-circleci-smarter}
circleci=${CIRCLECI_BIN:-circleci}
root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
fail=0

pass() { printf 'PASS  %s\n' "$1"; }
flunk() { printf 'FAIL  %s\n' "$1"; fail=1; }

if "$circleci" config validate "$file" > /dev/null 2>&1; then pass "config validate"; else flunk "config validate"; fi

layout=$("$smarter" layout check --config "$file" --workflow demo --mandatory security-scan \
  --restricted-contexts deploy --policy-bundle "$root/policies" --format json 2> /dev/null)
errors=$(jq '[.findings[] | select(.severity == "error")] | length' <<< "$layout" 2> /dev/null || echo "?")
if [ "$errors" = 0 ]; then
  pass "layout check and guardrails: no errors"
else
  flunk "layout check and guardrails: $errors errors"
  jq -r '.findings[]? | select(.severity == "error") | "      \(.code) [\(.job)]"' <<< "$layout" 2> /dev/null
fi

catalog=$("$smarter" catalog --config "$file" --workflow demo 2> /dev/null) || { flunk "catalog"; exit 1; }

# names: the workflow's job names. ancestor A B: A is upstream of B through requires.
names() { jq -r '.jobs[].name' <<< "$catalog" | sort -u; }
ancestor() {
  jq -e --arg a "$1" --arg b "$2" '
    (.jobs | map({key: .name, value: [.requires[]?.job]}) | from_entries) as $req
    | def up($n; $seen): ($req[$n] // []) | map(select(. as $x | $seen | index($x) | not))
        | . as $next | if length == 0 then $seen else reduce $next[] as $m ($seen + $next; up($m; .)) end;
    up($b; []) | index($a) != null' <<< "$catalog" > /dev/null
}
has() { names | grep -qx "$1"; }
kind_of() { jq -r --arg n "$1" '.jobs[] | select(.name == $n) | .kind' <<< "$catalog" | head -1; }
approval_before() { for a in $(jq -r '.jobs[] | select(.kind == "approval") | .name' <<< "$catalog"); do ancestor "$a" "$1" && return 0; done; return 1; }
context_of() { jq -r --arg n "$1" '.jobs[] | select(.name == $n) | .contexts[]?' <<< "$catalog"; }

check() { if eval "$2"; then pass "$1"; else flunk "$1"; fi; }

case $case_no in
  1)
    for j in lint api-test web-test docs-check infra-validate security-scan build e2e deploy web-bundle smarter-gate; do
      check "has $j" "has $j"
    done
    check "worker-test is a matrix" '[ "$(kind_of worker-test)" = matrix ]'
    check "e2e runs after build" "ancestor build e2e"
    check "deploy runs after an approval" "approval_before deploy"
    check "deploy keeps the context deploy" 'context_of deploy | grep -qx deploy'
    check "deploy runs after e2e" "ancestor e2e deploy"
    ;;
  2)
    allowed=$'api-test\nbuild\ne2e\nlint\nsecurity-scan\nsmarter-gate\nworker-test'
    extra=$(comm -23 <(names) <(printf '%s\n' "$allowed") | while read -r n; do [ "$(kind_of "$n")" = no-op ] || echo "$n"; done)
    check "only the selected jobs (plus no-ops)" '[ -z "$extra" ]'
    missing=$(comm -13 <(names) <(printf '%s\n' "$allowed"))
    check "every selected job is there" '[ -z "$missing" ]'
    check "e2e runs after build" "ancestor build e2e"
    ;;
  3)
    for j in security-scan deploy smarter-gate; do check "has $j" "has $j"; done
    check "deploy runs after an approval" "approval_before deploy"
    check "deploy keeps the context deploy" 'context_of deploy | grep -qx deploy'
    check "e2e runs after build" "ancestor build e2e"
    ;;
  *) echo "check.sh: unknown case $case_no" >&2; exit 2 ;;
esac
exit "$fail"
