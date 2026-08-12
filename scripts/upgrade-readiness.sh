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
upstream_dir=$(mktemp -d)
# macOS exposes /var as a symlink to /private/var. Git records the physical
# worktree path, so normalize it before registration and cleanup.
old_tree=$(cd "$old_tree" && pwd -P)
isolated_environment=$(mktemp)
gateway_port=$(free_port)
object_api_port=$(free_port)
object_console_port=$(free_port)
rustfs_api_port=$(free_port)
rustfs_console_port=$(free_port)
upstream_port=$(free_port)
upstream_pid=

cleanup() {
  COMPOSE_PROJECT_NAME="$project" docker compose --env-file "$isolated_environment" -f compose.yml down -v --remove-orphans >/dev/null 2>&1 || true
  test -f "$old_tree/compose.yml" && COMPOSE_PROJECT_NAME="$project" docker compose --env-file "$isolated_environment" -f "$old_tree/compose.yml" down -v --remove-orphans >/dev/null 2>&1 || true
  docker image rm "$rollback_image" "$gateway_image" >/dev/null 2>&1 || true
  git worktree remove --force "$old_tree" >/dev/null 2>&1 || rm -rf "$old_tree"
  rm -f "$isolated_environment"
  if [[ -n "$upstream_pid" ]]; then
    kill "$upstream_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$upstream_dir"
}
trap cleanup EXIT

git worktree add --detach "$old_tree" "$base_ref" >/dev/null
awk -F= '$1 != "GATEWAY_HTTP_PORT" && $1 != "GATEWAY_POSTGRES_PORT" && $1 != "MINIO_API_PORT" && $1 != "MINIO_CONSOLE_PORT" && $1 != "RUSTFS_API_PORT" && $1 != "RUSTFS_CONSOLE_PORT" && $1 != "GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS" && $1 != "GATEWAY_REPOSITORY_READERS" && $1 != "COMPOSE_PROFILES" { print }' "$environment_file" >"$isolated_environment"
rustfs_access_key=${RUSTFS_ACCESS_KEY:-$(awk -F= '$1 == "RUSTFS_ACCESS_KEY" { print substr($0, index($0, "=") + 1) }' "$isolated_environment")}
rustfs_secret_key=${RUSTFS_SECRET_KEY:-$(awk -F= '$1 == "RUSTFS_SECRET_KEY" { print substr($0, index($0, "=") + 1) }' "$isolated_environment")}
test -n "$rustfs_access_key"
test -n "$rustfs_secret_key"
printf 'GATEWAY_HTTP_PORT=%s\nGATEWAY_POSTGRES_PORT=%s\nMINIO_API_PORT=%s\nMINIO_CONSOLE_PORT=%s\nRUSTFS_API_PORT=%s\nRUSTFS_CONSOLE_PORT=%s\nMINIO_ROOT_USER=%s\nMINIO_ROOT_PASSWORD=%s\nGATEWAY_MAVEN_PROXY_ALLOWED_HOSTS=host.docker.internal:%s\n' \
  "$gateway_port" "$(free_port)" "$object_api_port" "$object_console_port" "$rustfs_api_port" "$rustfs_console_port" \
  "$rustfs_access_key" "$rustfs_secret_key" "$upstream_port" >>"$isolated_environment"
gateway_url="http://127.0.0.1:${gateway_port}"

old_compose=(docker compose --env-file "$isolated_environment" -f "$old_tree/compose.yml")
current_compose=(docker compose --env-file "$isolated_environment" -f compose.yml -f compose.upgrade-minio.yml)
status() { curl --silent --show-error --output /dev/null --write-out '%{http_code}' "$@"; }
admin=(-H "Authorization: Bearer $(awk -F= '$1 == "GATEWAY_ADMIN_TOKEN" { print substr($0, index($0, "=") + 1) }' "$isolated_environment")")
resolver_token=$(awk -F= '$1 == "GATEWAY_RESOLVER_TOKEN" { print substr($0, index($0, "=") + 1) }' "$isolated_environment")
resolver=(-u "upgrade-readiness:$resolver_token")

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

maven_path='com/example/upgrade/1.0/upgrade-1.0.jar'
maven_body="upgrade-object-${suffix}"
mkdir -p "$upstream_dir/$(dirname "$maven_path")"
printf '%s' "$maven_body" >"$upstream_dir/$maven_path"
python3 -m http.server "$upstream_port" --bind 0.0.0.0 --directory "$upstream_dir" >/dev/null 2>&1 &
upstream_pid=$!
for _ in $(seq 1 30); do
  curl --silent --show-error --fail "http://127.0.0.1:$upstream_port/$maven_path" >/dev/null 2>&1 && break
  sleep 0.1
done
curl --silent --show-error --fail "http://127.0.0.1:$upstream_port/$maven_path" >/dev/null
maven_group="maven-object-$suffix"
payload=$(printf '{"name":"%s","members":[{"name":"fixture","type":"proxy","endpoint":"http://host.docker.internal:%s","position":0}]}' "$maven_group" "$upstream_port")
code=$(status "${admin[@]}" -H 'Content-Type: application/json' --data "$payload" "$gateway_url/api/v1/maven/groups")
[[ "$code" == 201 ]] || { printf 'Creating base Maven object Group returned HTTP %s.\n' "$code" >&2; exit 1; }
cached=$(curl --silent --show-error --fail "${resolver[@]}" "$gateway_url/maven/$maven_group/$maven_path")
[[ "$cached" == "$maven_body" ]] || { printf '%s\n' 'Base Gateway did not cache the Maven verification object.' >&2; exit 1; }
kill "$upstream_pid"
wait "$upstream_pid" 2>/dev/null || true
upstream_pid=
COMPOSE_PROJECT_NAME="$project" "${old_compose[@]}" stop gateway

build_gateway current "${current_compose[@]}"
if ! COMPOSE_PROJECT_NAME="$project" "${current_compose[@]}" up -d --no-build --wait; then
  COMPOSE_PROJECT_NAME="$project" "${current_compose[@]}" logs --no-color gateway >&2 || true
  exit 1
fi
for format in oci maven; do
  code=$(status "${admin[@]}" "$gateway_url/api/v1/$format/groups/$format-$suffix")
  [[ "$code" == 200 ]] || { printf 'Current Gateway could not read base %s Group: HTTP %s.\n' "$format" "$code" >&2; exit 1; }
done
cached=$(curl --silent --show-error --fail "${resolver[@]}" "$gateway_url/maven/$maven_group/$maven_path")
[[ "$cached" == "$maven_body" ]] || { printf '%s\n' 'Current Gateway could not read the cached Maven object from legacy S3 storage.' >&2; exit 1; }
for format in raw conan; do
  payload=$(printf '{"name":"%s-%s","anonymous":false,"cacheQuotaBytes":1048576,"members":[{"name":"current","type":"hosted","endpoint":"http://host.docker.internal:9","position":0,"anonymous":false}]}' "$format" "$suffix")
  code=$(status "${admin[@]}" -H 'Content-Type: application/json' --data "$payload" "$gateway_url/api/v1/$format/groups")
  [[ "$code" == 201 ]] || { printf 'Creating current %s Group returned HTTP %s.\n' "$format" "$code" >&2; exit 1; }
done
COMPOSE_PROJECT_NAME="$project" "${current_compose[@]}" stop gateway rustfs

docker image tag "$rollback_image" "$gateway_image"
COMPOSE_PROJECT_NAME="$project" "${old_compose[@]}" up -d --no-build --wait
code=$(status "${admin[@]}" "$gateway_url/api/v1/oci/groups/oci-$suffix")
[[ "$code" == 200 ]] || { printf 'Rollback Gateway could not read base OCI Group: HTTP %s.\n' "$code" >&2; exit 1; }
cached=$(curl --silent --show-error --fail "${resolver[@]}" "$gateway_url/maven/$maven_group/$maven_path")
[[ "$cached" == "$maven_body" ]] || { printf '%s\n' 'Rollback Gateway could not read the cached Maven object from the shared S3 store.' >&2; exit 1; }
printf '%s\n' 'Upgrade readiness passed: one shared legacy S3 store preserved cached Maven bytes across current migration and binary rollback; the copied RustFS cutover remains a separate gate.'
