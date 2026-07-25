#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

doc=docs/release-readiness.md
test -f "$doc"

targets_file=$(mktemp)
trap 'rm -f "$targets_file"' EXIT
awk '
  /^```sh$/ { in_block = 1; next }
  in_block && /^```$/ { exit }
  in_block && /^make / { print $2 }
' "$doc" > "$targets_file"

target_count=$(wc -l < "$targets_file" | tr -d ' ')
if [[ "$target_count" -eq 0 ]]; then
  printf '%s\n' "No release readiness commands found in $doc." >&2
  exit 1
fi

help_output=$(make help)
missing=0

while IFS= read -r target; do
  if ! grep -Eq "^$target:" Makefile; then
    printf 'Release readiness target %s is documented but missing from Makefile.\n' "$target" >&2
    missing=1
  fi
  if [[ "$help_output" != *"$target"* ]]; then
    printf 'Release readiness target %s is missing from make help.\n' "$target" >&2
    missing=1
  fi
  script=$(awk -v target="$target" '
    $0 == target ":" { in_target = 1; next }
    in_target && /^[^[:space:]].*:/ { exit }
    in_target && /@\.\/scripts\/.*\.sh/ {
      line = $0
      sub(/^.*@/, "", line)
      sub(/[[:space:]].*$/, "", line)
      print line
      exit
    }
  ' Makefile)
  if [[ -n "$script" && ! -x "$script" ]]; then
    printf 'Release readiness target %s invokes non-executable script %s.\n' "$target" "$script" >&2
    missing=1
  fi
done < "$targets_file"

if [[ "$missing" -ne 0 ]]; then
  exit 1
fi

printf 'Release readiness check passed: %d documented targets are available.\n' "$target_count"
