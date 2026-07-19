#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

environment_file=${GATEWAY_ENV_FILE:-.env}

if [[ ! -f "$environment_file" ]]; then
  printf '%s\n' 'Run requires a configured .env file.' >&2
  exit 1
fi

# shellcheck disable=SC1091
source "$environment_file"

for name in GATEWAY_HTTP_PORT GATEWAY_ADMIN_TOKEN GATEWAY_RESOLVER_TOKEN; do
  if [[ -z "${!name:-}" ]]; then
    printf 'Missing required %s\n' "$name" >&2
    exit 1
  fi
done

gateway_url="http://localhost:${GATEWAY_HTTP_PORT}"
original_resolver_token=$GATEWAY_RESOLVER_TOKEN
rotated_resolver_token="release-readiness-${RANDOM}-${RANDOM}"
token_rotated=false
gateway_container=""
gateway_configuration_restored=false

gateway_env() {
  local name=$1
  docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$gateway_container" \
    | sed -n "s/^${name}=//p"
}

capture_gateway_configuration() {
  gateway_container=$(docker compose --env-file "$environment_file" -f compose.yml ps -q gateway)
  test -n "$gateway_container" || { printf '%s\n' 'Release readiness requires a running Gateway.' >&2; exit 1; }
  original_adapter_mode=$(gateway_env GATEWAY_ADAPTER_MODE)
  original_gitea_username=$(gateway_env GATEWAY_GITEA_USERNAME)
  original_gitea_token=$(gateway_env GATEWAY_GITEA_TOKEN)
  original_maven_proxy_allowed_hosts=$(gateway_env GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS)
}

restore_gateway() {
  if [[ "$token_rotated" == true ]]; then
    GATEWAY_ADAPTER_MODE="$original_adapter_mode" \
    GATEWAY_GITEA_USERNAME="$original_gitea_username" \
    GATEWAY_GITEA_TOKEN="$original_gitea_token" \
    GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS="$original_maven_proxy_allowed_hosts" \
    GATEWAY_RESOLVER_TOKEN="$original_resolver_token" \
      docker compose --env-file "$environment_file" -f compose.yml up -d --force-recreate --wait gateway >/dev/null
    gateway_configuration_restored=true
  fi
}
trap restore_gateway EXIT

expect_status() {
  local expected=$1 url=$2 actual
  actual=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' "$url")
  if [[ "$actual" != "$expected" ]]; then
    printf 'Expected %s from %s, got %s\n' "$expected" "$url" "$actual" >&2
    exit 1
  fi
}

wait_ready() {
  local expected=$1 attempts=30
  while (( attempts > 0 )); do
    if [[ $(curl --silent --output /dev/null --write-out '%{http_code}' "$gateway_url/readyz") == "$expected" ]]; then
      return 0
    fi
    attempts=$((attempts - 1))
    sleep 1
  done
  printf 'Gateway readiness did not become HTTP %s\n' "$expected" >&2
  exit 1
}

# Standard Docker and Maven/Gradle clients exercise the Gitea Hosted and
# controlled external Proxy paths. The Maven fixture also verifies an
# unavailable upstream can still serve already cached content.
make oci-e2e
for client in oras; do
  GATEWAY_E2E_SKIP_BUILD=1 OCI_E2E_CLIENT="$client" ./scripts/oci-e2e.sh
done
# OCI E2E already seeded Gitea. Calling the script directly prevents Make from
# re-seeding it and rotating the fixture token between the two client checks.
GATEWAY_E2E_SKIP_BUILD=1 ./scripts/maven-e2e.sh
make performance-readiness
make upgrade-readiness

cache_status() {
  curl --silent --show-error --fail \
    -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
    "$gateway_url/api/v1/operations/cache"
}

# Readiness must surface dependencies that make cache reads or metadata writes
# unsafe, then recover once each dependency is restored.
docker compose --env-file "$environment_file" -f compose.yml stop minio >/dev/null
wait_ready 503
docker compose --env-file "$environment_file" -f compose.yml start minio >/dev/null
wait_ready 204
cache_status >/dev/null

docker compose --env-file "$environment_file" -f compose.yml stop postgres >/dev/null
wait_ready 503
docker compose --env-file "$environment_file" -f compose.yml start postgres >/dev/null
wait_ready 204
cache_status >/dev/null

# The maintenance view is admin-only and reports the collector's observable
# state. Its retention behavior is covered with deterministic time in Go tests.
before_collection=$(cache_status)
before_successful_runs=$(sed -n 's/.*"successful_runs":\([0-9][0-9]*\).*/\1/p' <<<"$before_collection")
test -n "$before_successful_runs" || { printf '%s\n' 'Cache status did not include successful_runs.' >&2; exit 1; }
collect_status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -X POST -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
  "$gateway_url/api/v1/operations/cache/collect")
[[ "$collect_status" == 204 ]] || { printf 'Cache collection returned HTTP %s\n' "$collect_status" >&2; exit 1; }
cache_status=$(cache_status)
for field in object_count bytes pending_candidates successful_runs failed_runs; do
  grep -Eq "\"${field}\":[0-9]+" <<<"$cache_status" || { printf 'Cache status lacks numeric %s.\n' "$field" >&2; exit 1; }
done
after_successful_runs=$(sed -n 's/.*"successful_runs":\([0-9][0-9]*\).*/\1/p' <<<"$cache_status")
[[ "$after_successful_runs" -eq $((before_successful_runs + 1)) ]] || { printf 'Cache successful_runs did not increment: before=%s after=%s\n' "$before_successful_runs" "$after_successful_runs" >&2; exit 1; }

# Static resolver-token rotation invalidates issued OCI bearer tokens because
# their HMAC key changes. It is the MVP token-revocation procedure.
old_oci_token=$(curl --silent --show-error --fail --user "release-readiness:${original_resolver_token}" \
  "$gateway_url/auth/token" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
test -n "$old_oci_token" || { printf '%s\n' 'Old OCI bearer token is empty.' >&2; exit 1; }

capture_gateway_configuration

token_rotated=true
GATEWAY_RESOLVER_TOKEN="$rotated_resolver_token" \
  docker compose --env-file "$environment_file" -f compose.yml up -d --force-recreate --wait gateway >/dev/null

expect_status 401 "$gateway_url/v2/"
old_token_status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $old_oci_token" "$gateway_url/v2/")
[[ "$old_token_status" == 401 ]] || { printf 'Old OCI bearer token returned HTTP %s\n' "$old_token_status" >&2; exit 1; }
new_oci_token=$(curl --silent --show-error --fail --user "release-readiness:${rotated_resolver_token}" \
  "$gateway_url/auth/token" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
test -n "$new_oci_token" || { printf '%s\n' 'New OCI bearer token is empty.' >&2; exit 1; }
new_token_status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $new_oci_token" "$gateway_url/v2/")
[[ "$new_token_status" == 200 ]] || { printf 'New OCI bearer token returned HTTP %s\n' "$new_token_status" >&2; exit 1; }
if [[ "${RELEASE_READINESS_FAIL_AFTER_TOKEN_ROTATION:-}" == 1 ]]; then
  printf '%s\n' 'Injected release readiness failure after token rotation.' >&2
  exit 1
fi

printf '%s\n' 'Release readiness passed: Gitea OCI, Maven/Gradle proxy cache, dependency recovery, cache maintenance view, and resolver-token rotation.'
