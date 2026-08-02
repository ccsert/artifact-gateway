#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
env_file=${GATEWAY_ENV_FILE:-"$root/.env"}
test -f "$env_file" || { printf '%s\n' 'Console E2E requires a configured environment file.' >&2; exit 1; }

# shellcheck disable=SC1090
source "$env_file"
gateway_port=${GATEWAY_HTTP_PORT:-8080}
curl --silent --show-error --fail "http://127.0.0.1:${gateway_port}/readyz" >/dev/null
console_port=$(python3 -c 'import socket; listener = socket.socket(); listener.bind(("127.0.0.1", 0)); print(listener.getsockname()[1]); listener.close()')
GATEWAY_ADMIN_TOKEN="$GATEWAY_ADMIN_TOKEN" PLAYWRIGHT_PORT="$console_port" VITE_GATEWAY_PROXY_TARGET="http://127.0.0.1:${gateway_port}" npm --prefix "$root/console" run e2e
