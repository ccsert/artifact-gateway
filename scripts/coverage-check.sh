#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

minimum=${GATEWAY_MIN_COVERAGE:-40.0}
profile=$(mktemp)
test_output=$(mktemp)
trap 'rm -f "$profile" "$test_output"' EXIT
packages=$(go list ./... | grep -v '/console/node_modules/')

if ! go test $packages -coverprofile="$profile" >"$test_output" 2>&1; then
  cat "$test_output" >&2
  exit 1
fi
actual=$(go tool cover -func="$profile" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')

if ! awk -v actual="$actual" -v minimum="$minimum" 'BEGIN { exit !(actual + 0 >= minimum + 0) }'; then
  printf 'Go coverage %.1f%% is below the required %.1f%%.\n' "$actual" "$minimum" >&2
  exit 1
fi

printf 'Go coverage gate passed: %.1f%% >= %.1f%%.\n' "$actual" "$minimum"

while read -r package package_minimum; do
  [[ -n "${package:-}" && "${package:0:1}" != "#" ]] || continue
  if ! output=$(go test "$package" -cover 2>&1); then
    printf '%s\n' "$output" >&2
    exit 1
  fi
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
