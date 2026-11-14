#!/bin/sh
# Starts the api binary and calls it over HTTP. Usage: scripts/e2e.sh [BINARY]
set -eu

bin=${1:-/tmp/workspace/api}
addr=127.0.0.1:18080

"$bin" -version
"$bin" -addr "$addr" &
pid=$!
trap 'kill "$pid" 2> /dev/null || true' EXIT

tries=0
until curl -fsS "http://$addr/healthz" > /dev/null 2>&1; do
  tries=$((tries + 1))
  if [ "$tries" -gt 50 ]; then
    echo "e2e: the api did not come up on $addr" >&2
    exit 1
  fi
  sleep 0.1
done

curl -fsS "http://$addr/healthz" | grep -q '"status":"ok"'
curl -fsS "http://$addr/greeting?name=Ada" | grep -q '"message":"Hello, Ada!"'
echo "e2e: ok"
