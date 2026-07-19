#!/usr/bin/env bash
set -euo pipefail

backup_dir=${1:-.artifacts/backup-drill/$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$backup_dir"

docker compose --env-file .env -f compose.yml exec -T postgres \
  pg_dump -U gateway -d gateway --format=custom >"$backup_dir/gateway.dump"
docker compose --env-file .env -f compose.yml exec -T minio \
  sh -ec 'tar -C /data -czf - .' >"$backup_dir/minio-data.tar.gz"

shasum -a 256 "$backup_dir/gateway.dump" "$backup_dir/minio-data.tar.gz" >"$backup_dir/SHA256SUMS"
printf 'Backup created: %s\n' "$backup_dir"
