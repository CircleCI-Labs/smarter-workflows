#!/usr/bin/env bash
# summary.sh: benchmark savings table for run-scenarios.sh (uses $csv and $timing).
# shellcheck shell=bash
: "${csv:=}"
: "${timing:=}"

# median_of: the median of the integers on stdin.
median_of() {
  sort -n | awk '{a[NR]=$1} END {
    if (NR == 0) printf "0"
    else if (NR % 2) printf "%d", a[(NR + 1) / 2]
    else printf "%.0f", (a[NR / 2] + a[NR / 2 + 1]) / 2
  }'
}

# target_full_median TARGET: median job seconds of full-run scenarios for one target.
target_full_median() {
  local tgt=$1 med
  med=$(awk -F'\t' -v t="$tgt" '$2 == t && $6 == "PASS" && $4 ~ /^full/ && $5 ~ /^[0-9]+$/ { print $5 }' "$timing" | median_of)
  if ! [ "$med" -gt 0 ] 2> /dev/null; then
    printf '%s\n' "$E2E_TIMING_NA"
  else
    printf '%s\n' "$med"
  fi
}

# print_savings_summary: medians per scenario against that target's full-run median.
print_savings_summary() {
  local full_med name tgt med saved pct
  declare -A full_by_tgt=()
  if [ -z "$csv" ]; then
    return 0
  fi
  if [ ! -s "${timing:-}" ]; then
    echo "no timing data"
    return 0
  fi
  echo
  echo "### Median job seconds (benchmark savings)"
  echo
  echo "| Scenario | Target | Median job s | Full median s | Saved |"
  echo "|---|---|---:|---:|---:|"
  while IFS=$'\t' read -r name tgt; do
    [ -n "$name" ] || continue
    if [ -z "${full_by_tgt[$tgt]+x}" ]; then
      full_by_tgt[$tgt]=$(target_full_median "$tgt")
    fi
    full_med=${full_by_tgt[$tgt]}
    med=$(awk -F'\t' -v n="$name" -v t="$tgt" \
      '$1 == n && $2 == t && $6 == "PASS" && $5 ~ /^[0-9]+$/ { print $5 }' "$timing" | median_of)
    if ! [ "$med" -gt 0 ] 2> /dev/null; then
      med=$E2E_TIMING_NA
    fi
    if [ "$full_med" != "$E2E_TIMING_NA" ] && [ "$med" != "$E2E_TIMING_NA" ]; then
      saved=$((full_med - med))
      pct=$(awk -v s="$saved" -v f="$full_med" 'BEGIN { if (f > 0) printf "%.0f", 100 * s / f; else print "0" }')
      printf '| %s | %s | %s | %s | %s%% |\n' "$name" "$tgt" "$med" "$full_med" "$pct"
    else
      printf '| %s | %s | %s | %s | — |\n' "$name" "$tgt" "$med" "$full_med"
    fi
  done < <(awk -F'\t' '{ print $1 FS $2 }' "$timing" | sort -u)
  return 0
}