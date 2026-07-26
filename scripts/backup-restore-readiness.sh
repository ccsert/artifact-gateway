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
grant_headers=$(mktemp)
grant_response=$(mktemp)
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
  rm -f "$grant_headers"
  rm -f "$grant_response"
  rm -rf "$backup_dir"
}
trap cleanup EXIT

compose up -d --build --wait
# shellcheck disable=SC1091
source "$isolated_environment"
gateway_url="http://localhost:${GATEWAY_HTTP_PORT}"
gateway_token() {
  curl --silent --show-error --fail --user "$1:$GATEWAY_RESOLVER_TOKEN" "$gateway_url/auth/token" |
    python3 -c 'import json, sys; print(json.load(sys.stdin)["token"])'
}
run_id="backup-${RANDOM}"
COMPOSE_PROJECT_NAME="$project" GATEWAY_ENV_FILE="$isolated_environment" RAW_E2E_RUN_ID="$run_id" ./scripts/raw-e2e.sh
raw_group="raw-ready-${run_id}"
conan_group="conan-ready-${run_id}"
grant_repository="grant-restore-${run_id}"
replication_target_repository="replication-restore-${run_id}"
promotion_target_repository="promotion-restore-${run_id}"
create_repository=$(printf '{"name":"%s","format":"raw"}' "$grant_repository")
repository=$(curl --silent --show-error --fail \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: grant-restore-${run_id}" --data "$create_repository" "$gateway_url/api/v2/repositories")
repository_id=$(python3 -c 'import json, sys; print(json.load(sys.stdin)["id"])' <<<"$repository")
[[ -n "$repository_id" ]] || { printf '%s\n' 'Creating grant recovery Repository returned no ID.' >&2; exit 1; }
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v2/repositories/$repository_id")
[[ "$status" == 200 ]] || { printf 'Created grant recovery Repository %s lookup returned HTTP %s.\n' "$repository_id" "$status" >&2; exit 1; }

writer_token=$(gateway_token recovery-writer)
reader_token=$(gateway_token recovery-reader)
denied_token=$(gateway_token recovery-denied)
status=$(curl --silent --show-error --write-out '%{http_code}' \
  --request PUT -H "Authorization: Bearer $writer_token" --data-binary 'grant restore artifact' \
  "$gateway_url/raw/$grant_repository/releases/app.txt")
[[ "$status" == 201 ]] || { printf 'Writing grant recovery Raw object returned HTTP %s.\n' "$status" >&2; exit 1; }
replication_target=$(curl --silent --show-error --fail \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: replication-target-${run_id}" \
  --data "$(printf '{\"name\":\"%s\",\"format\":\"raw\"}' "$replication_target_repository")" "$gateway_url/api/v2/repositories")
replication_target_id=$(python3 -c 'import json, sys; print(json.load(sys.stdin)["id"])' <<<"$replication_target")
raw_digest="sha256:$(printf '%s' 'grant restore artifact' | shasum -a 256 | awk '{print $1}')"
replication_plan=$(curl --silent --show-error --fail \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: replication-plan-${run_id}" \
  --data "$(printf '{\"targetRepositoryId\":\"%s\",\"coordinate\":\"releases/app.txt\",\"digest\":\"%s\"}' "$replication_target_id" "$raw_digest")" \
  "$gateway_url/api/v2/repositories/$repository_id/replications")
replication_plan_id=$(python3 -c 'import json, sys; print(json.load(sys.stdin)["id"])' <<<"$replication_plan")
[[ -n "$replication_plan_id" ]] || { printf '%s\n' 'Replication recovery plan returned no ID.' >&2; exit 1; }
promotion_target=$(curl --silent --show-error --fail \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: promotion-target-${run_id}" \
  --data "$(printf '{\"name\":\"%s\",\"format\":\"raw\"}' "$promotion_target_repository")" "$gateway_url/api/v2/repositories")
promotion_target_id=$(python3 -c 'import json, sys; print(json.load(sys.stdin)["id"])' <<<"$promotion_target")
promotion_job=$(curl --silent --show-error --fail \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: promotion-job-${run_id}" \
  --data "$(printf '{\"targetRepositoryId\":\"%s\",\"coordinate\":\"releases/app.txt\",\"digest\":\"%s\"}' "$promotion_target_id" "$raw_digest")" \
  "$gateway_url/api/v2/repositories/$repository_id/promotions")
promotion_job_id=$(python3 -c 'import json, sys; print(json.load(sys.stdin)["id"])' <<<"$promotion_job")
[[ -n "$promotion_job_id" ]] || { printf '%s\n' 'Promotion recovery job returned no ID.' >&2; exit 1; }
grant_payload='[{"principal":"recovery-reader","scopes":["repositories:read"]}]'
status=$(curl --silent --show-error --write-out '%{http_code}' \
  --request PUT -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' -H 'If-Match: 1' \
  --data "$grant_payload" --output "$grant_response" "$gateway_url/api/v2/repositories/$repository_id/grants")
[[ "$status" == 200 ]] || { printf 'Replacing grant recovery Repository grants for %s returned HTTP %s: %s\n' "$repository_id" "$status" "$(<"$grant_response")" >&2; exit 1; }
configured_grants=$(curl --silent --show-error --fail --dump-header "$grant_headers" \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v2/repositories/$repository_id/grants")
tr -d '\r' <"$grant_headers" | grep -qi '^etag: 2$' || { printf '%s\n' 'Configured Repository grants did not receive version 2.' >&2; exit 1; }
grep -Fq '"principal":"recovery-reader"' <<<"$configured_grants" || { printf '%s\n' 'Configured Repository grants did not retain recovery-reader.' >&2; exit 1; }
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $denied_token" "$gateway_url/raw/$grant_repository/releases/app.txt")
[[ "$status" == 401 ]] || { printf 'Managed grant denial before backup returned HTTP %s.\n' "$status" >&2; exit 1; }
metrics=$(curl --silent --show-error --fail "$gateway_url/metrics")
grep -Fq 'artifact_gateway_repository_authorization_denials_total{format="raw",authorization_source="repository_grants",authorization_reason="scope_not_granted"} 1' <<<"$metrics" || {
  printf '%s\n' 'Managed grant denial metric is unavailable or has unexpected labels.' >&2; exit 1;
}
status=$(curl --silent --show-error --output "$grant_response" --write-out '%{http_code}' \
  -H "Authorization: Bearer $reader_token" "$gateway_url/raw/$grant_repository/releases/app.txt")
[[ "$status" == 200 && $(<"$grant_response") == 'grant restore artifact' ]] || {
  printf 'Managed grant allow before backup returned HTTP %s: %s\n' "$status" "$(<"$grant_response")" >&2; exit 1;
}
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
restored_grants=$(curl --silent --show-error --fail --dump-header "$grant_headers" \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v2/repositories/$repository_id/grants")
tr -d '\r' <"$grant_headers" | grep -qi '^etag: 2$' || { printf '%s\n' 'Restored Repository grants did not retain version 2.' >&2; exit 1; }
grep -Fq '"principal":"recovery-reader"' <<<"$restored_grants" || { printf '%s\n' 'Restored Repository grants did not retain recovery-reader.' >&2; exit 1; }
restored_replications=$(curl --silent --show-error --fail -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v2/repositories/$replication_target_id/replications")
grep -Fq "\"id\":\"$replication_plan_id\"" <<<"$restored_replications" || { printf '%s\n' 'Restored replication plan is unavailable.' >&2; exit 1; }
restored_lifecycle_jobs=$(curl --silent --show-error --fail -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v2/repositories/$promotion_target_id/lifecycle-jobs")
grep -Fq "\"id\":\"$promotion_job_id\"" <<<"$restored_lifecycle_jobs" || { printf '%s\n' 'Restored promotion lifecycle job is unavailable.' >&2; exit 1; }
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $denied_token" "$gateway_url/raw/$grant_repository/releases/app.txt")
[[ "$status" == 401 ]] || { printf 'Managed grant denial after restore returned HTTP %s.\n' "$status" >&2; exit 1; }
status=$(curl --silent --show-error --output "$grant_response" --write-out '%{http_code}' \
  -H "Authorization: Bearer $reader_token" "$gateway_url/raw/$grant_repository/releases/app.txt")
[[ "$status" == 200 && $(<"$grant_response") == 'grant restore artifact' ]] || {
  printf 'Restored managed grant allow returned HTTP %s: %s\n' "$status" "$(<"$grant_response")" >&2; exit 1;
}
audits=$(curl --silent --show-error --fail -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v1/audits?limit=500")
grep -Fq '"Format":"raw"' <<<"$audits" || { printf '%s\n' 'Restored Raw audit is unavailable.' >&2; exit 1; }
grep -Fq '"Format":"conan"' <<<"$audits" || { printf '%s\n' 'Restored Conan audit is unavailable.' >&2; exit 1; }
grep -Fq '"Actor":"recovery-denied"' <<<"$audits" || { printf '%s\n' 'Restored Repository grant denial audit is unavailable.' >&2; exit 1; }
grep -Fq '"AuthorizationSource":"repository_grants"' <<<"$audits" || { printf '%s\n' 'Restored Repository grant authorization source is unavailable.' >&2; exit 1; }

printf '%s\n' 'Backup/restore readiness passed: isolated PostgreSQL and MinIO restore preserved Raw cache, Conan state, Repository grants, promotion jobs, replication plans, and authorization audits.'
