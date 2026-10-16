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

# The agctl client takes its version from this same VERSION file. Keep its
# distribution channels linked: the release builder must emit version-stamped
# client archives, and the module install path documented for users must be the
# path go.mod declares. The packaged client's runtime version is verified by
# scripts/build-release-artifacts-test.sh.
test -d cmd/agctl
grep -Fq 'artifact-gateway-agctl_${version}_' scripts/build-release-artifacts.sh
cli_module=$(awk '/^module / { print $2; exit }' go.mod)
# go install resolves the module path as a repository, so it must be the GitHub
# repository that serves the tags (ADR 0008). CI names it; a checkout falls back
# to its origin remote and skips the check when neither is known.
source_repository=${GITHUB_REPOSITORY:-}
if [[ -z "$source_repository" ]]; then
  origin_url=$(git remote get-url origin 2>/dev/null || true)
  if [[ "$origin_url" =~ github\.com[:/]([^/]+/[^/]+)$ ]]; then
    source_repository=${BASH_REMATCH[1]%.git}
  fi
fi
if [[ -n "$source_repository" && "$cli_module" != "github.com/$source_repository" ]]; then
  printf 'go.mod module %s does not match the source repository github.com/%s.\n' "$cli_module" "$source_repository" >&2
  exit 1
fi
for doc in docs/cli.md docs/cli.zh-CN.md; do
  if ! grep -Fq "go install $cli_module/cmd/agctl@" "$doc"; then
    printf '%s must document the module install path %s/cmd/agctl.\n' "$doc" "$cli_module" >&2
    exit 1
  fi
done

release_heading=$(grep -E "^## $version - [0-9]{4}-[0-9]{2}-[0-9]{2}$" CHANGELOG.md | head -n 1)
test -n "$release_heading"
grep -Fxq "$release_heading" CHANGELOG.zh-CN.md
test -s ".github/release-notes/v$version.md"
grep -Fq "# Artifact Gateway v$version" ".github/release-notes/v$version.md"

printf 'Release version check passed: %s.\n' "$version"
