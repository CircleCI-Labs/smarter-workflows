#!/usr/bin/env bash
# timing.sh: parse CircleCI v2 timestamps and sum job run times for run-scenarios.sh.
E2E_TIMING_NA=n/a

E2E_JQ_DEF="def iso_to_epoch:
  if . == null or type != \"string\" or . == \"\" then null
  else (if test(\"\\\\.[0-9]+Z\$\") then sub(\"\\\\.[0-9]+Z\$\"; \"Z\") else . end | fromdateiso8601)
  end;"

E2E_JQ_SUM="
[.items[] | select(.type != \"approval\")
  | (.stopped_at // \"\") as \$st | (.started_at // \"\") as \$sa
  | select(\$st != \"\" and \$sa != \"\")
  | ((\$st | iso_to_epoch) as \$e | (\$sa | iso_to_epoch) as \$s
     | select(\$e != null and \$s != null) | (\$e - \$s))] | add // empty"

E2E_JQ_PLAN="
(.items[] | select(.name == \"plan\") | select((.started_at // \"\") != \"\" and (.stopped_at // \"\") != \"\")
  | (.stopped_at | iso_to_epoch) as \$e | (.started_at | iso_to_epoch) as \$s
  | select(\$e != null and \$s != null) | (\$e - \$s)) // empty"

# sum_job_run_seconds JOBS_JSON — a whole number or n/a; errors never propagate.
sum_job_run_seconds() {
  local json=$1 out
  if ! out=$(jq -r "${E2E_JQ_DEF} ${E2E_JQ_SUM}" <<< "$json" 2> /dev/null); then
    printf '%s\n' "$E2E_TIMING_NA"
    return 0
  fi
  if [ -z "$out" ] || [ "$out" = null ]; then
    printf '%s\n' "$E2E_TIMING_NA"
  else
    printf '%.0f\n' "$out"
  fi
}

# workflow_wall_seconds NAME WORKFLOWS_JSON
workflow_wall_seconds() {
  local name=$1 json=$2 out wf
  wf="
    (input.items[] | select(.name == \$n)) as \$wf
    | if \$wf == null then empty
      elif (\$wf.stopped_at // \"\") == \"\" or (\$wf.created_at // \"\") == \"\" then empty
      else (((\$wf.stopped_at | iso_to_epoch) as \$e | (\$wf.created_at | iso_to_epoch) as \$s
             | select(\$e != null and \$s != null) | (\$e - \$s) | floor)) end"
  if ! out=$(jq -rn --arg n "$name" "${E2E_JQ_DEF} ${wf}" <<< "$json" 2> /dev/null); then
    printf '%s\n' "$E2E_TIMING_NA"
    return 0
  fi
  if [ -z "$out" ] || [ "$out" = null ]; then
    printf '%s\n' "$E2E_TIMING_NA"
  else
    printf '%s\n' "$out"
  fi
}

# plan_job_seconds SETUP_JOBS_JSON — duration of the plan job, or n/a.
plan_job_seconds() {
  local json=$1 out
  if ! out=$(jq -r "${E2E_JQ_DEF} ${E2E_JQ_PLAN}" <<< "$json" 2> /dev/null); then
    printf '%s\n' "$E2E_TIMING_NA"
    return 0
  fi
  if [ -z "$out" ] || [ "$out" = null ]; then
    printf '%s\n' "$E2E_TIMING_NA"
  else
    printf '%.0f\n' "$out"
  fi
}