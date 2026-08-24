#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

version=$(tr -d '[:space:]' < VERSION)
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'VERSION is not a stable SemVer core: %s\n' "$version" >&2
  exit 1
fi

check_value() {
  local label=$1
  local actual=$2
  if [[ "$actual" != "$version" ]]; then
    printf '%s version is %s, expected %s.\n' "$label" "$actual" "$version" >&2
    exit 1
  fi
}

check_value 'Console package' "$(node -p "require('./console/package.json').version")"
check_value 'Console lockfile' "$(node -p "require('./console/package-lock.json').version")"
check_value 'OpenAPI tools package' "$(node -p "require('./tools/openapi/package.json').version")"
check_value 'OpenAPI tools lockfile' "$(node -p "require('./tools/openapi/package-lock.json').version")"

for contract in api/openapi/native-hosted.yaml api/openapi/management-runtime.yaml api/openapi/management.yaml; do
  contract_version=$(awk '/^info:$/ { in_info = 1; next } in_info && /^  version:/ { print $2; exit }' "$contract")
  check_value "$contract" "$contract_version"
done

grep -Fxq "## $version - 2026-08-24" CHANGELOG.md
grep -Fxq "## $version - 2026-08-24" CHANGELOG.zh-CN.md
test -s ".github/release-notes/v$version.md"
grep -Fq "# Artifact Gateway v$version" ".github/release-notes/v$version.md"

printf 'Release version check passed: %s.\n' "$version"
