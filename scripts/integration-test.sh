#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

compose=(docker compose --env-file /dev/null --project-name artifact-gateway-integration -f compose.integration.yml)
cleanup() {
  "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker volume create artifact-gateway-go-mod >/dev/null
docker volume create artifact-gateway-go-build >/dev/null
cleanup
"${compose[@]}" up -d --wait postgres rustfs
"${compose[@]}" run --rm --no-deps rustfs-ready
"${compose[@]}" run --rm --no-deps migrate
./scripts/migration-runner-check.sh
./scripts/apt-lifecycle-upgrade-check.sh
"${compose[@]}" run --rm --no-deps test
