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

for name in GATEWAY_HTTP_PORT GATEWAY_ADMIN_TOKEN GATEWAY_RESOLVER_TOKEN GITEA_HTTP_PORT GITEA_FIXTURE_USERNAME GITEA_FIXTURE_TOKEN GITEA_FIXTURE_ORG GITEA_MAVEN_GROUP GITEA_MAVEN_ARTIFACT GITEA_MAVEN_VERSION; do
  if [[ -z "${!name:-}" ]]; then
    printf 'Missing required %s\n' "$name" >&2
    exit 1
  fi
done

gateway_url="http://localhost:${GATEWAY_HTTP_PORT}"
proxy_port=${GATEWAY_MAVEN_PROXY_PORT:-18081}
proxy_host="host.docker.internal:${proxy_port}"
proxy_group="${GITEA_FIXTURE_ORG}-proxy"
proxy_endpoint="http://${proxy_host}"
group_json=$(printf '{"name":"%s","members":[{"name":"fixture-proxy","type":"proxy","endpoint":"%s","position":0}]}' "$proxy_group" "$proxy_endpoint")

workdir=$(mktemp -d)
proxy_pid=""
cleanup() {
  if [[ -n "$proxy_pid" ]]; then kill "$proxy_pid" 2>/dev/null || true; fi
  rm -rf "$workdir"
}
trap cleanup EXIT
python3 -m http.server "$proxy_port" --bind 0.0.0.0 --directory .gitea-fixture/maven >/dev/null 2>&1 &
proxy_pid=$!
until curl --silent --show-error --fail "http://localhost:${proxy_port}/" >/dev/null; do sleep 1; done

GATEWAY_ADAPTER_MODE=gitea \
GATEWAY_GITEA_USERNAME="$GITEA_FIXTURE_USERNAME" \
GATEWAY_GITEA_TOKEN="$GITEA_FIXTURE_TOKEN" \
GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS="$proxy_host" \
docker compose --env-file .env -f compose.yml up -d --build --wait

status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  --data "$group_json" \
  "$gateway_url/api/v1/maven/groups")
if [[ "$status" != 201 && "$status" != 409 ]]; then
  printf 'Creating Maven Hosted Group failed with HTTP %s\n' "$status" >&2
  exit 1
fi

repository_url="http://host.docker.internal:${GATEWAY_HTTP_PORT}/maven/${proxy_group}"
coordinate="${GITEA_MAVEN_GROUP}:${GITEA_MAVEN_ARTIFACT}:${GITEA_MAVEN_VERSION}"

cat >"$workdir/settings.xml" <<EOF
<settings><servers><server><id>gateway</id><username>maven-e2e</username><password>${GATEWAY_RESOLVER_TOKEN}</password></server></servers><profiles><profile><id>gateway</id><repositories><repository><id>gateway</id><url>${repository_url}</url></repository></repositories></profile></profiles><activeProfiles><activeProfile>gateway</activeProfile></activeProfiles></settings>
EOF
docker run --rm -v "$workdir:/work" -w /work maven:3.9-eclipse-temurin-21 \
  mvn --batch-mode --settings settings.xml dependency:get -Dartifact="$coordinate" >/dev/null

# A separate Gradle client must resolve from the Gateway cache after the
# controlled upstream is unavailable.
kill "$proxy_pid"
wait "$proxy_pid" 2>/dev/null || true
proxy_pid=""

cat >"$workdir/settings.gradle" <<'EOF'
pluginManagement { repositories { gradlePluginPortal() } }
EOF
cat >"$workdir/build.gradle" <<EOF
repositories { maven { url = uri('${repository_url}'); credentials { username = 'gradle-e2e'; password = '${GATEWAY_RESOLVER_TOKEN}' } } }
configurations { resolve }
dependencies { resolve '${coordinate}' }
tasks.register('resolveDependencies') { doLast { configurations.resolve.files.each { println it } } }
EOF
docker run --rm -v "$workdir:/work" -w /work gradle:8.14-jdk21 \
  gradle --no-daemon --quiet resolveDependencies | grep -Fq "${GITEA_MAVEN_ARTIFACT}-${GITEA_MAVEN_VERSION}.jar"

metrics=$(curl --silent --show-error --fail "$gateway_url/metrics")
for sample in 'artifact_gateway_maven_cache_requests_total{outcome="hit"}' 'artifact_gateway_maven_cache_requests_total{outcome="miss"}'; do
  grep -Fq "$sample" <<<"$metrics"
done

printf 'Maven first-read and Gradle cached-resolution E2E passed: %s through %s\n' "$coordinate" "$repository_url"
