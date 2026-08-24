#!/usr/bin/env bash
set -euo pipefail

output=${1:-}
if [[ -z "$output" || ! -d "$output" ]]; then
  printf '%s\n' 'Usage: write-release-checksums.sh OUTPUT_DIR' >&2
  exit 2
fi

output=$(cd "$output" && pwd)
(
  cd "$output"
  : > SHA256SUMS
  find . -maxdepth 1 -type f \( -name '*.tar.gz' -o -name '*.zip' \) -print |
    LC_ALL=C sort |
    while IFS= read -r artifact; do
      artifact=${artifact#./}
      if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$artifact" >> SHA256SUMS
      else
        shasum -a 256 "$artifact" >> SHA256SUMS
      fi
    done
)
