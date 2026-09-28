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
./scripts/member-role-upgrade-check.sh
"${compose[@]}" run --rm --no-deps test

# The Go test container has no Cargo binary. Run the pinned official client on
# the host against the same migrated PostgreSQL and RustFS services.
postgres_address=$("${compose[@]}" port postgres 5432)
rustfs_address=$("${compose[@]}" port rustfs 9000)
TEST_DATABASE_URL="postgres://gateway:integration-password@${postgres_address}/gateway_test?sslmode=disable" \
TEST_RUSTFS_ENDPOINT="http://${rustfs_address}" \
TEST_RUSTFS_ACCESS_KEY=integration-rustfs \
TEST_RUSTFS_SECRET_KEY=integration-password \
CARGO_REQUIRED=1 go test -count=1 -tags=integration ./internal/app -run '^TestPostgresRustFSCargoProxyOfficialOfflineReplay$'
