#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

minimum=${GATEWAY_MIN_COVERAGE:-40.0}
profile=$(mktemp)
trap 'rm -f "$profile"' EXIT
packages=$(go list ./... | grep -v '/console/node_modules/')

go test $packages -coverprofile="$profile" >/dev/null
actual=$(go tool cover -func="$profile" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')

if ! awk -v actual="$actual" -v minimum="$minimum" 'BEGIN { exit !(actual + 0 >= minimum + 0) }'; then
  printf 'Go coverage %.1f%% is below the required %.1f%%.\n' "$actual" "$minimum" >&2
  exit 1
fi

printf 'Go coverage gate passed: %.1f%% >= %.1f%%.\n' "$actual" "$minimum"

while read -r package package_minimum; do
  [[ -n "${package:-}" && "${package:0:1}" != "#" ]] || continue
  output=$(go test "$package" -cover)
  package_actual=$(printf '%s\n' "$output" | sed -nE 's/.*coverage: ([0-9.]+)% of statements.*/\1/p' | tail -1)
  if [[ -z "$package_actual" ]]; then
    printf 'Could not read coverage for %s.\n' "$package" >&2
    exit 1
  fi
  if ! awk -v actual="$package_actual" -v minimum="$package_minimum" 'BEGIN { exit !(actual + 0 >= minimum + 0) }'; then
    printf '%s coverage %.1f%% is below the required %.1f%%.\n' "$package" "$package_actual" "$package_minimum" >&2
    exit 1
  fi
  printf '%s coverage gate passed: %.1f%% >= %.1f%%.\n' "$package" "$package_actual" "$package_minimum"
done < scripts/coverage-packages.txt
