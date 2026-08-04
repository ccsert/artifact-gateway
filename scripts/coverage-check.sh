#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

minimum=${GATEWAY_MIN_COVERAGE:-37.0}
profile=$(mktemp)
trap 'rm -f "$profile"' EXIT

go test ./... -coverprofile="$profile" >/dev/null
actual=$(go tool cover -func="$profile" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')

if ! awk -v actual="$actual" -v minimum="$minimum" 'BEGIN { exit !(actual + 0 >= minimum + 0) }'; then
  printf 'Go coverage %.1f%% is below the required %.1f%%.\n' "$actual" "$minimum" >&2
  exit 1
fi

printf 'Go coverage gate passed: %.1f%% >= %.1f%%.\n' "$actual" "$minimum"
