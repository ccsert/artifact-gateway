#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

base_ref=${GATEWAY_UPGRADE_FROM_REF:-324aba95}
environment_file=${GATEWAY_ENV_FILE:-.env}
test -f "$environment_file" || { printf '%s\n' 'Upgrade readiness requires a configured environment file.' >&2; exit 1; }
git cat-file -e "$base_ref^{commit}"

free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'; }
project="artifact-gateway-upgrade-${RANDOM}-${RANDOM}"
gateway_image="${project}-gateway:latest"
rollback_image="${project}-rollback-gateway:local"
current_image="${project}-current-gateway:local"
old_tree=$(mktemp -d)
upstream_dir=$(mktemp -d)
go_workspace=$(mktemp -d)
compose_override=$(mktemp)
# macOS exposes /var as a symlink to /private/var. Git records the physical
# worktree path, so normalize it before registration and cleanup.
old_tree=$(cd "$old_tree" && pwd -P)
upstream_dir=$(cd "$upstream_dir" && pwd -P)
isolated_environment=$(mktemp)
gateway_port=$(free_port)
rustfs_api_port=$(free_port)
rustfs_console_port=$(free_port)
upstream_port=$(free_port)
proxy_port=$(free_port)
upstream_pid=

cleanup() {
  COMPOSE_PROJECT_NAME="$project" docker compose --env-file "$isolated_environment" -f compose.yml down -v --remove-orphans >/dev/null 2>&1 || true
  test -f "$old_tree/compose.yml" && COMPOSE_PROJECT_NAME="$project" docker compose --env-file "$isolated_environment" -f "$old_tree/compose.yml" down -v --remove-orphans >/dev/null 2>&1 || true
  docker image rm "$rollback_image" "$current_image" "$gateway_image" >/dev/null 2>&1 || true
  git worktree remove --force "$old_tree" >/dev/null 2>&1 || rm -rf "$old_tree"
  rm -f "$isolated_environment"
  if [[ -n "$upstream_pid" ]]; then
    kill "$upstream_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$upstream_dir"
  chmod -R u+w "$go_workspace" >/dev/null 2>&1 || true
  rm -rf "$go_workspace"
  rm -f "$compose_override"
}
trap cleanup EXIT

git worktree add --detach "$old_tree" "$base_ref" >/dev/null
upstream_cert="$upstream_dir/upstream.crt"
upstream_key="$upstream_dir/upstream.key"
openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days 1 \
  -subj '/CN=host.docker.internal' \
  -addext 'subjectAltName=DNS:host.docker.internal' \
  -addext 'basicConstraints=critical,CA:TRUE' \
  -keyout "$upstream_key" -out "$upstream_cert" >/dev/null 2>&1
# Compose, rather than this shell, expands the CA source path later.
# shellcheck disable=SC2016
printf '%s\n' \
  'services:' \
  '  gateway:' \
  '    environment:' \
  '      NO_PROXY: postgres,rustfs,127.0.0.1,localhost' \
  '    volumes:' \
  '      - "${GATEWAY_UPGRADE_CA_FILE}:/etc/ssl/certs/ca-certificates.crt:ro"' >"$compose_override"
awk -F= '$1 != "GATEWAY_HTTP_PORT" && $1 != "GATEWAY_POSTGRES_PORT" && $1 != "RUSTFS_API_PORT" && $1 != "RUSTFS_CONSOLE_PORT" && $1 != "RUSTFS_ACCESS_KEY" && $1 != "RUSTFS_SECRET_KEY" && $1 != "RUSTFS_RPC_SECRET" && $1 != "GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS" && $1 != "GATEWAY_EGRESS_PROXY" && $1 != "GATEWAY_REPOSITORY_READERS" && $1 != "COMPOSE_PROFILES" { print }' "$environment_file" >"$isolated_environment"
rustfs_access_key=${RUSTFS_ACCESS_KEY:-$(awk -F= '$1 == "RUSTFS_ACCESS_KEY" { print substr($0, index($0, "=") + 1) }' "$environment_file")}
rustfs_secret_key=${RUSTFS_SECRET_KEY:-$(awk -F= '$1 == "RUSTFS_SECRET_KEY" { print substr($0, index($0, "=") + 1) }' "$environment_file")}
rustfs_rpc_secret=${RUSTFS_RPC_SECRET:-$(awk -F= '$1 == "RUSTFS_RPC_SECRET" { print substr($0, index($0, "=") + 1) }' "$environment_file")}
test -n "$rustfs_access_key"
test -n "$rustfs_secret_key"
test -n "$rustfs_rpc_secret"
printf 'GATEWAY_HTTP_PORT=%s\nGATEWAY_POSTGRES_PORT=%s\nRUSTFS_API_PORT=%s\nRUSTFS_CONSOLE_PORT=%s\nRUSTFS_ACCESS_KEY=%s\nRUSTFS_SECRET_KEY=%s\nRUSTFS_RPC_SECRET=%s\nGATEWAY_MAVEN_PROXY_ALLOWED_HOSTS=host.docker.internal:%s\nGATEWAY_EGRESS_PROXY=http://host.docker.internal:%s\nGATEWAY_UPGRADE_CA_FILE=%s\n' \
  "$gateway_port" "$(free_port)" "$rustfs_api_port" "$rustfs_console_port" \
  "$rustfs_access_key" "$rustfs_secret_key" "$rustfs_rpc_secret" "$upstream_port" "$proxy_port" "$upstream_cert" >>"$isolated_environment"
gateway_url="http://127.0.0.1:${gateway_port}"

old_compose=(docker compose --env-file "$isolated_environment" -f "$old_tree/compose.yml" -f "$compose_override")
current_compose=(docker compose --env-file "$isolated_environment" -f compose.yml -f "$compose_override")
status() { curl --silent --show-error --output /dev/null --write-out '%{http_code}' "$@"; }
admin=(-H "Authorization: Bearer $(awk -F= '$1 == "GATEWAY_ADMIN_TOKEN" { print substr($0, index($0, "=") + 1) }' "$isolated_environment")")
resolver_token=$(awk -F= '$1 == "GATEWAY_RESOLVER_TOKEN" { print substr($0, index($0, "=") + 1) }' "$isolated_environment")
resolver=(-u "upgrade-readiness:$resolver_token")

assert_go_module_download() {
  local repository_name=$1 module_path=$2 version=$3 cache_name=$4 output
  mkdir -p "$go_workspace/$cache_name/mod" "$go_workspace/$cache_name/build"
  if ! output=$(cd "$go_workspace" && \
    GOPROXY="$gateway_url/go/$repository_name" GOSUMDB=off GONOSUMDB='*' GONOPROXY=none \
      GOMODCACHE="$go_workspace/$cache_name/mod" GOCACHE="$go_workspace/$cache_name/build" \
      go mod download -json "$module_path@$version" 2>&1); then
    printf 'Go module download failed for %s@%s during upgrade readiness:\n%s\n' "$module_path" "$version" "$output" >&2
    if [[ -f "$upstream_dir/server.log" ]]; then
      printf '%s\n' 'Local HTTPS fixture log:' >&2
      cat "$upstream_dir/server.log" >&2
    fi
    return 1
  fi
  python3 -c '
import json
import sys

module_path, version = sys.argv[1:]
value = json.load(sys.stdin)
if value.get("Path") != module_path or value.get("Version") != version or value.get("Error"):
    raise SystemExit(f"unexpected go mod download result: {value}")
' "$module_path" "$version" <<<"$output"
}
write_go_module_zip() {
  local output=$1 module_path=$2 version=$3 marker=$4
  python3 - "$output" "$module_path" "$version" "$marker" <<'PY'
import sys
import zipfile

output, module_path, version, marker = sys.argv[1:]
prefix = f"{module_path}@{version}/"
with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED) as archive:
    archive.writestr(prefix + "go.mod", f"module {module_path}\n\ngo 1.20\n")
    archive.writestr(prefix + "upgrade.go", f'package upgrade\n\nconst Marker = "{marker}"\n')
PY
}
enable_anonymous_access() {
  local policy version
  policy=$(curl --silent --show-error --fail "${admin[@]}" "$gateway_url/api/v2/anonymous-access-policy")
  version=$(python3 -c 'import json, sys; print(json.load(sys.stdin)["version"])' <<<"$policy")
  curl --silent --show-error --fail --request PUT "${admin[@]}" -H 'Content-Type: application/json' -H "If-Match: $version" \
    --data "{\"version\":\"$version\",\"enabled\":true}" "$gateway_url/api/v2/anonymous-access-policy" >/dev/null
}

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
go_proxy_repository="go-proxy-$suffix"
go_proxy_module="example.com/upgrade/proxy"
go_proxy_version="v1.0.0"
for format in oci maven; do
  payload=$(printf '{"name":"%s-%s","members":[{"name":"legacy","type":"hosted","endpoint":"http://host.docker.internal:9","position":0}]}' "$format" "$suffix")
  code=$(status "${admin[@]}" -H 'Content-Type: application/json' --data "$payload" "$gateway_url/api/v1/$format/groups")
  [[ "$code" == 201 ]] || { printf 'Creating base %s Group returned HTTP %s.\n' "$format" "$code" >&2; exit 1; }
done

maven_path='com/example/upgrade/1.0/upgrade-1.0.jar'
maven_body="upgrade-object-${suffix}"
mkdir -p "$upstream_dir/$(dirname "$maven_path")"
printf '%s' "$maven_body" >"$upstream_dir/$maven_path"
go_proxy_dir="$upstream_dir/$go_proxy_module/@v"
mkdir -p "$go_proxy_dir"
printf '%s\n' "$go_proxy_version" >"$go_proxy_dir/list"
printf '{"Version":"%s","Time":"2026-08-19T00:00:00Z"}\n' "$go_proxy_version" >"$go_proxy_dir/$go_proxy_version.info"
printf 'module %s\n\ngo 1.20\n' "$go_proxy_module" >"$go_proxy_dir/$go_proxy_version.mod"
go_proxy_archive="$go_workspace/$go_proxy_version-proxy.zip"
write_go_module_zip "$go_proxy_archive" "$go_proxy_module" "$go_proxy_version" base-proxy
cp "$go_proxy_archive" "$go_proxy_dir/$go_proxy_version.zip"
python3 - "$upstream_port" "$proxy_port" "$upstream_dir" "$upstream_cert" "$upstream_key" >"$upstream_dir/server.log" 2>&1 <<'PY' &
import functools
import http.server
import select
import socket
import socketserver
import ssl
import sys
import threading

upstream_port, proxy_port, directory, certificate, key = sys.argv[1:]
handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=directory)
server = http.server.ThreadingHTTPServer(("0.0.0.0", int(upstream_port)), handler)
context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
context.load_cert_chain(certificate, key)
server.socket = context.wrap_socket(server.socket, server_side=True)

class ConnectProxy(socketserver.StreamRequestHandler):
    def handle(self):
        request = self.rfile.readline().decode("ascii", "replace").strip().split()
        while self.rfile.readline().strip():
            pass
        expected = f"host.docker.internal:{upstream_port}"
        if len(request) < 2 or request[0] != "CONNECT" or request[1] != expected:
            self.wfile.write(b"HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
            return
        with socket.create_connection(("127.0.0.1", int(upstream_port))) as remote:
            self.wfile.write(b"HTTP/1.1 200 Connection Established\r\n\r\n")
            peers = {self.connection: remote, remote: self.connection}
            while True:
                readable, _, _ = select.select(list(peers), [], [], 30)
                if not readable:
                    return
                for source in readable:
                    data = source.recv(65536)
                    if not data:
                        return
                    peers[source].sendall(data)

class ThreadingProxy(socketserver.ThreadingTCPServer):
    allow_reuse_address = True

threading.Thread(target=server.serve_forever, daemon=True).start()
with ThreadingProxy(("0.0.0.0", int(proxy_port)), ConnectProxy) as proxy:
    proxy.serve_forever()
PY
upstream_pid=$!
for _ in $(seq 1 30); do
  curl --noproxy '*' --silent --show-error --fail --cacert "$upstream_cert" \
    --resolve "host.docker.internal:$upstream_port:127.0.0.1" \
    "https://host.docker.internal:$upstream_port/$maven_path" >/dev/null 2>&1 && break
  sleep 0.1
done
curl --noproxy '*' --silent --show-error --fail --cacert "$upstream_cert" \
  --resolve "host.docker.internal:$upstream_port:127.0.0.1" \
  "https://host.docker.internal:$upstream_port/$maven_path" >/dev/null
maven_group="maven-object-$suffix"
payload=$(printf '{"name":"%s","members":[{"name":"fixture","type":"proxy","endpoint":"https://host.docker.internal:%s","position":0}]}' "$maven_group" "$upstream_port")
code=$(status "${admin[@]}" -H 'Content-Type: application/json' --data "$payload" "$gateway_url/api/v1/maven/groups")
[[ "$code" == 201 ]] || { printf 'Creating base Maven object Group returned HTTP %s.\n' "$code" >&2; exit 1; }
enable_anonymous_access
go_proxy_payload=$(printf '{"name":"%s","format":"go","type":"proxy","endpoint":"https://host.docker.internal:%s","allowedHosts":["host.docker.internal"],"anonymousRead":true}' "$go_proxy_repository" "$upstream_port")
go_proxy_response="$go_workspace/base-go-proxy.json"
code=$(curl --silent --show-error --output "$go_proxy_response" --write-out '%{http_code}' \
  "${admin[@]}" -H 'Content-Type: application/json' -H "Idempotency-Key: go-proxy-$suffix" \
  --data "$go_proxy_payload" "$gateway_url/api/v2/repositories")
if [[ "$code" != 201 ]]; then
  printf 'Creating base Go Proxy Repository returned HTTP %s:\n' "$code" >&2
  cat "$go_proxy_response" >&2
  exit 1
fi
assert_go_module_download "$go_proxy_repository" "$go_proxy_module" "$go_proxy_version" base-proxy
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
docker image tag "$gateway_image" "$current_image"
for format in oci maven; do
  code=$(status "${admin[@]}" "$gateway_url/api/v1/$format/groups/$format-$suffix")
  [[ "$code" == 200 ]] || { printf 'Current Gateway could not read base %s Group: HTTP %s.\n' "$format" "$code" >&2; exit 1; }
done
cached=$(curl --silent --show-error --fail "${resolver[@]}" "$gateway_url/maven/$maven_group/$maven_path")
[[ "$cached" == "$maven_body" ]] || { printf '%s\n' 'Current Gateway could not read the cached Maven object from RustFS.' >&2; exit 1; }
assert_go_module_download "$go_proxy_repository" "$go_proxy_module" "$go_proxy_version" current-proxy-offline
go_hosted_repository="go-hosted-$suffix"
go_hosted_module="example.com/upgrade/hosted"
go_hosted_version="v1.0.0"
go_hosted_payload=$(printf '{"name":"%s","format":"go","anonymousRead":true}' "$go_hosted_repository")
code=$(status "${admin[@]}" -H 'Content-Type: application/json' -H "Idempotency-Key: go-hosted-$suffix" \
  --data "$go_hosted_payload" "$gateway_url/api/v2/repositories")
[[ "$code" == 201 ]] || { printf 'Creating current Go Hosted Repository returned HTTP %s.\n' "$code" >&2; exit 1; }
go_hosted_archive="$go_workspace/$go_hosted_version-hosted.zip"
write_go_module_zip "$go_hosted_archive" "$go_hosted_module" "$go_hosted_version" current-hosted
code=$(status "${admin[@]}" --request PUT --data-binary "@$go_hosted_archive" \
  "$gateway_url/go/$go_hosted_repository/$go_hosted_module/@v/$go_hosted_version.zip")
[[ "$code" == 201 ]] || { printf 'Publishing current Go Hosted module returned HTTP %s.\n' "$code" >&2; exit 1; }
assert_go_module_download "$go_hosted_repository" "$go_hosted_module" "$go_hosted_version" current-hosted
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
[[ "$cached" == "$maven_body" ]] || { printf '%s\n' 'Rollback Gateway could not read the cached Maven object from the shared RustFS store.' >&2; exit 1; }
assert_go_module_download "$go_proxy_repository" "$go_proxy_module" "$go_proxy_version" rollback-proxy-offline

COMPOSE_PROJECT_NAME="$project" "${old_compose[@]}" stop gateway
docker image tag "$current_image" "$gateway_image"
COMPOSE_PROJECT_NAME="$project" "${current_compose[@]}" up -d --no-build --force-recreate --wait gateway
assert_go_module_download "$go_hosted_repository" "$go_hosted_module" "$go_hosted_version" current-hosted-after-rollback
assert_go_module_download "$go_proxy_repository" "$go_proxy_module" "$go_proxy_version" current-proxy-after-rollback
printf '%s\n' 'Upgrade readiness passed: one shared PostgreSQL/RustFS state preserved Go Proxy cache and Go Hosted publication across current migration, binary rollback, and forward recovery.'
