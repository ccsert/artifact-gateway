#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

environment_file=${GATEWAY_ENV_FILE:-.env}
previous_ref=${GATEWAY_UPGRADE_FROM_REF:-0d1d3f8}

if [[ ! -f "$environment_file" || ! -f .gitea-fixture/connection.env ]]; then
  printf '%s\n' 'Run requires a configured environment file and the seeded Gitea fixture.' >&2
  exit 1
fi
git rev-parse --verify --quiet "${previous_ref}^{commit}" >/dev/null || {
  printf 'Upgrade source revision is unavailable: %s\n' "$previous_ref" >&2
  exit 1
}

# shellcheck disable=SC1091
source "$environment_file"
# shellcheck disable=SC1091
source .gitea-fixture/connection.env

for name in GATEWAY_ADMIN_TOKEN GATEWAY_HTTP_PORT MINIO_API_PORT MINIO_CONSOLE_PORT; do
  if [[ -z "${!name:-}" ]]; then
    printf 'Missing required %s\n' "$name" >&2
    exit 1
  fi
done

free_port() {
  python3 -c 'import socket; listener = socket.socket(); listener.bind(("127.0.0.1", 0)); print(listener.getsockname()[1]); listener.close()'
}

gateway_port=$(free_port)
minio_api_port=$(free_port)
minio_console_port=$(free_port)
upgrade_project="artifact-gateway-upgrade-${RANDOM}-${RANDOM}"
previous_tree=$(mktemp -d)
upgrade_environment_file=$(mktemp)
fixture_dir=$(mktemp -d)
raw_fixture_port=$(free_port)
raw_fixture_pid=""

# Docker Compose resolves values from --env-file ahead of process variables.
# Make a private copy so this rehearsal cannot bind the release environment's
# ports or share its persistent volumes.
awk -F= '$1 != "GATEWAY_HTTP_PORT" && $1 != "MINIO_API_PORT" && $1 != "MINIO_CONSOLE_PORT" { print }' \
  "$environment_file" >"$upgrade_environment_file"
printf 'GATEWAY_HTTP_PORT=%s\nMINIO_API_PORT=%s\nMINIO_CONSOLE_PORT=%s\n' \
  "$gateway_port" "$minio_api_port" "$minio_console_port" >>"$upgrade_environment_file"

compose() {
  COMPOSE_PROJECT_NAME="$upgrade_project" docker compose --env-file "$upgrade_environment_file" -f "$1/compose.yml" "${@:2}"
}

cleanup() {
  if [[ -n "$raw_fixture_pid" ]]; then kill "$raw_fixture_pid" 2>/dev/null || true; fi
  compose "$repo_root" down -v --remove-orphans >/dev/null 2>&1 || true
  git worktree remove --force "$previous_tree" >/dev/null 2>&1 || true
  rm -f "$upgrade_environment_file"
  rm -rf "$fixture_dir"
}
trap cleanup EXIT

git worktree add --detach "$previous_tree" "$previous_ref" >/dev/null

# First deploy the previous revision against new persistent volumes. This keeps
# the production checkout untouched while giving the migration an actual prior
# schema and configuration to upgrade.
compose "$previous_tree" up -d --build --wait

# Upgrade the exact same project and volumes with the current checkout. The
# one-shot migration service must complete before Gateway is considered ready.
compose "$repo_root" up -d --build --wait
gateway_url="http://localhost:${gateway_port}"
ready_status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' "$gateway_url/readyz")
[[ "$ready_status" == 204 ]] || { printf 'Upgraded Gateway readiness returned HTTP %s.\n' "$ready_status" >&2; exit 1; }

# Seed V2 rows and cache objects before rollback. The source goes away before
# rollback, so successful Raw reads after restore prove the migrated MinIO
# cache remains usable while the prior binary ignores the additive V2 tables.
mkdir -p "$fixture_dir/release"
printf '%s' 'upgrade raw artifact' >"$fixture_dir/release/app.txt"
python3 -m http.server "$raw_fixture_port" --bind 127.0.0.1 --directory "$fixture_dir" >"$fixture_dir/raw.log" 2>&1 &
raw_fixture_pid=$!
until curl --silent --show-error --fail "http://localhost:${raw_fixture_port}/release/app.txt" >/dev/null; do
  kill -0 "$raw_fixture_pid" 2>/dev/null || { cat "$fixture_dir/raw.log" >&2; exit 1; }
  sleep 1
done
raw_group="upgrade-raw-${RANDOM}"
conan_group="upgrade-conan-${RANDOM}"
raw_payload=$(printf '{"name":"%s","anonymous":true,"cacheQuotaBytes":1048576,"members":[{"name":"fixture","type":"hosted","endpoint":"http://host.docker.internal:%s","position":0,"anonymous":true}]}' "$raw_group" "$raw_fixture_port")
conan_payload=$(printf '{"name":"%s","anonymous":true,"cacheQuotaBytes":1048576,"members":[{"name":"fixture","type":"hosted","endpoint":"http://host.docker.internal:%s","position":0,"anonymous":true}]}' "$conan_group" "$raw_fixture_port")
for format in raw conan; do
  payload_var="${format}_payload"
  status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
    -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' \
    --data "${!payload_var}" "$gateway_url/api/v1/$format/groups")
  [[ "$status" == 201 ]] || { printf 'Creating %s V2 Group returned HTTP %s.\n' "$format" "$status" >&2; exit 1; }
done
[[ $(curl --silent --show-error "$gateway_url/raw/$raw_group/release/app.txt") == 'upgrade raw artifact' ]] || { printf '%s\n' 'Upgraded Raw Group did not serve fixture content.' >&2; exit 1; }
conan_status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  "$gateway_url/conan/v2/$conan_group/conans/pkg/1.0/user/stable/revisions")
[[ "$conan_status" == 404 ]] || { printf 'Upgraded Conan Group returned HTTP %s.\n' "$conan_status" >&2; exit 1; }
for format in raw conan; do
  group_var="${format}_group"
  group_json=$(curl --silent --show-error --fail -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v1/$format/groups/${!group_var}")
  grep -Fq '"anonymous":true' <<<"$group_json" || { printf '%s anonymous policy was not persisted.\n' "$format" >&2; exit 1; }
done
audits=$(curl --silent --show-error --fail -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v1/audits?limit=500")
grep -Fq '"Format":"raw"' <<<"$audits" || { printf '%s\n' 'Raw audit was not persisted before rollback.' >&2; exit 1; }
grep -Fq '"Format":"conan"' <<<"$audits" || { printf '%s\n' 'Conan audit was not persisted before rollback.' >&2; exit 1; }
kill "$raw_fixture_pid"
wait "$raw_fixture_pid" 2>/dev/null || true
raw_fixture_pid=""
[[ $(curl --silent --show-error "$gateway_url/raw/$raw_group/release/app.txt") == 'upgrade raw artifact' ]] || { printf '%s\n' 'Upgraded Raw cache did not survive source shutdown.' >&2; exit 1; }

# Exercise both package protocols on the upgraded instance using the existing
# real Gitea fixture. Call scripts directly: Make's fixture prerequisite would
# try to allocate a second Gitea on the fixture's published ports.
COMPOSE_PROJECT_NAME="$upgrade_project" \
GATEWAY_ENV_FILE="$upgrade_environment_file" \
GATEWAY_E2E_SKIP_BUILD=1 \
./scripts/oci-e2e.sh
COMPOSE_PROJECT_NAME="$upgrade_project" \
GATEWAY_ENV_FILE="$upgrade_environment_file" \
GATEWAY_E2E_SKIP_BUILD=1 \
./scripts/maven-e2e.sh

# Roll back the binary/configuration while retaining the upgraded volumes. A
# successful authenticated group read proves the previous release can start
# against the migrated data before the isolated volumes are removed.
compose "$previous_tree" up -d --build --wait
ready_status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' "$gateway_url/readyz")
[[ "$ready_status" == 204 ]] || { printf 'Rolled-back Gateway readiness returned HTTP %s.\n' "$ready_status" >&2; exit 1; }
group_status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v1/oci/groups/$GITEA_FIXTURE_ORG")
[[ "$group_status" == 200 ]] || { printf 'Rolled-back OCI group read returned HTTP %s.\n' "$group_status" >&2; exit 1; }

printf 'Upgrade gate passed: %s -> current -> %s; OCI/Maven clients and persisted Raw/Conan V2 state passed before rollback.\n' \
  "$previous_ref" "$previous_ref"
