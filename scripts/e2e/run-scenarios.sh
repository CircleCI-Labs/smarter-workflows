#!/usr/bin/env bash
# run-scenarios.sh: end-to-end scenarios for Smarter Workflows on real CircleCI.
#
# For each scenario of scenarios-demo.tsv (the demo monorepo, examples/demo) and
# scenarios-self.tsv (this repository's own pipeline) it:
#   1. creates the branch scenario/<name>-<unix time> from origin/main,
#   2. applies the scenario's change and commits it,
#   3. pushes the branch,
#   4. finds the pipeline of the right definition by the pushed commit,
#   5. waits for the pipeline to finish,
#   6. downloads plan.json from the plan job,
#   7. compares the jobs that ran in the planned workflow with the expectation,
#   8. checks the GitHub check of the workflow,
#   9. deletes the branch.
# It prints a markdown table of the results and exits 1 if a scenario failed.
#
# A scenarios file has one scenario per line, with tab-separated fields:
#   name  expect  change  [commit message]
# - name: letters, digits and dashes.
# - expect: one of
#     full                    the plan is a full run, and every job ran
#     full:TEXT               the same, and the reason of the full run has TEXT
#     blocked                 the setup job fails at the guardrails, nothing runs
#     JOB JOB ...             exactly these jobs ran, besides smarter-gate
# - change: a shell snippet, run in a fresh worktree of the base (one line).
# - commit message: optional; the default is "test: scenario NAME".
# Lines that start with # and blank lines are skipped.
#
# Needs git, jq, gh and the circleci CLI, all logged in. It only pushes branches
# named scenario/*, and never force-pushes. The branch of a scenario is built
# in a separate worktree of its own: nothing is checked out or committed in the
# worktree it is run from.
set -euo pipefail
export LC_ALL=C

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

usage() {
  cat << 'EOF'
usage: scripts/e2e/run-scenarios.sh [options]

  --target demo|self|all  the scenarios to run (default: all)
  --only NAME[,NAME...]   run only the scenarios with these names
  --list                  list the scenarios and exit
  --dry-run               build each scenario's commit locally, push nothing
  --evaluate BRANCH:SHA   check the pipeline of a commit that was already pushed
                          against one scenario (--only NAME); push and delete nothing
  --jobs N                run N scenarios at the same time (default: 1)
  --base-ref REF          branch the scenarios from REF (default: origin/main)
  --timeout SECONDS       how long one scenario may take (default: 1500)
  --poll SECONDS          seconds between two polls, at least 15 (default: 15)
  --keep                  do not delete the scenario branches
  -h, --help              this text

The map of the base commit must exist (a full run of its pipeline on main).
Scenarios that need it wait for it, and fail when it does not come.
EOF
}

repo=CircleCI-Labs/smarter-workflows
project=circleci/98KhFjn8YyZY9qoBGmzCBs/AEZsEwRynPFf5UojjW6akf
ui=https://app.circleci.com/pipelines/$project

target=all
only=
list=0
dry=0
evaluate=
jobs=1
base_ref=origin/main
timeout=1500
poll=15
keep=0

while [ $# -gt 0 ]; do
  case $1 in
    --target) target=${2:?--target needs a value}; shift 2 ;;
    --only) only=${2:?--only needs a value}; shift 2 ;;
    --list) list=1; shift ;;
    --dry-run) dry=1; shift ;;
    --evaluate) evaluate=${2:?--evaluate needs BRANCH:SHA}; shift 2 ;;
    --jobs) jobs=${2:?--jobs needs a value}; shift 2 ;;
    --base-ref) base_ref=${2:?--base-ref needs a value}; shift 2 ;;
    --timeout) timeout=${2:?--timeout needs a value}; shift 2 ;;
    --poll) poll=${2:?--poll needs a value}; shift 2 ;;
    --keep) keep=1; shift ;;
    -h | --help) usage; exit 0 ;;
    *) echo "run-scenarios: unknown option $1" >&2; usage >&2; exit 2 ;;
  esac
done
case $target in demo | self | all) ;; *) echo "run-scenarios: --target is demo, self or all" >&2; exit 2 ;; esac
case $jobs in '' | *[!0-9]* | 0) echo "run-scenarios: --jobs is a positive number" >&2; exit 2 ;; esac
case $timeout in '' | *[!0-9]*) echo "run-scenarios: --timeout is a number of seconds" >&2; exit 2 ;; esac
case $poll in '' | *[!0-9]*) echo "run-scenarios: --poll is a number of seconds" >&2; exit 2 ;; esac
case $evaluate in '' | *:*) ;; *) echo "run-scenarios: --evaluate is BRANCH:SHA" >&2; exit 2 ;; esac
if [ "$poll" -lt 15 ]; then
  echo "run-scenarios: --poll is at least 15 seconds" >&2
  exit 2
fi

# --- the two pipeline definitions of the project ------------------------------

# target_info NAME sets t_def (the definition id), t_setup (its setup workflow),
# t_planned (the workflow the planner continues with) and t_file. The pipeline
# API does not say which definition made a pipeline, so the pipeline of a
# definition is the one of the pushed commit that has its setup workflow: the
# two definitions of this project name their setup workflows differently.
target_info() {
  case $1 in
    demo)
      t_def=dd1a0aff-1b3d-4e2f-9aef-ca58d5bacf24
      t_setup=demo-plan
      t_planned=demo
      t_file=$here/scenarios-demo.tsv
      ;;
    self)
      t_def=12eb0a5c-4f1c-4ebe-a1e5-d8dda5c13c53
      t_setup=plan
      t_planned=ci
      t_file=$here/scenarios-self.tsv
      ;;
  esac
}

# GitHub names the check of a workflow "<workflow> - <first 8 of the definition id>".
check_name() { printf '%s - %s' "$1" "${t_def:0:8}"; }

# --- tools ---------------------------------------------------------------------

circleci=circleci
if ! command -v circleci > /dev/null 2>&1 && [ -x "$HOME/.local/bin/circleci" ]; then
  circleci=$HOME/.local/bin/circleci
fi
if [ "$list" -eq 0 ]; then
  for tool in git jq gh "$circleci"; do
    if ! command -v "$tool" > /dev/null 2>&1; then
      echo "run-scenarios: $tool is needed" >&2
      exit 2
    fi
  done
fi

# cci PATH: the body of GET PATH from the CircleCI API, with a few retries.
cci() {
  local out i
  for i in 1 2 3; do
    if out=$("$circleci" api "$1" 2> /dev/null); then
      printf '%s' "$out"
      return 0
    fi
    sleep 3
  done
  return 1
}

now() { date +%s; }

# us separates the fields of a result file: it is not blank, so an empty field
# stays one.
us=$'\x1f'

# is_terminal STATUS: the workflow will not change any more (an approval that
# waits for a person counts: nothing else is going to run).
is_terminal() {
  case $1 in success | failed | error | canceled | unauthorized | not_run | on_hold) return 0 ;; esac
  return 1
}

# --- scenarios -------------------------------------------------------------------

work=$(mktemp -d "${TMPDIR:-/tmp}/smarter-scenarios.XXXXXX")
branches=$work/branches # pushed
: > "$branches"
: > "$work/created" # created locally
git_lock=$work/git.lock

# shellcheck disable=SC2329  # called from the EXIT trap
delete_branches() {
  local b
  while read -r b; do
    [ -n "$b" ] || continue
    case $b in scenario/*) ;; *) continue ;; esac
    git push -q origin --delete "$b" > /dev/null 2>&1 || true
  done < "$branches"
}

# shellcheck disable=SC2329  # the EXIT trap
cleanup() {
  trap - EXIT INT TERM
  jobs -p | xargs -r kill 2> /dev/null || true
  if [ "$keep" -eq 0 ] && [ "$dry" -eq 0 ]; then
    delete_branches
  fi
  local created b
  created=$(cat "$work/created" 2> /dev/null || true)
  rm -rf "$work"
  git worktree prune > /dev/null 2>&1 || true
  if [ "$keep" -eq 0 ]; then
    for b in $created; do
      case $b in scenario/*) git branch -q -D "$b" > /dev/null 2>&1 || true ;; esac
    done
  fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# Scenario rows: "target<TAB>name<TAB>expect<TAB>change<TAB>message".
rows=$work/rows
: > "$rows"
for tgt in demo self; do
  if [ "$target" != all ] && [ "$target" != "$tgt" ]; then
    continue
  fi
  target_info "$tgt"
  while IFS=$'\t' read -r name expect change message || [ -n "$name" ]; do
    case $name in '' | '#'*) continue ;; esac
    if [ -n "$only" ] && ! printf ',%s,' "$only" | grep -q ",$name,"; then
      continue
    fi
    if [ -z "$expect" ] || [ -z "$change" ]; then
      echo "run-scenarios: $t_file: $name needs an expectation and a change" >&2
      exit 2
    fi
    printf '%s\t%s\t%s\t%s\t%s\n' "$tgt" "$name" "$expect" "$change" "$message" >> "$rows"
  done < "$t_file"
done
count=$(wc -l < "$rows" | tr -d ' ')
if [ "$count" -eq 0 ]; then
  echo "run-scenarios: no scenario matches" >&2
  exit 2
fi

if [ "$list" -eq 1 ]; then
  while IFS=$'\t' read -r tgt name expect _; do
    printf '%-5s %-26s %s\n' "$tgt" "$name" "$expect"
  done < "$rows"
  exit 0
fi

base_sha=
if [ -z "$evaluate" ]; then
  echo "base: $base_ref"
  if [ "$dry" -eq 0 ]; then
    case $base_ref in
      origin/*) git fetch -q origin "${base_ref#origin/}" ;;
    esac
  fi
  base_sha=$(git rev-parse --verify -q "$base_ref^{commit}") || {
    echo "run-scenarios: cannot resolve $base_ref" >&2
    exit 2
  }
  echo "base commit: $base_sha"
elif [ "$count" -ne 1 ]; then
  echo "run-scenarios: --evaluate needs exactly one scenario (--target and --only)" >&2
  exit 2
fi

# --- one scenario ----------------------------------------------------------------

# map_ready SETUP PLANNED: whether the pipeline of the base commit on main has
# built its impact map (the smarter-impact-map job of the planned workflow
# succeeded). Prints why not, and returns 1, when it has not (yet).
map_ready() {
  local ids id wfs wf jobs_json st
  ids=$(cci "api/v2/project/$project/pipeline?branch=main" |
    jq -r --arg sha "$base_sha" '.items[] | select((.trigger_parameters.git.checkout_sha // "") == $sha) | .id') || ids=
  for id in $ids; do
    wfs=$(cci "api/v2/pipeline/$id/workflow") || continue
    jq -e --arg n "$1" '.items[] | select(.name == $n)' <<< "$wfs" > /dev/null || continue
    wf=$(jq -r --arg n "$2" '[.items[] | select(.name == $n)][0].id // ""' <<< "$wfs")
    if [ -z "$wf" ]; then
      echo "the pipeline of the base commit has no $2 workflow yet"
      return 1
    fi
    jobs_json=$(cci "api/v2/workflow/$wf/job") || return 1
    st=$(jq -r '[.items[] | select(.name == "smarter-impact-map")][0].status // "absent"' <<< "$jobs_json")
    if [ "$st" = success ]; then
      return 0
    fi
    echo "the smarter-impact-map job of the base commit is $st"
    return 1
  done
  echo "no pipeline of this definition for the base commit on main"
  return 1
}

# check_conclusion SHA NAME: success, failure, ... or pending or missing.
check_conclusion() {
  gh api "repos/$repo/commits/$1/check-runs?per_page=100" 2> /dev/null |
    jq -r --arg n "$2" '
      [.check_runs[] | select(.name == $n)][0]
      | if . == null then "missing"
        elif .status != "completed" then "pending"
        else (.conclusion // "none") end' || echo missing
}

# wait_check SHA NAME DEADLINE: wait for the check to conclude, print the result.
wait_check() {
  local c
  while :; do
    c=$(check_conclusion "$1" "$2")
    case $c in pending | missing) ;; *) echo "$c"; return 0 ;; esac
    if [ "$(now)" -ge "$3" ]; then
      echo "$c"
      return 0
    fi
    sleep "$poll"
  done
}

# reason_of PLAN JOB: the plan's reasons for a job. A job of a matrix has the
# name of its entry plus a suffix.
reason_of() {
  jq -r --arg n "$2" '
    ([.jobs[] | select(.name == $n)][0]) //
    ([.jobs[] | select(.name != "" and ($n | startswith(.name + "-")))][0]) //
    null
    | if . == null then "not in the plan" else (.reasons | join("; ")) end' "$1"
}

# run_scenario INDEX: runs one scenario. Its log goes to stdout, and the result
# to $work/INDEX.res: status, target, name, expect, jobs that ran, pipeline
# number and note, separated by the unit separator ($us).
run_scenario() {
  local idx=$1 tgt name expect change message
  IFS=$'\t' read -r tgt name expect change message < <(sed -n "${idx}p" "$rows")
  target_info "$tgt"
  [ -n "$message" ] || message="test: scenario $name"

  local status=FAIL note="" ran="" pnum="" branch sha wt pid dir
  local problems=()
  finish() {
    printf '%s\n' "$status$us$tgt$us$name$us$expect$us$ran$us$pnum$us$note" > "$work/$idx.res"
  }
  fail() { problems+=("$1"); }

  echo "=== $tgt/$name: expect $expect"
  wt=$work/wt-$idx
  dir=$work/art-$idx

  local started deadline
  if [ -n "$evaluate" ]; then
    branch=${evaluate%%:*}
    sha=${evaluate#*:}
    echo "  evaluating the pipeline of $branch at ${sha:0:9}; nothing is pushed or deleted"
  else
    # 1-2. The branch and its commit, in a worktree of its own. The branch is
    # scenario/<name>-<unix time>; the time moves on if that name is taken.
    {
      flock 9
      local ts
      ts=$(now)
      while git show-ref -q --verify "refs/heads/scenario/$name-$ts"; do ts=$((ts + 1)); done
      branch=scenario/$name-$ts
      git worktree add -q --no-track -b "$branch" "$wt" "$base_sha"
    } 9> "$git_lock"
    printf '%s\n' "$branch" >> "$work/created"
    if ! (cd "$wt" && bash -euo pipefail -c "$change") > "$work/$idx.change.log" 2>&1; then
      sed 's/^/  change: /' "$work/$idx.change.log"
      status=ERROR; note="the change failed"; finish
      echo "  ERROR: $note"
      return 0
    fi
    git -C "$wt" add -A
    if git -C "$wt" diff --cached --quiet; then
      status=ERROR; note="the change did not change anything"; finish
      echo "  ERROR: $note"
      return 0
    fi
    git -C "$wt" commit -q -m "$message"
    sha=$(git -C "$wt" rev-parse HEAD)
    echo "  branch $branch, commit ${sha:0:9}"
    git -C "$wt" show --stat --format= HEAD | sed 's/^/  /'
    if [ "$dry" -eq 1 ]; then
      status=DRY; note="not pushed"; finish
      return 0
    fi

    # The map of the base commit has to exist before the plan job looks for it,
    # so the scenarios that select jobs wait for it before they push.
    started=$(now)
    deadline=$((started + timeout))
    case $expect in
      full | full:* | blocked) ;;
      *)
        if [ "$base_ref" = origin/main ]; then
          local why
          while ! why=$(map_ready "$t_setup" "$t_planned"); do
            if [ "$(now)" -ge "$deadline" ]; then
              status=ERROR; note="the base commit has no impact map: $why"; finish
              echo "  ERROR: $note"
              return 0
            fi
            echo "  waiting for the map of the base commit: $why"
            sleep "$poll"
          done
        fi
        ;;
    esac

    # 3. Push. Only scenario/* branches are ever pushed.
    case $branch in scenario/*) ;; *) echo "refusing to push $branch" >&2; exit 2 ;; esac
    printf '%s\n' "$branch" >> "$branches"
    git -C "$wt" push -q origin "refs/heads/$branch:refs/heads/$branch"
    started=$(now)
    deadline=$((started + timeout))
  fi
  started=$(now)
  deadline=$((started + timeout))

  # 4. The pipeline of this definition, by the commit.
  echo "  waiting for the pipeline of $t_setup / $t_planned ..."
  pid=
  local wfs="" setup_status="" planned_status="" setup_done=0 setup_wf="" planned_wf=""
  while :; do
    local ids id
    ids=$(cci "api/v2/project/$project/pipeline?branch=$(jq -rn --arg s "$branch" '$s | @uri')" |
      jq -r --arg sha "$sha" '.items[] | select((.trigger_parameters.git.checkout_sha // "") == $sha) | .id') || ids=
    if [ -z "$pid" ]; then
      for id in $ids; do
        wfs=$(cci "api/v2/pipeline/$id/workflow") || continue
        if jq -e --arg n "$t_setup" '.items[] | select(.name == $n)' <<< "$wfs" > /dev/null; then
          pid=$id
          break
        fi
      done
    fi
    # 5. Wait for it to finish.
    if [ -n "$pid" ]; then
      wfs=$(cci "api/v2/pipeline/$pid/workflow") || wfs='{"items":[]}'
      setup_status=$(jq -r --arg n "$t_setup" '[.items[] | select(.name == $n)][0].status // ""' <<< "$wfs")
      planned_status=$(jq -r --arg n "$t_planned" '[.items[] | select(.name == $n)][0].status // ""' <<< "$wfs")
      if is_terminal "$setup_status"; then
        [ "$setup_done" -ne 0 ] || setup_done=$(now)
        if [ "$setup_status" != success ]; then
          break
        elif [ -n "$planned_status" ] && is_terminal "$planned_status"; then
          break
        elif [ -z "$planned_status" ] && [ $(($(now) - setup_done)) -gt 90 ]; then
          break
        fi
      fi
    fi
    if [ -z "$pid" ] && [ "$(now)" -ge $((started + 300)) ]; then
      local errs
      errs=$(cci "api/v2/project/$project/pipeline?branch=$(jq -rn --arg s "$branch" '$s | @uri')" |
        jq -r '[.items[] | select((.errors | length) > 0) | .errors[].message] | unique | join("; ")' || true)
      status=ERROR; note="no pipeline with a $t_setup workflow for the commit after 300s${errs:+; errored pipelines say: $errs}"; finish
      echo "  ERROR: $note"
      return 0
    fi
    if [ "$(now)" -ge "$deadline" ]; then
      status=ERROR; note="timed out after ${timeout}s (setup: ${setup_status:-no pipeline}, planned: ${planned_status:-none})"; finish
      echo "  ERROR: $note"
      return 0
    fi
    sleep "$poll"
  done
  pnum=$(cci "api/v2/pipeline/$pid" | jq -r '.number // ""')
  setup_wf=$(jq -r --arg n "$t_setup" '[.items[] | select(.name == $n)][0].id // ""' <<< "$wfs")
  planned_wf=$(jq -r --arg n "$t_planned" '[.items[] | select(.name == $n)][0].id // ""' <<< "$wfs")
  echo "  pipeline #$pnum: $t_setup $setup_status, $t_planned ${planned_status:-none} ($(($(now) - started))s)"
  echo "  $ui/$pnum"

  # 6. plan.json of the plan job.
  local plan=""
  local setup_jobs plan_job
  setup_jobs=$(cci "api/v2/workflow/$setup_wf/job" || echo '{"items":[]}')
  plan_job=$(jq -r '[.items[] | select(.name == "plan")][0].id // ""' <<< "$setup_jobs")
  if [ -n "$plan_job" ]; then
    mkdir -p "$dir"
    "$circleci" artifact "$plan_job" --output "$dir" > /dev/null 2>&1 || true
    plan=$(find "$dir" -name plan.json 2> /dev/null | head -n 1)
  fi
  local plan_full=false plan_reason=""
  if [ -n "$plan" ]; then
    plan_full=$(jq -r '.full' "$plan")
    plan_reason=$(jq -r '.full_reason' "$plan")
    echo "  plan: mode $(jq -r '.mode' "$plan"), full=$plan_full${plan_reason:+ ($plan_reason)}, map $(jq -r '(.map_revision // "") | if . == "" then "none" else .[0:9] end' "$plan"), base $(jq -r '(.base // "") | if . == "" then "none" else .[0:9] end' "$plan")"
    jq -r '.warnings[]? | "  plan warning: " + .' "$plan"
    jq -r '.guardrails | "  guardrails: " + .status + (if (.repairs | length) > 0 then ", repairs " + (.repairs | join(", ")) else "" end)' "$plan"
    jq -r '.guardrails.violations[]? | select(.hard) | "  hard violation: " + .rule + ": " + .message' "$plan"
  else
    echo "  plan.json: not available"
  fi

  # 7. The jobs that ran against the expectation.
  local actual="" actual_all="" expected
  local wf_jobs_json="" bad=""
  if [ -n "$planned_wf" ]; then
    wf_jobs_json=$(cci "api/v2/workflow/$planned_wf/job" || echo '{"items":[]}')
    actual_all=$(jq -r '.items[] | select(.type != "approval") | .name' <<< "$wf_jobs_json" | sort)
    actual=$(grep -vxE 'smarter-gate|smarter-impact-map' <<< "$actual_all" || true)
    bad=$(jq -r '.items[] | select(.type != "approval" and .status != "success") | .name + " (" + .status + ")"' <<< "$wf_jobs_json")
    ran=$(tr '\n' ' ' <<< "$actual" | sed 's/ $//')
    echo "  ran: ${ran:-nothing} (+ smarter-gate)"
  fi

  case $expect in
    blocked)
      if [ -n "$planned_wf" ]; then
        fail "the pipeline continued, a $t_planned workflow ran ($planned_status)"
      fi
      if [ "$setup_status" != failed ]; then
        fail "the setup workflow is $setup_status, want failed"
      fi
      if [ -z "$plan" ]; then
        fail "plan.json is not available, so a failure at the guardrails cannot be shown"
      else
        [ "$(jq -r '.guardrails.status' "$plan")" = HARD_FAIL ] || fail "guardrails status is $(jq -r '.guardrails.status' "$plan"), want HARD_FAIL"
        [ "$(jq -r '[.guardrails.violations[]? | select(.hard)] | length' "$plan")" -gt 0 ] || fail "the plan records no hard violation"
      fi
      ;;
    *)
      if [ "$setup_status" != success ]; then
        fail "the setup workflow is $setup_status, want success"
      fi
      if [ -z "$planned_wf" ]; then
        fail "no $t_planned workflow was created"
      else
        [ "$planned_status" = success ] || fail "the $t_planned workflow is $planned_status, want success"
        [ -z "$bad" ] || fail "jobs that did not succeed: $(tr '\n' ' ' <<< "$bad")"
        grep -qx 'smarter-gate' <<< "$actual_all" || fail "the workflow has no smarter-gate job"
      fi
      if [ -n "$plan" ] && [ -n "$planned_wf" ]; then
        # The plan and the workflow agree: what the plan says runs is what ran.
        # A matrix entry of the plan is several jobs of the workflow, named
        # after the entry and the values.
        local cross
        cross=$(awk '
          NR == FNR { p[$0] = 1; next }
          { a[$0] = 1 }
          END {
            for (n in p) {
              if (n in a) { used[n] = 1; continue }
              found = 0
              for (x in a) if (index(x, n "-") == 1) { used[x] = 1; found = 1 }
              if (!found) print "P " n
            }
            for (x in a) if (!(x in used) && !(x in p)) print "A " x
          }' <(jq -r '.jobs[] | select(.run) | .name' "$plan") <(printf '%s\n' "$actual" | grep -v '^$' || true))
        local only_plan only_wf
        only_plan=$(sed -n 's/^P //p' <<< "$cross" | sort | tr '\n' ' ')
        only_wf=$(sed -n 's/^A //p' <<< "$cross" | sort | tr '\n' ' ')
        [ -z "$only_plan" ] || fail "the plan says these jobs run, but they are not in the workflow: $only_plan"
        [ -z "$only_wf" ] || fail "these jobs are in the workflow, but the plan does not run them: $only_wf"
      fi
      case $expect in
        full | full:*)
          if [ -n "$plan" ]; then
            [ "$plan_full" = true ] || fail "the plan is not a full run (jobs skipped: $(jq -r '[.jobs[] | select(.run | not) | .name] | join(", ")' "$plan"))"
            case $plan_reason in
              "no usable impact map"* | "no diff base"*) fail "the full run has the reason \"$plan_reason\", which is a setup problem, not what the scenario tests" ;;
            esac
            if [ "${expect#full:}" != "$expect" ] && [ "${plan_reason#*"${expect#full:}"}" = "$plan_reason" ]; then
              fail "the reason of the full run is \"$plan_reason\", want one with \"${expect#full:}\""
            fi
            local skipped
            skipped=$(jq -r '.jobs[] | select((.run | not) and ((.reasons[0] // "") | startswith("does not run") | not)) | .name' "$plan" | tr '\n' ' ')
            [ -z "$skipped" ] || fail "a full run skipped jobs: $skipped"
          else
            fail "plan.json is not available"
          fi
          ;;
        *)
          if [ -n "$plan" ] && [ "$plan_full" = true ]; then
            fail "the plan is a full run ($plan_reason), want a selection"
          fi
          if [ -n "$plan" ] && [ "$(jq -r '.map_revision' "$plan")" != "$(jq -r '.base' "$plan")" ]; then
            # The base of a scenario is main, whose pipeline built this map: any
            # other map is a stale one, and what it skips proves nothing.
            fail "the plan used the impact map of $(jq -r '.map_revision | .[0:9]' "$plan"), not the one of its base $(jq -r '.base | .[0:9]' "$plan")"
          fi
          expected=$(tr ' ' '\n' <<< "$expect" | grep -v '^$' | sort -u)
          local missing unexpected j
          missing=$(comm -23 <(printf '%s\n' "$expected") <(printf '%s\n' "$actual" | grep -v '^$' || true) || true)
          unexpected=$(comm -13 <(printf '%s\n' "$expected") <(printf '%s\n' "$actual" | grep -v '^$' || true) || true)
          if [ -n "$missing" ] || [ -n "$unexpected" ]; then
            fail "the jobs that ran are not the expected ones"
            echo "  MISMATCH"
            echo "    expected: $(tr '\n' ' ' <<< "$expected" | sed 's/ $//')"
            echo "    actual:   ${ran:-nothing}"
            if [ -n "$plan" ]; then
              for j in $missing; do echo "    - $j did not run; plan: $(reason_of "$plan" "$j")"; done
              for j in $unexpected; do echo "    + $j ran; plan: $(reason_of "$plan" "$j")"; done
            fi
          fi
          ;;
      esac
      ;;
  esac

  # 8. The GitHub check of the workflow.
  local want_wf want_conclusion c
  if [ "$expect" = blocked ]; then
    want_wf=$t_setup; want_conclusion=failure
  else
    want_wf=$t_planned; want_conclusion=success
  fi
  c=$(wait_check "$sha" "$(check_name "$want_wf")" $(($(now) + 180)))
  echo "  check \"$(check_name "$want_wf")\": $c"
  [ "$c" = "$want_conclusion" ] || fail "the check \"$(check_name "$want_wf")\" is $c, want $want_conclusion"

  if [ "${#problems[@]}" -eq 0 ]; then
    status=PASS
    echo "  PASS"
  else
    local p
    for p in "${problems[@]}"; do echo "  FAIL: $p"; done
    note=${problems[0]}
  fi
  finish
}

# --- run them ----------------------------------------------------------------------

echo "scenarios: $count, in $jobs at a time"
i=0
running=0
while [ "$i" -lt "$count" ]; do
  i=$((i + 1))
  if [ "$jobs" -eq 1 ]; then
    run_scenario "$i" 2>&1 | tee "$work/$i.log" || true
  else
    if [ "$running" -ge "$jobs" ]; then
      wait -n || true
      running=$((running - 1))
    fi
    run_scenario "$i" > "$work/$i.log" 2>&1 &
    running=$((running + 1))
  fi
  if [ "$dry" -eq 0 ]; then
    : # a scenario that did not write a result is reported below
  fi
done
wait || true

if [ "$jobs" -gt 1 ]; then
  for ((i = 1; i <= count; i++)); do
    cat "$work/$i.log"
  done
fi

# --- results ------------------------------------------------------------------------

failed=0
echo
echo "| Scenario | Target | Expected | Result | Pipeline | Note |"
echo "|---|---|---|---|---|---|"
for ((i = 1; i <= count; i++)); do
  if [ -f "$work/$i.res" ]; then
    IFS=$us read -r st tgt name expect _ pnum note < "$work/$i.res"
  else
    IFS=$'\t' read -r tgt name expect _ < <(sed -n "${i}p" "$rows")
    st=ERROR; pnum=""; note="no result (see the log above)"
  fi
  case $st in PASS | DRY) ;; *) failed=$((failed + 1)) ;; esac
  link=
  [ -z "$pnum" ] || link="[#$pnum]($ui/$pnum)"
  printf '| %s | %s | %s | %s | %s | %s |\n' "$name" "$tgt" "$(printf '%s' "$expect" | sed 's/|/\\|/g')" "$st" "$link" "$(printf '%s' "$note" | sed 's/|/\\|/g')"
done
echo
if [ "$failed" -eq 0 ]; then
  echo "all $count scenarios passed"
  exit 0
fi
echo "$failed of $count scenarios failed"
exit 1
