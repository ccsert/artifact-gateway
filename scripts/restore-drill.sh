#!/usr/bin/env bash
set -euo pipefail

environment_file=${GATEWAY_ENV_FILE:-.env}
backup_dir=${1:?usage: scripts/restore-drill.sh <backup-directory>}
compose=(docker compose --env-file "$environment_file" -f compose.yml)
# shellcheck disable=SC1091
source "$environment_file"
shasum -a 256 --check "$backup_dir/SHA256SUMS"

"${compose[@]}" stop gateway rustfs
"${compose[@]}" exec -T postgres \
  pg_restore -U gateway -d gateway --clean --if-exists <"$backup_dir/gateway.dump"
rustfs_container=$("${compose[@]}" ps -aq rustfs)
test -n "$rustfs_container"
"${compose[@]}" run --rm --no-deps --entrypoint sh rustfs \
  -ec 'find /data -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +'
docker cp - "$rustfs_container:/data" <"$backup_dir/rustfs-data.tar"
docker start "$rustfs_container" >/dev/null
"${compose[@]}" run --rm --no-deps rustfs-ready
# Starting through Compose re-evaluates the completed migrate dependency on
# some Compose releases. The restored dump already contains its schema, so
# restart only the existing Gateway container.
gateway_container=$("${compose[@]}" ps -aq gateway)
test -n "$gateway_container"
docker start "$gateway_container" >/dev/null
gateway_url="http://localhost:${GATEWAY_HTTP_PORT:-8080}"
ready_status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' "$gateway_url/readyz")
[[ "$ready_status" == 204 ]] || { printf 'Restored Gateway readiness returned HTTP %s.\n' "$ready_status" >&2; exit 1; }
printf 'Restore completed from: %s\n' "$backup_dir"
