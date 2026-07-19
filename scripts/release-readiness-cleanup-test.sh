#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

environment_file=${GATEWAY_ENV_FILE:-.env}
gateway_container=$(docker compose --env-file "$environment_file" -f compose.yml ps -q gateway)
test -n "$gateway_container" || { printf '%s\n' 'Cleanup test requires a running Gateway.' >&2; exit 1; }

configuration() {
  docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$1" \
    | rg '^(GATEWAY_ADAPTER_MODE|GATEWAY_GITEA_USERNAME|GATEWAY_GITEA_TOKEN|GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS|GATEWAY_RESOLVER_TOKEN)=' \
    | sort
}

assert_configuration_restored() {
  local scenario=$1 current
  gateway_container=$(docker compose --env-file "$environment_file" -f compose.yml ps -q gateway)
  current=$(configuration "$gateway_container")
  [[ "$current" == "$before" ]] || {
    printf 'Gateway configuration changed after %s release readiness run.\n' "$scenario" >&2
    diff <(printf '%s\n' "$before") <(printf '%s\n' "$current") >&2 || true
    exit 1
  }
}

before=$(configuration "$gateway_container")

if RELEASE_READINESS_FAIL_AFTER_TOKEN_ROTATION=1 ./scripts/release-readiness.sh; then
  printf '%s\n' 'Injected release readiness failure unexpectedly succeeded.' >&2
  exit 1
fi
assert_configuration_restored failed

./scripts/release-readiness.sh
assert_configuration_restored successful

printf '%s\n' 'Release readiness cleanup test passed: failed and successful gates restored Gateway configuration.'
