#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

ARTIFACT_GATEWAY_GO_CLI_E2E=1 \
go test ./internal/app \
	  -run '^TestNativeGoRealClientDownloads(ThroughProxyAndOfflineCache|HostedPublication)$' \
  -count=1 -v
