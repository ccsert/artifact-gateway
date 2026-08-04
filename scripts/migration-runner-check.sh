#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
compose=(docker compose -f "$root/compose.integration.yml")

second_output=$(cd "$root" && "${compose[@]}" run --rm --no-deps migrate 2>&1)
expected_skips=$(find "$root/migrations" -maxdepth 1 -type f -name '*.sql' | wc -l | tr -d ' ')
actual_skips=$(printf '%s\n' "$second_output" | grep -c '^Skipping applied migration ' || true)
if [[ "$actual_skips" -ne "$expected_skips" ]] || printf '%s\n' "$second_output" | grep -q '^Applying migration '; then
  printf '%s\n' "$second_output" >&2
  printf 'Migration no-op check failed: expected %s skips, observed %s.\n' "$expected_skips" "$actual_skips" >&2
  exit 1
fi
printf 'Migration no-op check passed: %s applied files were skipped.\n' "$actual_skips"

probe_dir=$(mktemp -d)
trap 'rm -rf "$probe_dir"' EXIT
latest_migration=$(find "$root/migrations" -maxdepth 1 -type f -name '*.sql' | sort | tail -n 1)
probe_name=${latest_migration##*/}
cp "$latest_migration" "$probe_dir/$probe_name"
printf '\n-- checksum drift probe\n' >> "$probe_dir/$probe_name"

set +e
drift_output=$(cd "$root" && "${compose[@]}" run --rm --no-deps --volume "$probe_dir:/migrations:ro" migrate 2>&1)
drift_status=$?
set -e
if [[ "$drift_status" -eq 0 ]] || ! printf '%s\n' "$drift_output" | grep -Fq "Applied migration $probe_name has changed"; then
  printf '%s\n' "$drift_output" >&2
  printf 'Migration checksum-drift check failed for %s.\n' "$probe_name" >&2
  exit 1
fi
printf 'Migration checksum-drift check passed for %s.\n' "$probe_name"
