#!/usr/bin/env bash
set -euo pipefail

environment_file=${GATEWAY_ENV_FILE:-.env}
backup_dir=${1:-.artifacts/backup-drill/$(date -u +%Y%m%dT%H%M%SZ)}
compose=(docker compose --env-file "$environment_file" -f compose.yml)
mkdir -p "$backup_dir"

"${compose[@]}" exec -T postgres \
  pg_dump -U gateway -d gateway --format=custom >"$backup_dir/gateway.dump"

# The MinIO image does not guarantee a tar binary. Docker can stream a tar
# archive directly, keeping the drill independent of utilities in that image.
minio_container=$("${compose[@]}" ps -q minio)
test -n "$minio_container"
docker cp "$minio_container:/data/." - >"$backup_dir/minio-data.tar"

shasum -a 256 "$backup_dir/gateway.dump" "$backup_dir/minio-data.tar" >"$backup_dir/SHA256SUMS"
printf 'Backup created: %s\n' "$backup_dir"
