#!/usr/bin/env bash
set -euo pipefail

environment_file=${GATEWAY_ENV_FILE:-.env}
backup_dir=${1:?usage: scripts/restore-drill.sh <backup-directory>}
compose=(docker compose --env-file "$environment_file" -f compose.yml)
# shellcheck disable=SC1091
source "$environment_file"
shasum -a 256 --check "$backup_dir/SHA256SUMS"

"${compose[@]}" stop gateway
"${compose[@]}" exec -T postgres \
  pg_restore -U gateway -d gateway --clean --if-exists <"$backup_dir/gateway.dump"
minio_container=$("${compose[@]}" ps -q minio)
test -n "$minio_container"
"${compose[@]}" exec -T minio sh -ec 'rm -rf /data/*'
docker cp - "$minio_container:/data" <"$backup_dir/minio-data.tar"
"${compose[@]}" start gateway
gateway_url="http://localhost:${GATEWAY_HTTP_PORT:-8080}"
ready_status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' "$gateway_url/readyz")
[[ "$ready_status" == 204 ]] || { printf 'Restored Gateway readiness returned HTTP %s.\n' "$ready_status" >&2; exit 1; }
printf 'Restore completed from: %s\n' "$backup_dir"
