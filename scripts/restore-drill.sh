#!/usr/bin/env bash
set -euo pipefail

backup_dir=${1:?usage: scripts/restore-drill.sh <backup-directory>}
shasum -a 256 --check "$backup_dir/SHA256SUMS"

docker compose --env-file .env -f compose.yml stop gateway
docker compose --env-file .env -f compose.yml exec -T postgres \
  pg_restore -U gateway -d gateway --clean --if-exists <"$backup_dir/gateway.dump"
minio_container=$(docker compose --env-file .env -f compose.yml ps -q minio)
test -n "$minio_container"
docker compose --env-file .env -f compose.yml exec -T minio sh -ec 'rm -rf /data/*'
docker cp - "$minio_container:/data" <"$backup_dir/minio-data.tar"
docker compose --env-file .env -f compose.yml start gateway
docker compose --env-file .env -f compose.yml exec -T gateway \
  wget -qO- http://localhost:8080/readyz >/dev/null
printf 'Restore completed from: %s\n' "$backup_dir"
