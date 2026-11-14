#!/bin/sh
# Checks the Markdown in docs/: each page starts with a title, and every
# relative link points to a file that exists. Run it from examples/demo.
set -eu

status=0
count=0
for doc in docs/*.md; do
  count=$((count + 1))
  case $(head -n 1 "$doc") in
    "# "*) ;;
    *)
      echo "check-docs: $doc: the first line must be a '# ' title" >&2
      status=1
      ;;
  esac
  links=$(grep -o '](\([^)]*\))' "$doc" | sed 's/^](//; s/)$//' || true)
  for link in $links; do
    case $link in
      http://* | https://* | mailto:* | '#'*) continue ;;
    esac
    target=${link%%#*}
    if [ ! -e "docs/$target" ]; then
      echo "check-docs: $doc: broken link to $link" >&2
      status=1
    fi
  done
done

if [ "$status" -eq 0 ]; then
  echo "check-docs: $count pages ok"
fi
exit "$status"
