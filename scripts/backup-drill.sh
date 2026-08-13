#!/usr/bin/env bash
set -euo pipefail

environment_file=${GATEWAY_ENV_FILE:-.env}
backup_dir=${1:-.artifacts/backup-drill/$(date -u +%Y%m%dT%H%M%SZ)}
compose=(docker compose --env-file "$environment_file" -f compose.yml)
mkdir -p "$backup_dir"

rustfs_container=$("${compose[@]}" ps -aq rustfs)
gateway_container=$("${compose[@]}" ps -aq gateway)
test -n "$rustfs_container"
test -n "$gateway_container"
services_stopped=0
resume_services() {
  docker start "$rustfs_container" >/dev/null
  "${compose[@]}" run --rm --no-deps rustfs-ready
  docker start "$gateway_container" >/dev/null
}
cleanup() {
  local status=$?
  if [[ "$services_stopped" == 1 ]]; then
    resume_services || true
  fi
  exit "$status"
}
trap cleanup EXIT

"${compose[@]}" stop gateway
services_stopped=1
"${compose[@]}" exec -T postgres \
  pg_dump -U gateway -d gateway --format=custom >"$backup_dir/gateway.dump"

# Stop the RustFS writer before streaming its volume. This archive is supported
# only for the pinned RustFS baseline used by the running stack.
"${compose[@]}" stop rustfs
docker cp "$rustfs_container:/data/." - >"$backup_dir/rustfs-data.tar"
resume_services
services_stopped=0
trap - EXIT

shasum -a 256 "$backup_dir/gateway.dump" "$backup_dir/rustfs-data.tar" >"$backup_dir/SHA256SUMS"
printf 'Backup created: %s\n' "$backup_dir"
