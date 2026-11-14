#!/bin/sh
# Checks the Terraform files in infra/ without terraform: braces are balanced,
# a required_version is set, and nothing opens a port to the whole internet.
# Run it from examples/demo.
set -eu

status=0
count=0
for tf in infra/*.tf; do
  count=$((count + 1))
  opens=$(grep -o '{' "$tf" | wc -l)
  closes=$(grep -o '}' "$tf" | wc -l)
  if [ "$opens" -ne "$closes" ]; then
    echo "check-infra: $tf: $opens '{' but $closes '}'" >&2
    status=1
  fi
  if grep -n '0\.0\.0\.0/0' "$tf" > /dev/null; then
    echo "check-infra: $tf: opens a port to 0.0.0.0/0" >&2
    status=1
  fi
done

if ! cat infra/*.tf | grep -q 'required_version'; then
  echo "check-infra: no terraform required_version in infra/" >&2
  status=1
fi

if [ "$status" -eq 0 ]; then
  echo "check-infra: $count files ok"
fi
exit "$status"
