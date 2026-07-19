#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

environment_file=${GATEWAY_ENV_FILE:-.env}
gateway_container=$(docker compose --env-file "$environment_file" -f compose.yml ps -q gateway)
test -n "$gateway_container" || { printf '%s\n' 'Cleanup test requires a running Gateway.' >&2; exit 1; }

configuration() {
  docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$1" \
    | rg '^(GATEWAY_ADAPTER_MODE|GATEWAY_GITEA_USERNAME|GATEWAY_GITEA_TOKEN|GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS)=' \
    | sort
}

before=$(configuration "$gateway_container")
if MAVEN_E2E_FAIL_AFTER_ALLOWLIST_TIGHTENING=1 ./scripts/maven-e2e.sh; then
  printf '%s\n' 'Injected Maven E2E failure unexpectedly succeeded.' >&2
  exit 1
fi

gateway_container=$(docker compose --env-file "$environment_file" -f compose.yml ps -q gateway)
after=$(configuration "$gateway_container")
[[ "$after" == "$before" ]] || { printf '%s\n' 'Gateway configuration changed after failed Maven E2E.' >&2; exit 1; }

./scripts/maven-e2e.sh
gateway_container=$(docker compose --env-file "$environment_file" -f compose.yml ps -q gateway)
after=$(configuration "$gateway_container")
[[ "$after" == "$before" ]] || { printf '%s\n' 'Gateway configuration changed after successful Maven E2E.' >&2; exit 1; }

printf '%s\n' 'Maven E2E cleanup test passed: failed and successful runs restored Gateway configuration.'
