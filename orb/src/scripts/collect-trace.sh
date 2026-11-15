#!/bin/sh
# collect-trace.sh: the post-step of Smarter Workflows tracing
# (docs/adr/0003, docs/spec/v0.1.md "File formats" and "The algorithm" step 8).
#
# It writes the trace of this job node to
#   ${SMARTER_TRACE_DIR:-/tmp/smarter/traces}/<16 hex of sha256($CIRCLE_JOB)>.<node>.tsv
# in the "smarter-trace v1" format: a header, `supported true|false`, a reason
# when unsupported, then one F line per tracked file the job read and one D
# line per directory it listed ("." is the root).
#
# Run it once, last: its own traversal reads every directory, so a second run
# would report them all as listed (a safe over-report, but useless).
#
# The trace is `supported false` when reset-atime.sh did not run or found atime
# unusable (no .git/smarter-trace-ready marker), when there is no git checkout,
# or when GNU find -printf is missing. A job with such a trace always runs.
#
# Read means "atime is not 0": reset-atime.sh set every atime to the epoch, so
# any later read moved it. That includes the spec's "atime > mtime", and it also
# keeps what the job read and then wrote (a write moves mtime past atime) and
# the directories it listed and then added files to.
#   - Files: git-tracked files, from `git ls-files`.
#   - Dirs: directories above a tracked file, from the same single find
#     traversal, which prints each directory before it reads it.
#
# It never prints environment variables, prints one summary line, and never
# fails the job: every path ends in exit 0.
set +e
LC_ALL=C
export LC_ALL

say() { printf 'smarter: %s\n' "$*"; }

tmp=
reason=
nfiles=0
ndirs=0

cleanup() {
  if [ -n "$tmp" ]; then
    rm -rf "$tmp"
  fi
  return 0
}

# one_line TEXT: TEXT with tabs and line breaks turned into spaces.
one_line() { printf '%s' "$1" | tr '\t\r\n' '   '; }

# sha16 TEXT: the first 16 hex digits of the sha256 of TEXT.
sha16() {
  if command -v sha256sum > /dev/null 2>&1; then
    printf '%s' "$1" | sha256sum | cut -c1-16
  elif command -v shasum > /dev/null 2>&1; then
    printf '%s' "$1" | shasum -a 256 | cut -c1-16
  fi
}

# take_trace: leave the F and D lines in $tmp/body. On failure, set reason and
# return 1.
take_trace() {
  dir=${CIRCLE_WORKING_DIRECTORY:-.}
  # shellcheck disable=SC2088
  case $dir in
    "~") dir=${HOME:-.} ;;
    "~/"*) dir=${HOME:-.}/${dir#"~/"} ;;
  esac
  if ! cd "$dir" 2>/dev/null; then
    reason="the working directory does not exist"
    return 1
  fi
  if [ ! -e .git ]; then
    reason="no git checkout in the working directory"
    return 1
  fi
  gitdir=$(git rev-parse --git-dir 2>/dev/null)
  if [ -z "$gitdir" ] || [ ! -d "$gitdir" ]; then
    reason="git cannot read the checkout"
    return 1
  fi
  if [ ! -f "$gitdir/smarter-trace-ready" ]; then
    reason=
    if [ -f "$gitdir/smarter-trace-unsupported" ]; then
      IFS= read -r reason < "$gitdir/smarter-trace-unsupported"
    fi
    if [ -z "$reason" ]; then
      reason="the atime reset step did not run"
    fi
    return 1
  fi
  for tool in awk cut find grep mktemp sort tr wc; do
    if ! command -v "$tool" > /dev/null 2>&1; then
      reason="$tool not found"
      return 1
    fi
  done
  if [ "$(find "$gitdir/smarter-trace-ready" -maxdepth 0 -printf '%y' 2>/dev/null)" != f ]; then
    reason="find does not support -printf (GNU find is needed)"
    return 1
  fi
  tmp=$(mktemp -d "$gitdir/smarter-trace.XXXXXX" 2>/dev/null)
  if [ -z "$tmp" ]; then
    reason="cannot create a temp dir under the git directory"
    return 1
  fi

  # The one traversal. Nothing may list a directory before it.
  if ! find . -path ./.git -prune -o -printf '%y\t%A@\t%T@\t%P\n' > "$tmp/found" 2> "$tmp/found.err"; then
    msg=
    IFS= read -r msg < "$tmp/found.err"
    reason="find failed: $msg"
    return 1
  fi
  if ! git ls-files -z > "$tmp/tracked0" 2>/dev/null; then
    reason="git ls-files failed"
    return 1
  fi
  if [ "$(tr -cd '\t\n\r' < "$tmp/tracked0" | wc -c | tr -d ' ')" != 0 ]; then
    reason="a tracked path contains a tab or a line break"
    return 1
  fi
  tr '\0' '\n' < "$tmp/tracked0" > "$tmp/tracked"

  # found: type, atime, mtime and path, tab separated. tracked: one path a line.
  # Output: "1F<tab>path" or "2D<tab>path", so a sort puts files before dirs.
  if ! awk '
    BEGIN { FS = "\t" }
    FILENAME == ARGV[1] {
      tracked[$0] = 1
      n = split($0, part, "/")
      d = ""
      for (i = 1; i < n; i++) {
        d = (i == 1) ? part[1] : (d "/" part[i])
        hasfile[d] = 1
      }
      hasfile["."] = 1
      next
    }
    {
      ty = $1; at = $2; mt = $3
      if (ty !~ /^[a-z]$/ || at !~ /^[0-9]+(\.[0-9]+)?$/ || mt !~ /^[0-9]+(\.[0-9]+)?$/) next
      if (at !~ /[1-9]/) next
      path = substr($0, length(ty) + length(at) + length(mt) + 4)
      if (path == "") path = "."
      if (ty == "d") {
        if (path in hasfile) print "2D\t" path
      } else if (path in tracked) {
        print "1F\t" path
      }
    }
  ' "$tmp/tracked" "$tmp/found" > "$tmp/lines" 2>/dev/null; then
    reason="awk failed"
    return 1
  fi
  sort "$tmp/lines" | cut -c2- > "$tmp/body"
  nfiles=$(grep -c '^F' "$tmp/body")
  ndirs=$(grep -c '^D' "$tmp/body")
  return 0
}

# write_trace: write the TSV to $out, through a temp file.
write_trace() {
  {
    printf '# smarter-trace v1\n'
    printf 'job\t%s\n' "$job"
    printf 'node\t%s\n' "$node"
    printf 'method\tatime\n'
    if [ "$supported" = true ]; then
      printf 'supported\ttrue\n'
      cat "$tmp/body"
    else
      printf 'supported\tfalse\n'
      printf 'reason\t%s\n' "$(one_line "$reason")"
    fi
  } > "$out.tmp" && mv -f "$out.tmp" "$out"
}

run_collect() {
  job=$(one_line "${CIRCLE_JOB:-}")
  node=${CIRCLE_NODE_INDEX:-0}
  case $node in '' | *[!0-9]*) node=0 ;; esac
  dest=${SMARTER_TRACE_DIR:-/tmp/smarter/traces}
  case $dest in /*) ;; *) dest=$(pwd)/$dest ;; esac
  if ! mkdir -p "$dest" 2>/dev/null; then
    say "cannot create the trace directory, trace not written"
    return 0
  fi
  hash=$(sha16 "${CIRCLE_JOB:-}")
  if [ -z "$hash" ]; then
    say "no sha256 tool found, trace not written"
    return 0
  fi
  out=$dest/$hash.$node.tsv

  supported=false
  if take_trace; then
    supported=true
  fi
  if ! write_trace; then
    rm -f "$out.tmp"
    cleanup
    say "cannot write the trace file, trace not written"
    return 0
  fi
  cleanup
  name=${out##*/}
  if [ "$supported" = true ]; then
    say "trace $name written ($nfiles files read, $ndirs dirs listed)"
  else
    say "trace $name written (not supported here: $(one_line "$reason"))"
  fi
  return 0
}

run_collect
exit 0
