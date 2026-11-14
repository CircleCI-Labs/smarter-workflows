#!/bin/sh
# Looks for secrets that were committed to the demo. Run it from the root of
# the repository.
set -eu

pattern='AKIA[0-9A-Z]{16}|-----BEGIN ([A-Z]+ )?PRIVATE KEY-----|(secret|token|passwd|password)[a-z_]*[[:space:]]*[:=][[:space:]]*["'"'"'][^"'"'"']{8,}["'"'"']'

if git grep -n -I -i -E -e "$pattern" -- examples/demo ':!examples/demo/scripts/security-scan.sh'; then
  echo "security-scan: possible secrets above" >&2
  exit 1
fi
echo "security-scan: no secrets found in examples/demo"
