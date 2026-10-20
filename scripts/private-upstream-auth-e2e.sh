#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
for binary in docker go mvn oras python3; do
  command -v "$binary" >/dev/null || { printf 'Private upstream acceptance requires %s\n' "$binary" >&2; exit 1; }
done
context=$(docker context show)
endpoint=$(docker context inspect "$context" --format '{{(index .Endpoints "docker").Host}}')
case "$endpoint" in
  unix://*) ;;
  *) printf 'Private upstream acceptance requires a local Docker Unix socket.\n' >&2; exit 1 ;;
esac
project="artifact-gateway-private-auth-${BASHPID}-${RANDOM}"
compose=(docker --context "$context" compose --env-file /dev/null --project-name "$project" -f compose.integration.yml)
cleanup() {
  local result=$?
  trap - EXIT
  "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || result=1
  local remaining
  remaining=$(docker --context "$context" ps -aq --filter "label=com.docker.compose.project=$project")
  if [[ -n "$remaining" ]]; then
    printf 'Owned private upstream containers remain: %s\n' "$remaining" >&2
    result=1
  fi
  local volumes networks
  volumes=$(docker --context "$context" volume ls -q --filter "label=com.docker.compose.project=$project")
  networks=$(docker --context "$context" network ls -q --filter "label=com.docker.compose.project=$project")
  if [[ -n "$volumes" || -n "$networks" ]]; then result=1; fi
  printf 'Private upstream owned project=%s cleanup=%s containers=%s volumes=%s networks=%s\n' "$project" "$result" "${remaining:-0}" "${volumes:-0}" "${networks:-0}"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
printf 'Private upstream owned project=%s docker-context=%s\n' "$project" "$context"
"${compose[@]}" up -d --wait postgres rustfs
"${compose[@]}" run --rm --no-deps rustfs-ready
"${compose[@]}" run --rm --no-deps migrate
postgres_address=$("${compose[@]}" port postgres 5432)
rustfs_address=$("${compose[@]}" port rustfs 9000)
TEST_DATABASE_URL="postgres://gateway:integration-password@${postgres_address}/gateway_test?sslmode=disable" \
TEST_RUSTFS_ENDPOINT="http://${rustfs_address}" \
TEST_RUSTFS_ACCESS_KEY=integration-rustfs \
TEST_RUSTFS_SECRET_KEY=integration-password \
  go test -json -count=1 -tags='integration mavenclient ociclient' ./internal/app \
  -run '^Test(PostgresRustFSPrivateProxy(Credentials|OfficialClients)|Private.*)$'
