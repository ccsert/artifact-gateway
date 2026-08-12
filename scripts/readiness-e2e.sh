#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd); cd "$root"
env_file=${GATEWAY_ENV_FILE:-.env}; test -f "$env_file"
port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
project="artifact-gateway-ready-${RANDOM}-${RANDOM}"; isolated=$(mktemp)
awk -F= '$1 != "GATEWAY_HTTP_PORT" && $1 != "GATEWAY_POSTGRES_PORT" && $1 != "RUSTFS_API_PORT" && $1 != "RUSTFS_CONSOLE_PORT" {print}' "$env_file" > "$isolated"
gateway_port=$(port); printf 'GATEWAY_HTTP_PORT=%s\nGATEWAY_POSTGRES_PORT=%s\nRUSTFS_API_PORT=%s\nRUSTFS_CONSOLE_PORT=%s\n' "$gateway_port" "$(port)" "$(port)" "$(port)" >> "$isolated"
compose() { COMPOSE_PROJECT_NAME="$project" docker compose --env-file "$isolated" -f compose.yml "$@"; }
trap 'compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -f "$isolated"' EXIT
code() { curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:${gateway_port}/readyz"; }
expect() { [[ $(code) == "$1" ]] || { printf 'readyz=%s, want %s\n' "$(code)" "$1" >&2; exit 1; }; }
eventually() { for _ in $(seq 1 30); do [[ $(code) == "$1" ]] && return; sleep 1; done; expect "$1"; }
compose up -d --build --wait; expect 204
for service in rustfs postgres; do
  compose stop "$service"; sleep 1; expect 503
  compose start "$service"; eventually 204
done
printf '%s\n' 'Readiness E2E passed: RustFS and PostgreSQL faults return 503 and recover to 204.'
