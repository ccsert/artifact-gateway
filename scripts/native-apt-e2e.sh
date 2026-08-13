#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

go test ./internal/app \
  -run '^(TestAPTArtifactSearchReturnsEmptyAndCachedAssetPages|TestNativeAPTProxy|TestV2GroupAPTUsesOrderedProxyFallback|TestAPTUpstream)' \
  -count=1 -v

go test ./internal/repository \
  -run '^TestMemoryAPTStore' \
  -count=1 -v

"$root/scripts/native-apt-hosted-e2e.sh"
