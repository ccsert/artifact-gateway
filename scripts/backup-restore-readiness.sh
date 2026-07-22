#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

environment_file=${GATEWAY_ENV_FILE:-.env}
test -f "$environment_file" || { printf '%s\n' 'Backup readiness requires a configured environment file.' >&2; exit 1; }
# shellcheck disable=SC1091
source "$environment_file"

free_port() {
  python3 -c 'import socket; listener = socket.socket(); listener.bind(("127.0.0.1", 0)); print(listener.getsockname()[1]); listener.close()'
}

gateway_port=$(free_port)
minio_api_port=$(free_port)
minio_console_port=$(free_port)
project="artifact-gateway-backup-${RANDOM}-${RANDOM}"
isolated_environment=$(mktemp)
mkdir -p "$repo_root/.artifacts"
backup_dir=$(mktemp -d "$repo_root/.artifacts/backup-readiness.XXXXXX")

awk -F= '$1 != "GATEWAY_HTTP_PORT" && $1 != "MINIO_API_PORT" && $1 != "MINIO_CONSOLE_PORT" { print }' "$environment_file" >"$isolated_environment"
printf 'GATEWAY_HTTP_PORT=%s\nMINIO_API_PORT=%s\nMINIO_CONSOLE_PORT=%s\n' "$gateway_port" "$minio_api_port" "$minio_console_port" >>"$isolated_environment"

compose() {
  COMPOSE_PROJECT_NAME="$project" docker compose --env-file "$isolated_environment" -f compose.yml "$@"
}

cleanup() {
  compose down -v --remove-orphans >/dev/null 2>&1 || true
  rm -f "$isolated_environment"
  rm -rf "$backup_dir"
}
trap cleanup EXIT

compose up -d --build --wait
# shellcheck disable=SC1091
source "$isolated_environment"
gateway_url="http://localhost:${GATEWAY_HTTP_PORT}"
run_id="backup-${RANDOM}"
COMPOSE_PROJECT_NAME="$project" GATEWAY_ENV_FILE="$isolated_environment" RAW_E2E_RUN_ID="$run_id" ./scripts/raw-e2e.sh
raw_group="raw-ready-${run_id}"
conan_group="conan-ready-${run_id}"
conan_payload=$(printf '{"name":"%s","anonymous":true,"cacheQuotaBytes":1048576,"members":[{"name":"fixture","type":"hosted","endpoint":"http://host.docker.internal:9","position":0,"anonymous":true}]}' "$conan_group")
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' \
  --data "$conan_payload" "$gateway_url/api/v1/conan/groups")
[[ "$status" == 201 ]] || { printf 'Creating recovery Conan Group returned HTTP %s.\n' "$status" >&2; exit 1; }
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  "$gateway_url/conan/v2/$conan_group/conans/pkg/1.0/user/stable/revisions")
[[ "$status" == 502 ]] || { printf 'Recovery Conan audit request returned HTTP %s.\n' "$status" >&2; exit 1; }

COMPOSE_PROJECT_NAME="$project" GATEWAY_ENV_FILE="$isolated_environment" ./scripts/backup-drill.sh "$backup_dir"
mutation_payload=$(printf '{"name":"post-restore-%s","anonymous":false,"cacheQuotaBytes":1048576,"members":[{"name":"fixture","type":"hosted","endpoint":"http://host.docker.internal:9","position":0,"anonymous":false}]}' "$run_id")
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' \
  --data "$mutation_payload" "$gateway_url/api/v1/raw/groups")
[[ "$status" == 201 ]] || { printf 'Creating post-backup mutation returned HTTP %s.\n' "$status" >&2; exit 1; }
COMPOSE_PROJECT_NAME="$project" GATEWAY_ENV_FILE="$isolated_environment" ./scripts/restore-drill.sh "$backup_dir"

for format in raw conan; do
  group_var="${format}_group"
  status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
    -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v1/$format/groups/${!group_var}")
  [[ "$status" == 200 ]] || { printf 'Restored %s Group returned HTTP %s.\n' "$format" "$status" >&2; exit 1; }
done
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v1/raw/groups/post-restore-$run_id")
[[ "$status" == 404 ]] || { printf 'Post-backup Raw Group survived restore with HTTP %s.\n' "$status" >&2; exit 1; }
[[ $(curl --silent --show-error "$gateway_url/raw/$raw_group/release/app.txt") == 'raw release artifact' ]] || { printf '%s\n' 'Restored Raw cache content is unavailable.' >&2; exit 1; }
audits=$(curl --silent --show-error --fail -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v1/audits?limit=500")
grep -Fq '"Format":"raw"' <<<"$audits" || { printf '%s\n' 'Restored Raw audit is unavailable.' >&2; exit 1; }
grep -Fq '"Format":"conan"' <<<"$audits" || { printf '%s\n' 'Restored Conan audit is unavailable.' >&2; exit 1; }

printf '%s\n' 'Backup/restore readiness passed: isolated PostgreSQL and MinIO restore preserved Raw cache, Conan state, and V2 audit records.'
