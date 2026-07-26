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
oci_headers=$(mktemp)
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
  rm -f "$oci_headers"
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
create_hosted_repository() {
  local format=$1 name=$2 key=$3 response
  response=$(curl --silent --show-error --fail \
    -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' -H "Idempotency-Key: $key" \
    --data "$(printf '{\"name\":\"%s\",\"format\":\"%s\"}' "$name" "$format")" "$gateway_url/api/v2/repositories")
  python3 -c 'import json, sys; print(json.load(sys.stdin)["id"])' <<<"$response"
}
enqueue_idempotently() {
  local path=$1 key=$2 payload=$3 first replay
  first=$(curl --silent --show-error --fail \
    -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' -H "Idempotency-Key: $key" \
    --data "$payload" "$gateway_url$path") || return 1
  replay=$(curl --silent --show-error --fail \
    -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' -H "Idempotency-Key: $key" \
    --data "$payload" "$gateway_url$path") || return 1
  [[ "$replay" == "$first" ]] || { printf 'Idempotency replay changed the response for %s.\n' "$path" >&2; exit 1; }
  python3 -c 'import json, sys; print(json.load(sys.stdin)["id"])' <<<"$first"
}
run_id="backup-${RANDOM}"
COMPOSE_PROJECT_NAME="$project" GATEWAY_ENV_FILE="$isolated_environment" RAW_E2E_RUN_ID="$run_id" ./scripts/raw-e2e.sh
raw_group="raw-ready-${run_id}"
conan_group="conan-ready-${run_id}"
grant_repository="grant-restore-${run_id}"
replication_target_repository="replication-restore-${run_id}"
promotion_target_repository="promotion-restore-${run_id}"
oci_source_repository="oci-restore-${run_id}"
maven_source_repository="maven-restore-${run_id}"
conan_source_repository="conan-restore-${run_id}"
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
replication_plan_id=$(enqueue_idempotently "/api/v2/repositories/$repository_id/replications" "replication-plan-${run_id}" "$(printf '{\"targetRepositoryId\":\"%s\",\"coordinate\":\"releases/app.txt\",\"digest\":\"%s\"}' "$replication_target_id" "$raw_digest")")
[[ -n "$replication_plan_id" ]] || { printf '%s\n' 'Replication recovery plan returned no ID.' >&2; exit 1; }
promotion_target=$(curl --silent --show-error --fail \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: promotion-target-${run_id}" \
  --data "$(printf '{\"name\":\"%s\",\"format\":\"raw\"}' "$promotion_target_repository")" "$gateway_url/api/v2/repositories")
promotion_target_id=$(python3 -c 'import json, sys; print(json.load(sys.stdin)["id"])' <<<"$promotion_target")
promotion_job_id=$(enqueue_idempotently "/api/v2/repositories/$repository_id/promotions" "promotion-job-${run_id}" "$(printf '{\"targetRepositoryId\":\"%s\",\"coordinate\":\"releases/app.txt\",\"digest\":\"%s\"}' "$promotion_target_id" "$raw_digest")")
[[ -n "$promotion_job_id" ]] || { printf '%s\n' 'Promotion recovery job returned no ID.' >&2; exit 1; }

# Create visible source Artifacts through each native protocol before snapshotting
# their idempotent promotion and replication instructions.
oci_source_id=$(create_hosted_repository oci "$oci_source_repository" "oci-source-${run_id}")
oci_body='backup restore oci artifact'
oci_digest="sha256:$(printf '%s' "$oci_body" | shasum -a 256 | awk '{print $1}')"
status=$(curl --silent --show-error --output /dev/null --dump-header "$oci_headers" --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" --request POST "$gateway_url/v2/$oci_source_repository/team/widget/blobs/uploads/")
[[ "$status" == 202 ]] || { printf 'Creating OCI upload returned HTTP %s.\n' "$status" >&2; exit 1; }
oci_location=$(tr -d '\r' <"$oci_headers" | awk 'tolower($1)=="location:" {print $2}')
[[ -n "$oci_location" ]] || { printf '%s\n' 'Creating OCI upload returned no Location header.' >&2; exit 1; }
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" --request PUT --data-binary "$oci_body" "$gateway_url$oci_location?digest=$oci_digest")
[[ "$status" == 201 ]] || { printf 'Completing OCI upload returned HTTP %s.\n' "$status" >&2; exit 1; }
oci_manifest=$(printf '{"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"%s","size":%s},"layers":[]}' "$oci_digest" "${#oci_body}")
status=$(curl --silent --show-error --output /dev/null --dump-header "$oci_headers" --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/vnd.oci.image.manifest.v1+json' \
  --request PUT --data-binary "$oci_manifest" "$gateway_url/v2/$oci_source_repository/team/widget/manifests/backup")
[[ "$status" == 201 ]] || { printf 'Publishing OCI manifest returned HTTP %s.\n' "$status" >&2; exit 1; }
oci_manifest_digest=$(tr -d '\r' <"$oci_headers" | awk 'tolower($1)=="docker-content-digest:" {print $2}')
[[ -n "$oci_manifest_digest" ]] || { printf '%s\n' 'Publishing OCI manifest returned no digest.' >&2; exit 1; }

maven_source_id=$(create_hosted_repository maven "$maven_source_repository" "maven-source-${run_id}")
maven_coordinate='org.example:restore:1.0.0'
maven_name='restore-1.0.0.pom'
maven_body='<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>restore</artifactId><version>1.0.0</version></project>'
maven_digest="sha256:$(printf '%s' "$maven_body" | shasum -a 256 | awk '{print $1}')"
maven_session=$(curl --silent --show-error --fail -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' -H "Idempotency-Key: maven-session-${run_id}" \
  --data "$(printf '{\"format\":\"maven\",\"coordinate\":\"%s\",\"pomObject\":\"%s\",\"objects\":[{\"name\":\"%s\",\"digest\":\"%s\",\"size\":%s}]}' "$maven_coordinate" "$maven_name" "$maven_name" "$maven_digest" "${#maven_body}")" "$gateway_url/api/v2/repositories/$maven_source_id/publish-sessions")
maven_session_id=$(python3 -c 'import json, sys; data=json.load(sys.stdin); print(data.get("ID", data.get("id", "")))' <<<"$maven_session")
[[ -n "$maven_session_id" ]] || { printf '%s\n' 'Creating Maven publish session returned no ID.' >&2; exit 1; }
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" --request PUT --data-binary "$maven_body" "$gateway_url/api/v2/publish-sessions/$maven_session_id/objects/$maven_name")
[[ "$status" == 204 ]] || { printf 'Uploading Maven object returned HTTP %s.\n' "$status" >&2; exit 1; }
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" --request POST "$gateway_url/api/v2/publish-sessions/$maven_session_id:commit")
[[ "$status" == 200 ]] || { printf 'Committing Maven publication returned HTTP %s.\n' "$status" >&2; exit 1; }

conan_source_id=$(create_hosted_repository conan "$conan_source_repository" "conan-source-${run_id}")
conan_reference='restore/1.0/user/stable'
conan_revision='rrev'
conan_name='conanfile.py'
conan_body='backup restore conan artifact'
conan_object_digest="sha256:$(printf '%s' "$conan_body" | shasum -a 256 | awk '{print $1}')"
conan_digest=$(python3 -c 'import hashlib, sys; name, digest = sys.argv[1:]; print("sha256:" + hashlib.sha256((name + "\0" + digest + "\0").encode()).hexdigest())' "$conan_name" "$conan_object_digest")
conan_session=$(curl --silent --show-error --fail -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' \
  --data "$(printf '{\"kind\":\"recipe\",\"reference\":\"%s\",\"recipeRevision\":\"%s\",\"objects\":[{\"name\":\"%s\",\"digest\":\"%s\",\"size\":%s}]}' "$conan_reference" "$conan_revision" "$conan_name" "$conan_object_digest" "${#conan_body}")" "$gateway_url/api/v2/repositories/$conan_source_id/conan-publish-sessions")
conan_session_id=$(python3 -c 'import json, sys; data=json.load(sys.stdin); print(data.get("ID", data.get("id", "")))' <<<"$conan_session")
[[ -n "$conan_session_id" ]] || { printf '%s\n' 'Creating Conan publish session returned no ID.' >&2; exit 1; }
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" --request PUT --data-binary "$conan_body" "$gateway_url/api/v2/conan-publish-sessions/$conan_session_id/objects/$conan_name")
[[ "$status" == 204 ]] || { printf 'Uploading Conan object returned HTTP %s.\n' "$status" >&2; exit 1; }
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" --request POST "$gateway_url/api/v2/conan-publish-sessions/$conan_session_id:commit")
[[ "$status" == 200 ]] || { printf 'Committing Conan publication returned HTTP %s.\n' "$status" >&2; exit 1; }

oci_replication_target_id=$(create_hosted_repository oci "oci-replication-restore-${run_id}" "oci-replication-target-${run_id}")
oci_promotion_target_id=$(create_hosted_repository oci "oci-promotion-restore-${run_id}" "oci-promotion-target-${run_id}")
maven_replication_target_id=$(create_hosted_repository maven "maven-replication-restore-${run_id}" "maven-replication-target-${run_id}")
maven_promotion_target_id=$(create_hosted_repository maven "maven-promotion-restore-${run_id}" "maven-promotion-target-${run_id}")
conan_replication_target_id=$(create_hosted_repository conan "conan-replication-restore-${run_id}" "conan-replication-target-${run_id}")
conan_promotion_target_id=$(create_hosted_repository conan "conan-promotion-restore-${run_id}" "conan-promotion-target-${run_id}")

oci_replication_plan_id=$(enqueue_idempotently "/api/v2/repositories/$oci_source_id/replications" "oci-replication-${run_id}" "$(printf '{\"targetRepositoryId\":\"%s\",\"coordinate\":\"team/widget\",\"digest\":\"%s\"}' "$oci_replication_target_id" "$oci_manifest_digest")")
oci_promotion_job_id=$(enqueue_idempotently "/api/v2/repositories/$oci_source_id/promotions" "oci-promotion-${run_id}" "$(printf '{\"targetRepositoryId\":\"%s\",\"coordinate\":\"team/widget\",\"digest\":\"%s\"}' "$oci_promotion_target_id" "$oci_manifest_digest")")
maven_replication_plan_id=$(enqueue_idempotently "/api/v2/repositories/$maven_source_id/replications" "maven-replication-${run_id}" "$(printf '{\"targetRepositoryId\":\"%s\",\"coordinate\":\"%s\",\"digest\":\"%s\"}' "$maven_replication_target_id" "$maven_coordinate" "$maven_digest")")
maven_promotion_job_id=$(enqueue_idempotently "/api/v2/repositories/$maven_source_id/promotions" "maven-promotion-${run_id}" "$(printf '{\"targetRepositoryId\":\"%s\",\"coordinate\":\"%s\",\"digest\":\"%s\"}' "$maven_promotion_target_id" "$maven_coordinate" "$maven_digest")")
conan_coordinate="$conan_reference#$conan_revision"
conan_replication_plan_id=$(enqueue_idempotently "/api/v2/repositories/$conan_source_id/replications" "conan-replication-${run_id}" "$(printf '{\"targetRepositoryId\":\"%s\",\"coordinate\":\"%s\",\"digest\":\"%s\"}' "$conan_replication_target_id" "$conan_coordinate" "$conan_digest")")
conan_promotion_job_id=$(enqueue_idempotently "/api/v2/repositories/$conan_source_id/promotions" "conan-promotion-${run_id}" "$(printf '{\"targetRepositoryId\":\"%s\",\"coordinate\":\"%s\",\"digest\":\"%s\"}' "$conan_promotion_target_id" "$conan_coordinate" "$conan_digest")")
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
for replication in \
  "$oci_replication_target_id:$oci_replication_plan_id" \
  "$maven_replication_target_id:$maven_replication_plan_id" \
  "$conan_replication_target_id:$conan_replication_plan_id"; do
  target_id=${replication%%:*}
  plan_id=${replication#*:}
  restored_replications=$(curl --silent --show-error --fail -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v2/repositories/$target_id/replications")
  grep -Fq "\"id\":\"$plan_id\"" <<<"$restored_replications" || { printf 'Restored replication plan %s is unavailable.\n' "$plan_id" >&2; exit 1; }
done
for promotion in \
  "$oci_promotion_target_id:$oci_promotion_job_id" \
  "$maven_promotion_target_id:$maven_promotion_job_id" \
  "$conan_promotion_target_id:$conan_promotion_job_id"; do
  target_id=${promotion%%:*}
  job_id=${promotion#*:}
  restored_lifecycle_jobs=$(curl --silent --show-error --fail -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v2/repositories/$target_id/lifecycle-jobs")
  grep -Fq "\"id\":\"$job_id\"" <<<"$restored_lifecycle_jobs" || { printf 'Restored promotion job %s is unavailable.\n' "$job_id" >&2; exit 1; }
done
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
[[ $(grep -o '"Operation":"promote"' <<<"$audits" | wc -l | tr -d ' ') -ge 4 ]] || { printf '%s\n' 'Restored promotion audits do not cover all native formats.' >&2; exit 1; }
[[ $(grep -o '"Operation":"replicate"' <<<"$audits" | wc -l | tr -d ' ') -ge 4 ]] || { printf '%s\n' 'Restored replication audits do not cover all native formats.' >&2; exit 1; }

printf '%s\n' 'Backup/restore readiness passed: isolated PostgreSQL and MinIO restore preserved OCI, Maven, Raw, and Conan promotion jobs and replication plans, Raw cache, Conan state, Repository grants, and authorization audits.'
