#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd); cd "$root"
env_file=${GATEWAY_ENV_FILE:-.env}; test -f "$env_file"
port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
project="artifact-gateway-cacheops-${RANDOM}-${RANDOM}"; isolated=$(mktemp)
gateway_port=$(port); awk -F= '$1 != "GATEWAY_HTTP_PORT" && $1 != "GATEWAY_POSTGRES_PORT" && $1 != "MINIO_API_PORT" && $1 != "MINIO_CONSOLE_PORT" {print}' "$env_file" > "$isolated"
printf 'GATEWAY_HTTP_PORT=%s\nGATEWAY_POSTGRES_PORT=%s\nMINIO_API_PORT=%s\nMINIO_CONSOLE_PORT=%s\n' "$gateway_port" "$(port)" "$(port)" "$(port)" >> "$isolated"
compose() { COMPOSE_PROJECT_NAME="$project" docker compose --env-file "$isolated" -f compose.yml "$@"; }
trap 'compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -f "$isolated"' EXIT
source "$isolated"
base="http://127.0.0.1:${gateway_port}"
status() { curl --silent --show-error --output "$2" --write-out '%{http_code}' "$1" "${@:3}"; }
compose up -d --build --wait
tmp=$(mktemp); trap 'compose down -v --remove-orphans >/dev/null 2>&1 || true; rm -f "$isolated" "$tmp"' EXIT
code=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' -H "Authorization: Bearer $GATEWAY_RESOLVER_TOKEN" "$base/api/v1/operations/cache")
[[ "$code" == 403 ]] || { printf 'resolver cache status HTTP %s, want 403\n' "$code" >&2; exit 1; }
code=$(curl --silent --show-error --output "$tmp" --write-out '%{http_code}' -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$base/api/v1/operations/cache")
[[ "$code" == 200 ]] || { printf 'admin cache status HTTP %s: %s\n' "$code" "$(cat "$tmp")" >&2; exit 1; }
before=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("successful_runs",0))' "$tmp")
code=$(curl --silent --show-error --output "$tmp" --write-out '%{http_code}' -X POST -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$base/api/v1/operations/cache/collect")
[[ "$code" == 204 ]] || { printf 'cache collect HTTP %s: %s\n' "$code" "$(cat "$tmp")" >&2; exit 1; }
code=$(curl --silent --show-error --output "$tmp" --write-out '%{http_code}' -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$base/api/v1/operations/cache")
[[ "$code" == 200 ]] || { printf 'post-collect cache status HTTP %s: %s\n' "$code" "$(cat "$tmp")" >&2; exit 1; }
after=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("successful_runs",0))' "$tmp")
[[ "$after" -gt "$before" ]] || { printf 'successful_runs did not increase: before=%s after=%s\n' "$before" "$after" >&2; exit 1; }
printf 'Cache operations E2E passed: resolver denied, admin collection succeeded, successful_runs increased.\n'
