#!/bin/sh
# reset-atime.sh: the post-checkout step of Smarter Workflows tracing
# (docs/adr/0003, docs/spec/v0.1.md "The algorithm" step 8).
#
# Why: `checkout` reads every file (git's racy-index check), so right after it
# every atime is newer than its mtime and a trace would list the whole repo.
# This step puts every atime back to the epoch (0) and stops git from reading
# files again, so from here on an atime above 0 means "the job read it".
#
# What it does, in order:
#   1. Probes that atime works on this filesystem: a file and a directory under
#      .git/ get atime 0 and mtime 1, are read, and atime must move.
#   2. Waits one second, refreshes git's index and stops git trusting ctime, so
#      `git status` and `git diff` read nothing but .gitignore files.
#   3. Sets atime to 0 on every tracked file and on every directory (mtimes stay
#      as they are).
#   4. Writes .git/smarter-trace-ready (ok) or .git/smarter-trace-unsupported
#      (one line, the reason). collect-trace.sh reads these markers.
#
# It runs in $CIRCLE_WORKING_DIRECTORY (a leading ~ is expanded), or in the
# current directory. It needs touch and find that can reset and read atimes;
# GNU find/touch are preferred, busybox forms are supported. It never fails
# the job: every path ends in exit 0, and it prints one line.
set +e

say() { printf 'smarter: %s\n' "$*"; }

gitdir=
tmp=
reason=
at=
mt=
have_printf=

cleanup() {
  if [ -n "$gitdir" ]; then
    rm -rf "$gitdir/smarter-atime-probe" "$gitdir/smarter-atime-probe.d"
  fi
  if [ -n "$tmp" ]; then
    rm -rf "$tmp"
  fi
  return 0
}

# unsupported REASON: record that tracing cannot work here, and say why.
unsupported() {
  if [ -n "$gitdir" ]; then
    rm -f "$gitdir/smarter-trace-ready"
    printf '%s\n' "$1" > "$gitdir/smarter-trace-unsupported" 2>/dev/null
  fi
  cleanup
  say "atime tracing is not available here: $1"
}

# touch_epoch FILE...: set atime to the epoch on each path (directories too).
# Busybox -t and -d date forms use the local timezone; TZ=UTC makes the epoch.
touch_epoch() {
  if touch -a -d @0 "$@" 2>/dev/null; then
    return 0
  fi
  if TZ=UTC touch -a -t 197001010000.00 "$@" 2>/dev/null; then
    return 0
  fi
  TZ=UTC touch -a -d '1970-01-01 00:00:00' "$@"
}

# stat_times PATH: set at and mt to the atime and mtime of PATH, in seconds
# since the epoch (fractions are ignored for reads).
stat_times() {
  st=
  if [ "$have_printf" = 1 ]; then
    st=$(find "$1" -maxdepth 0 -printf '%A@ %T@\n' 2>/dev/null) || st=
  fi
  if [ -z "$st" ]; then
    st=$(stat -c '%X %Y' "$1" 2>/dev/null) || return 1
  fi
  case $st in
    *" "*" "*) return 1 ;;
    *" "*) ;;
    *) return 1 ;;
  esac
  at=${st%% *}
  mt=${st#* }
  case $at in '' | *[!0-9.]*) return 1 ;; esac
  case $mt in '' | *[!0-9.]*) return 1 ;; esac
  return 0
}

# is_zero N: true if the time N is the epoch (only zeros and a dot).
is_zero() {
  case $1 in '' | *[!0.]*) return 1 ;; esac
  return 0
}

# probe: check that this filesystem updates atime on reads and on directory
# listings, and that touch and find do what the reset needs. Sets reason.
probe() {
  pfile=$gitdir/smarter-atime-probe
  pdir=$gitdir/smarter-atime-probe.d
  rm -rf "$pfile" "$pdir"
  if ! { printf 'probe\n' > "$pfile" && mkdir "$pdir"; } 2>/dev/null; then
    reason="cannot create files under the git directory"
    return 1
  fi
  if ! touch -m -d @1 "$pfile" "$pdir" 2>/dev/null; then
    if ! TZ=UTC touch -m -t 197001010100.00 "$pfile" "$pdir" 2>/dev/null; then
      reason="touch cannot set mtime (GNU or busybox touch is needed)"
      return 1
    fi
  fi
  if ! stat_times "$pfile"; then
    reason="cannot read atime and mtime (GNU find -printf or stat -c is needed)"
    return 1
  fi
  mt1=$mt
  if ! touch_epoch "$pfile" "$pdir"; then
    reason="touch cannot reset atime to the epoch"
    return 1
  fi
  if ! stat_times "$pfile" || ! is_zero "$at" || [ "$mt" != "$mt1" ]; then
    reason="touch cannot reset atime alone here"
    return 1
  fi
  if ! stat_times "$pdir" || ! is_zero "$at"; then
    reason="atime of a directory cannot be reset on this filesystem"
    return 1
  fi
  cat "$pfile" > /dev/null 2>&1
  ls "$pdir" > /dev/null 2>&1
  if ! stat_times "$pfile" || is_zero "$at" || [ "${at%%.*}" -le "${mt%%.*}" ]; then
    reason="reading a file does not update its atime (noatime mount?)"
    return 1
  fi
  if ! stat_times "$pdir" || is_zero "$at" || [ "${at%%.*}" -le "${mt%%.*}" ]; then
    reason="listing a directory does not update its atime (nodiratime mount?)"
    return 1
  fi
  return 0
}

run_reset() {
  dir=${CIRCLE_WORKING_DIRECTORY:-.}
  # shellcheck disable=SC2088
  case $dir in
    "~") dir=${HOME:-.} ;;
    "~/"*) dir=${HOME:-.}/${dir#"~/"} ;;
  esac
  if ! cd "$dir" 2>/dev/null; then
    say "no working directory to reset"
    return 0
  fi
  if [ ! -e .git ]; then
    say "no git checkout here, nothing to reset"
    return 0
  fi
  gitdir=$(git rev-parse --git-dir 2>/dev/null)
  if [ -z "$gitdir" ] || [ ! -d "$gitdir" ]; then
    gitdir=
    say "git cannot read this checkout, nothing to reset"
    return 0
  fi
  rm -f "$gitdir/smarter-trace-ready" "$gitdir/smarter-trace-unsupported"

  for tool in find touch xargs mktemp cat ls sleep tr wc stat; do
    if ! command -v "$tool" > /dev/null 2>&1; then
      unsupported "$tool not found"
      return 0
    fi
  done
  have_printf=0
  if find . -maxdepth 0 -printf '%P' >/dev/null 2>&1; then
    have_printf=1
  fi
  if ! probe; then
    unsupported "$reason"
    return 0
  fi

  sleep 1
  git update-index -q --refresh > /dev/null 2>&1
  if ! git config core.trustctime false > /dev/null 2>&1; then
    unsupported "cannot set core.trustctime"
    return 0
  fi
  tmp=$(mktemp -d "$gitdir/smarter-reset.XXXXXX" 2>/dev/null)
  if [ -z "$tmp" ]; then
    unsupported "cannot create a temp dir under the git directory"
    return 0
  fi
  if ! git ls-files -z > "$tmp/files" 2>/dev/null; then
    unsupported "git ls-files failed"
    return 0
  fi
  if ! find . -path ./.git -prune -o -type d -print0 > "$tmp/dirs" 2>/dev/null; then
    unsupported "find could not list the directories"
    return 0
  fi
  if [ -s "$tmp/files" ]; then
    if ! xargs -0 touch -c -h -a -d @0 -- < "$tmp/files" 2>/dev/null; then
      if ! TZ=UTC xargs -0 touch -c -h -a -t 197001010000.00 -- < "$tmp/files" 2>/dev/null; then
        if ! TZ=UTC xargs -0 touch -c -h -a -d '1970-01-01 00:00:00' -- < "$tmp/files" 2>/dev/null; then
          unsupported "touch could not reset the tracked files"
          return 0
        fi
      fi
    fi
  fi
  if ! xargs -0 touch -a -d @0 -- < "$tmp/dirs" 2>/dev/null; then
    if ! TZ=UTC xargs -0 touch -a -t 197001010000.00 -- < "$tmp/dirs" 2>/dev/null; then
      if ! TZ=UTC xargs -0 touch -a -d '1970-01-01 00:00:00' -- < "$tmp/dirs" 2>/dev/null; then
        unsupported "touch could not reset the directories"
        return 0
      fi
    fi
  fi

  nfiles=$(tr -cd '\0' < "$tmp/files" | wc -c | tr -d ' ')
  ndirs=$(tr -cd '\0' < "$tmp/dirs" | wc -c | tr -d ' ')
  if ! printf 'ok\n' > "$gitdir/smarter-trace-ready" 2>/dev/null; then
    unsupported "cannot write the ready marker"
    return 0
  fi
  cleanup
  say "atime reset done ($nfiles files, $ndirs dirs); this job is traced"
  return 0
}

run_reset
exit 0
