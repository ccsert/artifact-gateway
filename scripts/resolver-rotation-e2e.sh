#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd); cd "$root"
env_file=${GATEWAY_ENV_FILE:-.env}; test -f "$env_file"
port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
project="artifact-gateway-rotate-${RANDOM}-${RANDOM}"; isolated=$(mktemp)
gateway_port=$(port); rustfs_api=$(port); rustfs_console=$(port)
awk -F= '$1 != "GATEWAY_HTTP_PORT" && $1 != "GATEWAY_POSTGRES_PORT" && $1 != "RUSTFS_API_PORT" && $1 != "RUSTFS_CONSOLE_PORT" && $1 != "GATEWAY_RESOLVER_TOKEN" {print}' "$env_file" > "$isolated"
printf 'GATEWAY_HTTP_PORT=%s\nGATEWAY_POSTGRES_PORT=%s\nRUSTFS_API_PORT=%s\nRUSTFS_CONSOLE_PORT=%s\nGATEWAY_RESOLVER_TOKEN=resolver-before-rotation\n' "$gateway_port" "$(port)" "$rustfs_api" "$rustfs_console" >> "$isolated"
compose() { COMPOSE_PROJECT_NAME="$project" docker compose --env-file "$isolated" -f compose.yml "$@"; }
trap 'compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -f "$isolated"' EXIT
base="http://127.0.0.1:${gateway_port}"
token() { curl --silent --show-error --fail --user "$1:$2" "$base/auth/token" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'; }
expect() { local want=$1; shift; local got; got=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' "$@"); [[ "$got" == "$want" ]] || { printf 'HTTP %s, want %s for %s\n' "$got" "$want" "$*" >&2; exit 1; }; }
compose up -d --build --wait
old_token=$(token fixture resolver-before-rotation); test -n "$old_token"
expect 404 -H "Authorization: Bearer $old_token" "$base/v2/rotation-missing/manifests/latest"
awk -F= '$1 != "GATEWAY_RESOLVER_TOKEN" {print}' "$isolated" > "$isolated.next"
printf 'GATEWAY_RESOLVER_TOKEN=resolver-after-rotation\n' >> "$isolated.next"; mv "$isolated.next" "$isolated"
compose up -d --build --force-recreate --no-deps gateway --wait
expect 401 -H "Authorization: Bearer $old_token" "$base/v2/rotation-missing/manifests/latest"
new_token=$(token fixture resolver-after-rotation); test -n "$new_token"
expect 404 -H "Authorization: Bearer $new_token" "$base/v2/rotation-missing/manifests/latest"
printf '%s\n' 'Resolver rotation E2E passed: old bearer rejected after restart and new bearer accepted.'
