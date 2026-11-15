#!/bin/sh
# test-trace.sh: tests for the two trace scripts in internal/tracescript/
# (reset-atime.sh and collect-trace.sh). It builds temp git repos, runs the
# reset, simulates what a job reads, runs the collector and compares the exact
# trace TSV.
#
#   scripts/test-trace.sh
#   REQUIRE_ATIME=1 scripts/test-trace.sh   fail, do not skip, when the
#                                           filesystem has no usable atime
#   SMARTER_TEST_TMP=DIR                    where the temp repos go
#                                           (default: $TMPDIR, then /tmp)
#   SMARTER_TEST_SH="sh -e"                 the shell that runs the two scripts
#                                           (default: sh)
#
# Needs git, GNU find and GNU touch, like the scripts themselves. Exit status:
# 0 when nothing failed, 1 otherwise.

# SC2016: single quotes are meant here (sh -c bodies, generated shim scripts).
# shellcheck disable=SC2016
set -u

pass=0
fail=0
skip=0

ok() {
  pass=$((pass + 1))
  printf 'ok - %s\n' "$1"
}
not_ok() {
  fail=$((fail + 1))
  printf 'not ok - %s\n' "$1"
}
skipped() {
  skip=$((skip + 1))
  printf 'skip - %s\n' "$1"
}
# assert NAME COMMAND...: ok when COMMAND succeeds.
assert() {
  assert_name=$1
  shift
  if "$@"; then ok "$assert_name"; else not_ok "$assert_name"; fi
}

here=$(cd "$(dirname "$0")" && pwd) || exit 2
root=$(dirname "$here")
reset_sh=$root/internal/tracescript/reset-atime.sh
collect_sh=$root/internal/tracescript/collect-trace.sh
runner=${SMARTER_TEST_SH:-sh}
for f in "$reset_sh" "$collect_sh"; do
  if [ ! -f "$f" ]; then
    echo "missing $f" >&2
    exit 2
  fi
done

base=${SMARTER_TEST_TMP:-${TMPDIR:-/tmp}}
work=$(mktemp -d "$base/smarter-trace-test.XXXXXX") || exit 2
# The shim commands (below) live apart from $work, which may be mounted noexec.
shims=$(mktemp -d "${TMPDIR:-/tmp}/smarter-trace-shims.XXXXXX") || exit 2
trap 'rm -rf "$work" "$shims"' EXIT
trap 'exit 1' HUP INT TERM

# A clean slate: no user git config, and none of the variables the scripts read
# (CI sets them for the job that runs this test).
HOME=$work/home
mkdir -p "$HOME"
export HOME
unset XDG_CONFIG_HOME GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
unset CIRCLE_JOB CIRCLE_NODE_INDEX CIRCLE_WORKING_DIRECTORY SMARTER_TRACE_DIR
GIT_CONFIG_NOSYSTEM=1
GIT_AUTHOR_NAME="Test Author"
GIT_AUTHOR_EMAIL=test@example.invalid
GIT_COMMITTER_NAME="Test Author"
GIT_COMMITTER_EMAIL=test@example.invalid
export GIT_CONFIG_NOSYSTEM GIT_AUTHOR_NAME GIT_AUTHOR_EMAIL GIT_COMMITTER_NAME GIT_COMMITTER_EMAIL
# Git's automatic maintenance starts detached after a commit (and after a fetch
# or a push) and writes into the repo after git returns, so the removal of a
# temp repo can race with it. Off here, as in internal/diff/difftest.
export GIT_CONFIG_COUNT=3
export GIT_CONFIG_KEY_0=maintenance.auto GIT_CONFIG_VALUE_0=false
export GIT_CONFIG_KEY_1=gc.auto GIT_CONFIG_VALUE_1=0
export GIT_CONFIG_KEY_2=gc.autoDetach GIT_CONFIG_VALUE_2=false

fstype=$(stat -f -c %T "$work" 2>/dev/null || echo unknown)
echo "# $(uname -sr), $fstype filesystem, git $(git --version | sed 's/^git version //')"

# --- helpers ---------------------------------------------------------------

tab=$(printf '\t')

# hdr JOB NODE true|false [REASON]: the header of an expected trace.
hdr() {
  printf '# smarter-trace v1\njob\t%s\nnode\t%s\nmethod\tatime\nsupported\t%s\n' "$1" "$2" "$3"
  if [ "$3" = false ]; then printf 'reason\t%s\n' "$4"; fi
}
F() { printf 'F\t%s\n' "$1"; }
D() { printf 'D\t%s\n' "$1"; }

# new_checkout NAME: clone the origin the way a CI checkout does (clone, then
# reset --hard, which reads every file). Sets repo, and tdir for its traces.
new_checkout() {
  repo=$work/$1/repo
  tdir=$work/$1/traces
  mkdir -p "$work/$1"
  git clone -q "$origin" "$repo" && git -C "$repo" reset -q --hard
}

# run_script SCRIPT DIR [VAR=VALUE]...: run SCRIPT with the test shell in DIR.
# Output goes to $work/out and the status to rc.
run_script() {
  rs_script=$1
  rs_dir=$2
  shift 2
  # shellcheck disable=SC2086
  (cd "$rs_dir" && env "$@" $runner "$rs_script") > "$work/out" 2>&1
  rc=$?
}
do_reset() { run_script "$reset_sh" "$@"; }
# do_collect DIR [VAR=VALUE]...: collect with job "test" and node 0 by default.
do_collect() {
  dc_dir=$1
  shift
  run_script "$collect_sh" "$dc_dir" CIRCLE_JOB=test CIRCLE_NODE_INDEX=0 SMARTER_TRACE_DIR="$tdir" "$@"
}

one_line_ok() { [ "$rc" -eq 0 ] && [ "$(wc -l < "$work/out")" -eq 1 ]; }

# trace_of DIR: the path of the single trace file in DIR.
trace_of() {
  set -- "$1"/*.tsv
  [ -f "$1" ] && [ "$#" -eq 1 ] && printf '%s' "$1"
}

# expect NAME: compare the trace in $tdir with the expected one on stdin.
expect() {
  ex_name=$1
  cat > "$work/expected"
  ex_file=$(trace_of "$tdir")
  if [ -z "$ex_file" ]; then
    not_ok "$ex_name (no trace file in $tdir)"
    return
  fi
  if diff -u "$work/expected" "$ex_file" > "$work/diff.out" 2>&1; then
    ok "$ex_name"
  else
    not_ok "$ex_name"
    sed 's/^/    /' "$work/diff.out"
  fi
}

# Both scripts must exit 0 with one line of output, and never print env values.
scripts_quiet() { one_line_ok && ! grep -q -e CIRCLE_ -e SMARTER_ "$work/out"; }

# --- the origin repo -------------------------------------------------------

origin=$work/origin
mkdir -p "$origin"
(
  cd "$origin" || exit 1
  git init -q . && git symbolic-ref HEAD refs/heads/main || exit 1
  mkdir -p src/util docs/img scripts "with space"
  printf '*.log\n' > .gitignore
  printf 'readme\n' > README.md
  printf 'main\n' > src/main.go
  printf 'helper\n' > src/util/helper.go
  printf 'guide\n' > docs/guide.md
  printf 'logo\n' > docs/img/logo.txt
  printf 'build\n' > scripts/build.sh
  printf 'space\n' > "with space/file name.txt"
  git add -A && git commit -q -m origin
) || { echo "cannot build the origin repo" >&2; exit 2; }

# --- 1. does this filesystem do atime? --------------------------------------

new_checkout idle || { echo "cannot clone the origin repo" >&2; exit 2; }
idle_repo=$repo
premise=$(cd "$repo" && find . -path ./.git -prune -o -type f -printf '%A@ %T@\n' 2>/dev/null |
  awk '$1 > $2 { n++ } END { print n + 0 }')
do_reset "$repo"
assert "reset: exits 0 with one line" one_line_ok
if [ -f "$repo/.git/smarter-trace-ready" ]; then
  atime_ok=1
  if [ "$premise" -gt 0 ]; then
    echo "# the checkout read $premise files, the reset undoes that"
  else
    echo "# note: the checkout read no file here, so the reset had nothing to undo"
  fi
else
  atime_ok=0
  why=$(cat "$repo/.git/smarter-trace-unsupported" 2>/dev/null || echo "no marker written")
  if [ "${REQUIRE_ATIME:-0}" = 1 ]; then
    not_ok "atime works on this filesystem ($why)"
  else
    echo "# atime does not work here ($why): the atime tests are skipped"
  fi
fi

# --- 2. unsupported and missing-checkout paths (no atime needed) ------------

# A foreign find (no -printf) and a foreign touch (no -d @EPOCH), like busybox or
# BSD ones: two directories of one-command shims to put first in PATH.
real_find=$(command -v find)
real_touch=$(command -v touch)
mkdir -p "$shims/find" "$shims/touch"
{
  printf '#!/bin/sh\nfor a in "$@"; do\n'
  printf '  case $a in -printf) echo "find: unknown predicate -printf" >&2; exit 1 ;; esac\n'
  printf 'done\nexec %s "$@"\n' "$real_find"
} > "$shims/find/find"
{
  printf '#!/bin/sh\nfor a in "$@"; do\n'
  printf '  case $a in @*) echo "touch: invalid date format" >&2; exit 1 ;; esac\n'
  printf 'done\nexec %s "$@"\n' "$real_touch"
} > "$shims/touch/touch"
chmod +x "$shims/find/find" "$shims/touch/touch"
if "$shims/find/find" . -maxdepth 0 > /dev/null 2>&1 && "$shims/touch/touch" --version > /dev/null 2>&1; then
  shims_ok=1
else
  shims_ok=0
fi

if [ "$shims_ok" = 1 ]; then
  new_checkout foreign-touch
  do_reset "$repo" PATH="$shims/touch:$PATH"
  assert "foreign touch: reset exits 0 with one line" one_line_ok
  assert "foreign touch: unsupported marker, no ready marker" sh -c \
    '[ -f "$1/.git/smarter-trace-unsupported" ] && [ ! -f "$1/.git/smarter-trace-ready" ]' sh "$repo"
  do_collect "$repo"
  assert "foreign touch: collector exits 0 with one line" one_line_ok
  expect "foreign touch: unsupported trace says why" << EOF
$(hdr test 0 false "touch does not support -d @0 (GNU touch is needed)")
EOF

  # The same under sh -e: the reset must never fail the job.
  saved_runner=$runner
  runner="sh -e"
  do_reset "$repo" PATH="$shims/touch:$PATH"
  runner=$saved_runner
  assert "foreign touch: reset survives sh -e" one_line_ok

  new_checkout foreign-find
  do_reset "$repo" PATH="$shims/find:$PATH"
  foreign_find_reason="find does not support -printf (GNU find is needed)"
  assert "foreign find: unsupported marker" grep -q -F "$foreign_find_reason" "$repo/.git/smarter-trace-unsupported"
  do_collect "$repo" PATH="$shims/find:$PATH"
  expect "foreign find: unsupported trace says why" << EOF
$(hdr test 0 false "$foreign_find_reason")
EOF
else
  skipped "foreign find and touch (the shim directory cannot run programs)"
fi

new_checkout no-reset
do_collect "$repo"
assert "no reset: collector exits 0 with one line" scripts_quiet
expect "no reset: unsupported trace" << EOF
$(hdr test 0 false "the atime reset step did not run")
EOF
# The same under sh -e: the scripts must never fail the job.
rm -rf "$tdir"
saved_runner=$runner
runner="sh -e"
do_collect "$repo"
runner=$saved_runner
assert "no reset: collector survives sh -e" one_line_ok

new_checkout marker
printf 'a reason from the reset\n' > "$repo/.git/smarter-trace-unsupported"
do_collect "$repo"
expect "unsupported marker: its reason is in the trace" << EOF
$(hdr test 0 false "a reason from the reset")
EOF

mkdir -p "$work/empty" "$work/empty-traces"
tdir=$work/empty-traces
do_reset "$work/empty"
assert "no checkout: reset exits 0 with one line" one_line_ok
do_collect "$work/empty"
expect "no checkout: unsupported trace" << EOF
$(hdr test 0 false "no git checkout in the working directory")
EOF
rm -rf "$tdir"
do_collect "$work/empty" CIRCLE_WORKING_DIRECTORY="$work/does-not-exist"
expect "missing working directory: unsupported trace" << EOF
$(hdr test 0 false "the working directory does not exist")
EOF

# The unsupported trace of the real unsupported filesystem, when this is one.
if [ "$atime_ok" = 0 ] && [ -f "$idle_repo/.git/smarter-trace-unsupported" ]; then
  tdir=$work/idle/traces
  do_collect "$idle_repo"
  assert "this filesystem: the trace says supported false" grep -q "^supported${tab}false\$" "$(trace_of "$tdir")"
fi

if [ "$atime_ok" = 0 ]; then
  skipped "atime tests (atime does not work on this filesystem)"
  echo "# $pass passed, $fail failed, $skip skipped"
  [ "$fail" -eq 0 ]
  exit
fi

# --- 3. the atime tests ------------------------------------------------------

# 3a. A job that reads nothing has an empty trace; the reset only moved atimes.
repo=$idle_repo
tdir=$work/idle/traces
assert "reset: ready marker says ok" grep -q '^ok$' "$repo/.git/smarter-trace-ready"
assert "reset: git no longer trusts ctime" sh -c '[ "$(git -C "$1" config core.trustctime)" = false ]' sh "$repo"
do_collect "$repo"
assert "collector: exits 0 with one line, no env names" scripts_quiet
expect "idle job: nothing read" << EOF
$(hdr test 0 true)
EOF
assert "collector: file name is the sha256 of the job and the node" test -f "$tdir/9f86d081884c7d65.0.tsv"

# 3b. mtimes are untouched by the reset.
new_checkout mtimes
(cd "$repo" && find . -path ./.git -prune -o -printf '%T@ %p\n' | sort) > "$work/mtimes.before"
do_reset "$repo"
(cd "$repo" && find . -path ./.git -prune -o -printf '%T@ %p\n' | sort) > "$work/mtimes.after"
assert "reset: leaves every mtime alone" cmp -s "$work/mtimes.before" "$work/mtimes.after"

# 3c. Reads and listings, tracked and untracked. A canary variable in the
# environment must not show up anywhere.
new_checkout reads
do_reset "$repo" FAKE_SECRET_TOKEN=canary-4f9c2e
assert "reset: prints no environment values" sh -c '! grep -q canary-4f9c2e "$1"' sh "$work/out"
(
  cd "$repo" || exit 1
  cat README.md "with space/file name.txt" > /dev/null
  ls src > /dev/null
  for f in docs/*.md; do :; done
  mkdir untracked
  printf 'u\n' > untracked/u.txt
  cat untracked/u.txt > /dev/null
  ls untracked > /dev/null
)
do_collect "$repo" "CIRCLE_JOB=unit tests" CIRCLE_NODE_INDEX=2 FAKE_SECRET_TOKEN=canary-4f9c2e
assert "reads: collector exits 0 with one line" one_line_ok
assert "reads: collector prints no environment values" sh -c '! grep -q canary-4f9c2e "$1"' sh "$work/out"
assert "reads: the trace holds no environment values" sh -c '! grep -q canary-4f9c2e "$1"' sh "$(trace_of "$tdir")"
{
  hdr "unit tests" 2 true
  F README.md
  F "with space/file name.txt"
  D docs
  D src
} > "$work/expected.reads"
expect "reads: files and directories the job used, nothing else" < "$work/expected.reads"

# 3d. Read then rewritten, listed then changed: still reported.
new_checkout rewrites
do_reset "$repo"
(
  cd "$repo" || exit 1
  cat README.md > /dev/null
  printf 'more\n' >> README.md
  ls src docs/img > /dev/null
  : > src/new.txt
  rm docs/img/logo.txt
  ls > /dev/null
  mkdir dist
)
do_collect "$repo"
expect "rewrites: read then written, listed then changed" << EOF
$(hdr test 0 true)
$(F README.md)
$(D .)
$(D docs/img)
$(D src)
EOF

# 3e. git status and git diff read only .gitignore, and list every directory.
new_checkout git
do_reset "$repo"
(cd "$repo" && git status --porcelain > /dev/null && git diff > /dev/null)
do_collect "$repo"
expect "git status and git diff: only .gitignore, every directory" << EOF
$(hdr test 0 true)
$(F .gitignore)
$(D .)
$(D docs)
$(D docs/img)
$(D scripts)
$(D src)
$(D src/util)
$(D "with space")
EOF

# 3f. A second checkout reads nothing, and the reset can run again.
new_checkout again
do_reset "$repo"
(cd "$repo" && git fetch -q origin && git checkout -q --force HEAD && git reset -q --hard HEAD)
do_collect "$repo"
expect "second checkout after the reset: nothing read" << EOF
$(hdr test 0 true)
EOF
do_reset "$repo"
assert "reset twice: exits 0 with one line" one_line_ok
assert "reset twice: still ready" test -f "$repo/.git/smarter-trace-ready"
rm -f "$tdir"/*.tsv
do_collect "$repo"
expect "reset twice: nothing read" << EOF
$(hdr test 0 true)
EOF

# 3g. ~ in CIRCLE_WORKING_DIRECTORY, run from elsewhere.
mkdir -p "$work/tilde/home"
git clone -q "$origin" "$work/tilde/home/project" && git -C "$work/tilde/home/project" reset -q --hard
repo=$work/tilde/home/project
tdir=$work/tilde/traces
do_reset / HOME="$work/tilde/home" CIRCLE_WORKING_DIRECTORY='~/project'
assert "working directory ~/project: reset ready" test -f "$repo/.git/smarter-trace-ready"
(cd "$repo" && cat README.md > /dev/null)
do_collect / HOME="$work/tilde/home" CIRCLE_WORKING_DIRECTORY='~/project'
expect "working directory ~/project: trace" << EOF
$(hdr test 0 true)
$(F README.md)
EOF

# 3h. A tracked path with a tab cannot be written to a TSV: the trace says so.
tabrepo=$work/tabrepo
mkdir -p "$tabrepo"
(
  cd "$tabrepo" || exit 1
  git init -q . && git symbolic-ref HEAD refs/heads/main || exit 1
  printf 'x\n' > "a${tab}b.txt"
  printf 'y\n' > plain.txt
  git add -A && git commit -q -m tab
) && git clone -q "$tabrepo" "$work/tab-clone" && git -C "$work/tab-clone" reset -q --hard
repo=$work/tab-clone
tdir=$work/tab-traces
do_reset "$repo"
assert "tab in a tracked path: the reset still works" test -f "$repo/.git/smarter-trace-ready"
do_collect "$repo"
expect "tab in a tracked path: unsupported trace" << EOF
$(hdr test 0 false "a tracked path contains a tab or a line break")
EOF

# 3i. Tracked symlinks, one of them dangling, do not break the reset.
linkrepo=$work/linkrepo
mkdir -p "$linkrepo"
(
  cd "$linkrepo" || exit 1
  git init -q . && git symbolic-ref HEAD refs/heads/main || exit 1
  mkdir src
  printf 'x\n' > src/a.txt
  ln -s src/a.txt link-to-file
  ln -s src link-to-dir
  ln -s missing dangling
  git add -A && git commit -q -m links
) && git clone -q "$linkrepo" "$work/link-clone" && git -C "$work/link-clone" reset -q --hard
repo=$work/link-clone
tdir=$work/link-traces
do_reset "$repo"
assert "symlinks: the reset works" test -f "$repo/.git/smarter-trace-ready"
do_collect "$repo"
expect "symlinks: nothing read after the reset" << EOF
$(hdr test 0 true)
EOF
(cd "$repo" && cat link-to-file > /dev/null)
do_collect "$repo"
assert "symlinks: reading through a link reports the target" grep -q "^F${tab}src/a.txt\$" "$(trace_of "$tdir")"

# 3j. A git worktree has a .git file, not a directory.
git -C "$origin" worktree add -q "$work/wt" -b wt-branch > /dev/null 2>&1
repo=$work/wt
tdir=$work/wt-traces
if [ -f "$repo/.git" ]; then
  do_reset "$repo"
  assert "worktree: reset ready" test -f "$(git -C "$repo" rev-parse --git-dir)/smarter-trace-ready"
  (cd "$repo" && cat README.md > /dev/null && ls src > /dev/null)
  do_collect "$repo"
  expect "worktree: trace" << EOF
$(hdr test 0 true)
$(F README.md)
$(D src)
EOF
else
  skipped "worktree (git worktree add is not available)"
fi

# 3k. A good reset, then a find without -printf (the collector checks again).
if [ "$shims_ok" = 1 ]; then
  new_checkout late-foreign-find
  do_reset "$repo"
  do_collect "$repo" PATH="$shims/find:$PATH"
  expect "ready marker but no find -printf: unsupported trace" << EOF
$(hdr test 0 false "find does not support -printf (GNU find is needed)")
EOF
fi

# 3l. No job name: the hash is the sha256 of the empty string.
new_checkout nojob
do_reset "$repo"
run_script "$collect_sh" "$repo" SMARTER_TRACE_DIR="$tdir"
assert "no CIRCLE_JOB: file name is the sha256 of an empty name" test -f "$tdir/e3b0c44298fc1c14.0.tsv"

# 3m. A job name with shell metacharacters is written as one line in the TSV.
new_checkout hostile-job
do_reset "$repo"
do_collect "$repo" CIRCLE_JOB='a;b|c'
assert "hostile job name: collector exits 0 with one line" one_line_ok
tr=$(trace_of "$tdir")
assert "hostile job name: the job line is in the trace" grep -q "^job${tab}a;b|c\$" "$tr"

echo "# $pass passed, $fail failed, $skip skipped"
[ "$fail" -eq 0 ]
