#!/bin/sh
# Lints the source of the demo: Go formatting and vet, and the whitespace rules
# of the JavaScript, Python and JSON files. Run it from examples/demo.
set -eu

status=0
fail() {
  echo "lint: $*" >&2
  status=1
}

# Go: formatted, and vet is quiet.
unformatted=$(gofmt -l services/api)
if [ -n "$unformatted" ]; then
  fail "gofmt would change: $unformatted"
fi
(cd services/api && go vet ./...) || fail "go vet failed"

# JavaScript, Python and JSON: no tabs, no trailing blanks, a final newline.
files=$(find services libs -type f \( -name '*.js' -o -name '*.py' -o -name '*.json' \) ! -path '*/node_modules/*' | sort)
for f in $files; do
  if grep -n "$(printf '\t')" "$f" > /dev/null; then
    fail "$f: has a tab"
  fi
  if grep -n ' $' "$f" > /dev/null; then
    fail "$f: has trailing blanks"
  fi
  if [ -s "$f" ] && [ "$(tail -c 1 "$f" | wc -l)" -eq 0 ]; then
    fail "$f: no newline at the end"
  fi
done

if [ "$status" -eq 0 ]; then
  echo "lint: ok ($(echo "$files" | wc -l | tr -d ' ') files, and the Go package)"
fi
exit "$status"
