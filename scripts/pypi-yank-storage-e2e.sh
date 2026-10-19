#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
temporary=$(mktemp -d)
project="ag-pypi-yank-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:12])')"
compose=(docker compose --env-file /dev/null --project-name "$project" -f compose.integration.yml)
cleanup() {
  status=$?
  "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  for kind in container volume network; do
    if [[ "$kind" == container ]]; then
      remaining=$(docker ps -aq --filter "label=com.docker.compose.project=$project")
    else
      remaining=$(docker "$kind" ls -q --filter "label=com.docker.compose.project=$project")
    fi
    if [[ -n "$remaining" ]]; then printf 'Owned %s resources remain for %s\n' "$kind" "$project" >&2; status=1; fi
  done
  rm -rf -- "$temporary"
  exit "$status"
}
trap cleanup EXIT
"${compose[@]}" up -d --wait postgres rustfs
"${compose[@]}" run --rm --no-deps rustfs-ready
"${compose[@]}" run --rm --no-deps migrate
postgres_address=$("${compose[@]}" port postgres 5432)
rustfs_address=$("${compose[@]}" port rustfs 9000)
export TEST_DATABASE_URL="postgres://gateway:integration-password@${postgres_address}/gateway_test?sslmode=disable"
export TEST_RUSTFS_ENDPOINT="http://${rustfs_address}" TEST_RUSTFS_ACCESS_KEY=integration-rustfs TEST_RUSTFS_SECRET_KEY=integration-password
ARTIFACT_GATEWAY_PYPI_BACKUP_MANIFEST="$temporary/manifest.json" \
  go test -v -count=1 -p=1 -tags=integration ./internal/repository ./internal/app \
  -run '^TestPyPIYankPostgres|^TestPostgresRustFSPyPIPublication|^TestPyPIYankFile|^TestPyPIYankRevision|^TestPyPIYankAuthorization'
postgres_container=$("${compose[@]}" ps -q postgres)
rustfs_container=$("${compose[@]}" ps -q rustfs)
postgres_started=$(docker inspect --format '{{.State.StartedAt}}' "$postgres_container")
rustfs_started=$(docker inspect --format '{{.State.StartedAt}}' "$rustfs_container")
"${compose[@]}" restart --no-deps postgres rustfs
"${compose[@]}" up -d --no-deps --wait postgres
"${compose[@]}" run --rm --no-deps rustfs-ready
test "$("${compose[@]}" ps -q postgres)" = "$postgres_container"
test "$("${compose[@]}" ps -q rustfs)" = "$rustfs_container"
test "$("${compose[@]}" port postgres 5432)" = "$postgres_address"
test "$("${compose[@]}" port rustfs 9000)" = "$rustfs_address"
postgres_restarted=$(docker inspect --format '{{.State.StartedAt}}' "$postgres_container")
rustfs_restarted=$(docker inspect --format '{{.State.StartedAt}}' "$rustfs_container")
test "$postgres_restarted" != "$postgres_started"
test "$rustfs_restarted" != "$rustfs_started"
printf 'Owned service restart confirmed: PostgreSQL=%s RustFS=%s; container identities and ports retained.\n' "$postgres_container" "$rustfs_container"
printf 'PostgreSQL StartedAt: %s -> %s on %s\n' "$postgres_started" "$postgres_restarted" "$postgres_address"
printf 'RustFS StartedAt: %s -> %s on %s\n' "$rustfs_started" "$rustfs_restarted" "$rustfs_address"
ARTIFACT_GATEWAY_PYPI_RESTART_MANIFEST="$temporary/manifest.json" \
  go test -v -count=1 -tags=integration ./internal/app -run '^TestPyPIYankRestartedServicesPreserveState$'
"${compose[@]}" exec -T postgres pg_dump -U gateway -d gateway_test -Fc --no-owner >"$temporary/metadata.dump"
"${compose[@]}" exec -T postgres createdb -U gateway -T template0 gateway_yank_restore_test
"${compose[@]}" exec -T postgres pg_restore -U gateway -d gateway_yank_restore_test --no-owner <"$temporary/metadata.dump"
TEST_DATABASE_URL="postgres://gateway:integration-password@${postgres_address}/gateway_yank_restore_test?sslmode=disable" \
ARTIFACT_GATEWAY_PYPI_RESTORE_MANIFEST="$temporary/manifest.json" \
  go test -v -count=1 -tags=integration ./internal/app -run '^TestPyPIYankRestoredDatabaseAndIndependentObjects$'
printf '%s\n' 'PyPI yank: PostgreSQL transactions, Gateway reconstruction, PostgreSQL/RustFS service restart, distribution and isolated pg_dump restore verified.'
