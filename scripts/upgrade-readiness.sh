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
  compose "$repo_root" down -v --remove-orphans >/dev/null 2>&1 || true
  git worktree remove --force "$previous_tree" >/dev/null 2>&1 || true
  rm -f "$upgrade_environment_file"
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

printf 'Upgrade gate passed: %s -> current -> %s; OCI and Maven clients passed before rollback.\n' \
  "$previous_ref" "$previous_ref"
