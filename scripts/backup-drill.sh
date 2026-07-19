#!/usr/bin/env bash
set -euo pipefail

backup_dir=${1:-.artifacts/backup-drill/$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$backup_dir"

docker compose --env-file .env -f compose.yml exec -T postgres \
  pg_dump -U gateway -d gateway --format=custom >"$backup_dir/gateway.dump"

# The MinIO image does not guarantee a tar binary. Docker can stream a tar
# archive directly, keeping the drill independent of utilities in that image.
minio_container=$(docker compose --env-file .env -f compose.yml ps -q minio)
test -n "$minio_container"
docker cp "$minio_container:/data/." - >"$backup_dir/minio-data.tar"

shasum -a 256 "$backup_dir/gateway.dump" "$backup_dir/minio-data.tar" >"$backup_dir/SHA256SUMS"
printf 'Backup created: %s\n' "$backup_dir"
