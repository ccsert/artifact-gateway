#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

base_ref=${GATEWAY_UPGRADE_FROM_REF:-0d1d3f8}
environment_file=${GATEWAY_ENV_FILE:-.env}
test -f "$environment_file" || { printf '%s\n' 'Upgrade readiness requires a configured environment file.' >&2; exit 1; }
git cat-file -e "$base_ref^{commit}"

free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'; }
project="artifact-gateway-upgrade-${RANDOM}-${RANDOM}"
gateway_image="${project}-gateway:latest"
rollback_image="${project}-rollback-gateway:local"
old_tree=$(mktemp -d)
# macOS exposes /var as a symlink to /private/var. Git records the physical
# worktree path, so normalize it before registration and cleanup.
old_tree=$(cd "$old_tree" && pwd -P)
isolated_environment=$(mktemp)
gateway_port=$(free_port)
minio_api_port=$(free_port)
minio_console_port=$(free_port)

cleanup() {
  COMPOSE_PROJECT_NAME="$project" docker compose --env-file "$isolated_environment" -f compose.yml down -v --remove-orphans >/dev/null 2>&1 || true
  test -f "$old_tree/compose.yml" && COMPOSE_PROJECT_NAME="$project" docker compose --env-file "$isolated_environment" -f "$old_tree/compose.yml" down -v --remove-orphans >/dev/null 2>&1 || true
  docker image rm "$rollback_image" "$gateway_image" >/dev/null 2>&1 || true
  git worktree remove --force "$old_tree" >/dev/null 2>&1 || rm -rf "$old_tree"
  rm -f "$isolated_environment"
}
trap cleanup EXIT

git worktree add --detach "$old_tree" "$base_ref" >/dev/null
awk -F= '$1 != "GATEWAY_HTTP_PORT" && $1 != "GATEWAY_POSTGRES_PORT" && $1 != "MINIO_API_PORT" && $1 != "MINIO_CONSOLE_PORT" { print }' "$environment_file" >"$isolated_environment"
printf 'GATEWAY_HTTP_PORT=%s\nGATEWAY_POSTGRES_PORT=%s\nMINIO_API_PORT=%s\nMINIO_CONSOLE_PORT=%s\n' "$gateway_port" "$(free_port)" "$minio_api_port" "$minio_console_port" >>"$isolated_environment"
gateway_url="http://127.0.0.1:${gateway_port}"

old_compose=(docker compose --env-file "$isolated_environment" -f "$old_tree/compose.yml")
current_compose=(docker compose --env-file "$isolated_environment" -f compose.yml)
status() { curl --silent --show-error --output /dev/null --write-out '%{http_code}' "$@"; }
admin=(-H "Authorization: Bearer $(awk -F= '$1 == "GATEWAY_ADMIN_TOKEN" { print substr($0, index($0, "=") + 1) }' "$isolated_environment")")

build_gateway() {
  local label=$1
  shift
  local attempt
  for attempt in 1 2 3; do
    if COMPOSE_PROJECT_NAME="$project" "$@" build gateway; then
      return 0
    fi
    if [[ "$attempt" -eq 3 ]]; then
      printf 'Building the %s Gateway failed after %s attempts.\n' "$label" "$attempt" >&2
      return 1
    fi
    printf 'Building the %s Gateway failed; retrying (%s/3).\n' "$label" "$((attempt + 1))" >&2
    sleep "$attempt"
  done
}

build_gateway base "${old_compose[@]}"
COMPOSE_PROJECT_NAME="$project" "${old_compose[@]}" up -d --no-build --wait
docker image tag "$gateway_image" "$rollback_image"
suffix="upgrade-${RANDOM}"
for format in oci maven; do
  payload=$(printf '{"name":"%s-%s","members":[{"name":"legacy","type":"hosted","endpoint":"http://host.docker.internal:9","position":0}]}' "$format" "$suffix")
  code=$(status "${admin[@]}" -H 'Content-Type: application/json' --data "$payload" "$gateway_url/api/v1/$format/groups")
  [[ "$code" == 201 ]] || { printf 'Creating base %s Group returned HTTP %s.\n' "$format" "$code" >&2; exit 1; }
done
COMPOSE_PROJECT_NAME="$project" "${old_compose[@]}" down --remove-orphans

build_gateway current "${current_compose[@]}"
COMPOSE_PROJECT_NAME="$project" "${current_compose[@]}" up -d --no-build --wait
for format in oci maven; do
  code=$(status "${admin[@]}" "$gateway_url/api/v1/$format/groups/$format-$suffix")
  [[ "$code" == 200 ]] || { printf 'Current Gateway could not read base %s Group: HTTP %s.\n' "$format" "$code" >&2; exit 1; }
done
for format in raw conan; do
  payload=$(printf '{"name":"%s-%s","anonymous":false,"cacheQuotaBytes":1048576,"members":[{"name":"current","type":"hosted","endpoint":"http://host.docker.internal:9","position":0,"anonymous":false}]}' "$format" "$suffix")
  code=$(status "${admin[@]}" -H 'Content-Type: application/json' --data "$payload" "$gateway_url/api/v1/$format/groups")
  [[ "$code" == 201 ]] || { printf 'Creating current %s Group returned HTTP %s.\n' "$format" "$code" >&2; exit 1; }
done
COMPOSE_PROJECT_NAME="$project" "${current_compose[@]}" down --remove-orphans

docker image tag "$rollback_image" "$gateway_image"
COMPOSE_PROJECT_NAME="$project" "${old_compose[@]}" up -d --no-build --wait
code=$(status "${admin[@]}" "$gateway_url/api/v1/oci/groups/oci-$suffix")
[[ "$code" == 200 ]] || { printf 'Rollback Gateway could not read base OCI Group: HTTP %s.\n' "$code" >&2; exit 1; }
printf '%s\n' 'Upgrade readiness passed: current migration retained legacy OCI/Maven Groups and the rollback binary can read the persisted OCI Group.'
