#!/usr/bin/env sh
set -eu

spec=api/openapi/native-hosted-v1.json
base_sha=${GITHUB_BASE_SHA:-}

if [ -z "$base_sha" ]; then
  echo "No API baseline supplied; skipping compatibility comparison."
  exit 0
fi

if ! git cat-file -e "$base_sha:$spec" 2>/dev/null; then
  echo "API contract is new relative to $base_sha; no compatibility baseline."
  exit 0
fi

baseline=$(mktemp)
trap 'rm -f "$baseline"' EXIT
git show "$base_sha:$spec" > "$baseline"
API_BASELINE="$baseline" go test ./contracts -run '^TestNativeHostedAPICompatibility$'
