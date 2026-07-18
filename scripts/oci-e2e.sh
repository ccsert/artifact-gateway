#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

if [[ ! -f .env || ! -f .gitea-fixture/connection.env ]]; then
  printf '%s\n' 'Run requires .env and the seeded Gitea fixture.' >&2
  exit 1
fi

# shellcheck disable=SC1091
source .env
# shellcheck disable=SC1091
source .gitea-fixture/connection.env

for name in GATEWAY_HTTP_PORT GATEWAY_ADMIN_TOKEN GATEWAY_RESOLVER_TOKEN GITEA_HTTP_PORT GITEA_FIXTURE_USERNAME GITEA_FIXTURE_TOKEN GITEA_FIXTURE_ORG; do
  if [[ -z "${!name:-}" ]]; then
    printf 'Missing required %s\n' "$name" >&2
    exit 1
  fi
done

gateway_registry="localhost:${GATEWAY_HTTP_PORT}"
gateway_image="$gateway_registry/$GITEA_FIXTURE_ORG/gateway-fixture:1.0.0"
hosted_endpoint="http://host.docker.internal:${GITEA_HTTP_PORT}"

GATEWAY_ADAPTER_MODE=gitea \
GATEWAY_GITEA_USERNAME="$GITEA_FIXTURE_USERNAME" \
GATEWAY_GITEA_TOKEN="$GITEA_FIXTURE_TOKEN" \
docker compose --env-file .env -f compose.yml up -d --build --wait

group_json=$(printf '{"name":"%s","members":[{"name":"gitea-hosted","type":"hosted","endpoint":"%s","position":0}]}' "$GITEA_FIXTURE_ORG" "$hosted_endpoint")
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  --data "$group_json" \
  "http://$gateway_registry/api/v1/oci/groups")
if [[ "$status" != 201 && "$status" != 409 ]]; then
  printf 'Creating Hosted Group failed with HTTP %s\n' "$status" >&2
  exit 1
fi
if [[ "$status" == 409 ]]; then
  group=$(curl --silent --show-error --fail \
    -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
    "http://$gateway_registry/api/v1/oci/groups/$GITEA_FIXTURE_ORG")
  expected_member="\"name\":\"gitea-hosted\",\"type\":\"hosted\",\"endpoint\":\"$hosted_endpoint\",\"position\":0"
  member_count=$(grep -o '"name"' <<<"$group" | wc -l | tr -d ' ')
  if [[ "$group" != *"\"name\":\"$GITEA_FIXTURE_ORG\""* || "$group" != *"$expected_member"* || "$member_count" != 2 || "$group" == *'"type":"proxy"'* ]]; then
    printf 'Existing Hosted Group does not match the E2E fixture: %s\n' "$group" >&2
    exit 1
  fi
fi

printf '%s' "$GATEWAY_RESOLVER_TOKEN" | docker login "$gateway_registry" --username oci-e2e --password-stdin >/dev/null
docker pull "$gateway_image" >/dev/null

tags=$(docker image inspect --format '{{range .RepoTags}}{{println .}}{{end}}' "$gateway_image")
image_id=$(docker image inspect --format '{{.Id}}' "$gateway_image")
if ! printf '%s\n' "$tags" | grep -Fxq "$gateway_image" || [[ "$image_id" != sha256:* ]]; then
	printf 'Gateway pull did not create the expected local image: tags=%s id=%s\n' "$tags" "$image_id" >&2
	exit 1
fi

printf 'OCI E2E pull passed: %s (%s)\n' "$gateway_image" "$image_id"
