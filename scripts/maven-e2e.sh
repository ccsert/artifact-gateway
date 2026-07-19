#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

environment_file=${GATEWAY_ENV_FILE:-.env}

if [[ ! -f "$environment_file" || ! -f .gitea-fixture/connection.env ]]; then
  printf '%s\n' 'Run requires .env and the seeded Gitea fixture.' >&2
  exit 1
fi

gateway_http_port_override=${GATEWAY_HTTP_PORT:-}
# shellcheck disable=SC1091
source "$environment_file"
# shellcheck disable=SC1091
source .gitea-fixture/connection.env
if [[ -n "$gateway_http_port_override" ]]; then
  GATEWAY_HTTP_PORT=$gateway_http_port_override
fi

for name in GATEWAY_HTTP_PORT GATEWAY_ADMIN_TOKEN GATEWAY_RESOLVER_TOKEN GITEA_HTTP_PORT GITEA_FIXTURE_USERNAME GITEA_FIXTURE_TOKEN GITEA_FIXTURE_ORG GITEA_MAVEN_GROUP GITEA_MAVEN_ARTIFACT GITEA_MAVEN_VERSION; do
  if [[ -z "${!name:-}" ]]; then
    printf 'Missing required %s\n' "$name" >&2
    exit 1
  fi
done

gateway_url="http://localhost:${GATEWAY_HTTP_PORT}"
proxy_port=${GATEWAY_MAVEN_PROXY_PORT:-}
if [[ -z "$proxy_port" ]]; then
  proxy_port=$(python3 -c 'import socket; listener = socket.socket(); listener.bind(("127.0.0.1", 0)); print(listener.getsockname()[1]); listener.close()')
fi
if [[ "$proxy_port" == "$GATEWAY_HTTP_PORT" ]]; then
  printf '%s\n' 'GATEWAY_MAVEN_PROXY_PORT must differ from GATEWAY_HTTP_PORT.' >&2
  exit 1
fi
proxy_host="host.docker.internal:${proxy_port}"
run_id=${MAVEN_E2E_RUN_ID:-"$(date +%s)-${RANDOM}"}
proxy_group="${GITEA_FIXTURE_ORG}-proxy-${run_id}"
proxy_endpoint="http://${proxy_host}"

workdir=$(mktemp -d)
proxy_pid=""
cleanup() {
  if [[ -n "$proxy_pid" ]]; then kill "$proxy_pid" 2>/dev/null || true; fi
  rm -rf "$workdir"
}
trap cleanup EXIT
proxy_log="$workdir/proxy.log"
python3 scripts/maven-proxy-fixture.py --port "$proxy_port" --directory .gitea-fixture/maven --log "$proxy_log" >/dev/null 2>&1 &
proxy_pid=$!
until curl --silent --show-error --fail "http://localhost:${proxy_port}/" >/dev/null; do
  if ! kill -0 "$proxy_pid" 2>/dev/null; then
    wait "$proxy_pid" || true
    printf 'Maven proxy fixture failed to start on port %s\n' "$proxy_port" >&2
    exit 1
  fi
  sleep 1
done

GATEWAY_ADAPTER_MODE=gitea \
GATEWAY_GITEA_USERNAME="$GITEA_FIXTURE_USERNAME" \
GATEWAY_GITEA_TOKEN="$GITEA_FIXTURE_TOKEN" \
GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS="$proxy_host" \
docker compose --env-file "$environment_file" -f compose.yml up -d --build --wait

create_group() {
  local name=$1 endpoint=$2 status payload
  payload=$(printf '{"name":"%s","members":[{"name":"fixture-proxy","type":"proxy","endpoint":"%s","position":0}]}' "$name" "$endpoint")
  status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
    -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' \
    --data "$payload" "$gateway_url/api/v1/maven/groups")
  if [[ "$status" != 201 && "$status" != 409 ]]; then
    printf 'Creating Maven Proxy Group %s failed with HTTP %s\n' "$name" "$status" >&2
    exit 1
  fi
}

request_status() {
  curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
    --user "contract-e2e:${GATEWAY_RESOLVER_TOKEN}" "$1"
}

expect_request_status() {
  local expected=$1 url=$2 actual
  actual=$(request_status "$url")
  if [[ "$actual" != "$expected" ]]; then
    printf 'Expected Maven request HTTP %s from %s, got %s\n' "$expected" "$url" "$actual" >&2
    exit 1
  fi
}

create_group "$proxy_group" "$proxy_endpoint"

repository_url="http://host.docker.internal:${GATEWAY_HTTP_PORT}/maven/${proxy_group}"
coordinate="${GITEA_MAVEN_GROUP}:${GITEA_MAVEN_ARTIFACT}:${GITEA_MAVEN_VERSION}"
maven_resource="${GITEA_MAVEN_GROUP//.//}/${GITEA_MAVEN_ARTIFACT}/${GITEA_MAVEN_VERSION}/${GITEA_MAVEN_ARTIFACT}-${GITEA_MAVEN_VERSION}.pom"
metadata_resource="${GITEA_MAVEN_GROUP//.//}/${GITEA_MAVEN_ARTIFACT}/maven-metadata.xml"

for status in 429 503; do
  retry_group="${GITEA_FIXTURE_ORG}-retry-${status}-${run_id}"
  create_group "$retry_group" "${proxy_endpoint}/retry/${status}"
  expect_request_status 200 "$gateway_url/maven/${retry_group}/${maven_resource}"
  [[ $(grep -c "GET /retry/${status}/${maven_resource} ${status}" "$proxy_log") == 1 ]]
  [[ $(grep -c "GET /retry/${status}/${maven_resource} 200" "$proxy_log") == 1 ]]
done

negative_group="${GITEA_FIXTURE_ORG}-negative-${run_id}"
negative_resource="com/example/missing/1.0/missing-1.0.pom"
create_group "$negative_group" "$proxy_endpoint"
expect_request_status 404 "$gateway_url/maven/${negative_group}/${negative_resource}"
expect_request_status 404 "$gateway_url/maven/${negative_group}/${negative_resource}"
[[ $(grep -c "GET /${negative_resource} 404" "$proxy_log") == 1 ]]

denied_group="${GITEA_FIXTURE_ORG}-denied-${run_id}"
create_group "$denied_group" "http://untrusted.invalid"
expect_request_status 403 "$gateway_url/maven/${denied_group}/${maven_resource}"

cat >"$workdir/settings.xml" <<EOF
<settings><servers><server><id>gateway</id><username>maven-e2e</username><password>${GATEWAY_RESOLVER_TOKEN}</password></server><server><id>gateway-http</id><username>maven-e2e</username><password>${GATEWAY_RESOLVER_TOKEN}</password></server></servers><mirrors><mirror><id>gateway-http</id><mirrorOf>external:http:*</mirrorOf><url>${repository_url}</url></mirror></mirrors><profiles><profile><id>gateway</id><repositories><repository><id>gateway</id><url>${repository_url}</url></repository></repositories></profile></profiles><activeProfiles><activeProfile>gateway</activeProfile></activeProfiles></settings>
EOF

# Maven's stock dependency plugin is not part of the image and would be
# downloaded from Central before the Gateway is contacted. Build a tiny local
# plugin instead; its descriptor requests Maven's normal compile resolution.
maven_plugin_repository="$workdir/maven-repository/gateway/fixture/maven-client-fixture/1.0.0"
mkdir -p "$maven_plugin_repository"
cp scripts/maven-client-fixture/plugin.xml "$maven_plugin_repository/plugin.xml"
cat >"$maven_plugin_repository/maven-client-fixture-1.0.0.pom" <<'EOF'
<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>gateway.fixture</groupId><artifactId>maven-client-fixture</artifactId><version>1.0.0</version><packaging>maven-plugin</packaging></project>
EOF
docker run --rm -v "$repo_root/scripts/maven-client-fixture:/fixture:ro" -v "$workdir:/work" \
  --entrypoint sh maven:3.9-eclipse-temurin-21 -ec '
    mkdir -p /work/classes/META-INF/maven
    javac -cp "/usr/share/maven/lib/*" -d /work/classes /fixture/ResolveMojo.java
    cp /fixture/plugin.xml /work/classes/META-INF/maven/plugin.xml
    jar cf /work/maven-repository/gateway/fixture/maven-client-fixture/1.0.0/maven-client-fixture-1.0.0.jar -C /work/classes .
  '
cat >"$workdir/pom.xml" <<EOF
<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>gateway.e2e</groupId><artifactId>gateway-client</artifactId><version>1.0.0</version><repositories><repository><id>gateway</id><url>${repository_url}</url></repository></repositories><dependencies><dependency><groupId>${GITEA_MAVEN_GROUP}</groupId><artifactId>${GITEA_MAVEN_ARTIFACT}</artifactId><version>LATEST</version></dependency></dependencies></project>
EOF
docker run --rm -v "$workdir:/work" -w /work maven:3.9-eclipse-temurin-21 \
  mvn --batch-mode --settings settings.xml -Dmaven.repo.local=/work/maven-repository \
  gateway.fixture:maven-client-fixture:1.0.0:resolve-fixture >"$workdir/maven.log" 2>&1 || {
    cat "$workdir/maven.log" >&2
    exit 1
  }
grep -Fq "Gateway fixture dependency resolution completed." "$workdir/maven.log" || {
  cat "$workdir/maven.log" >&2
  exit 1
}

# Maven resolved LATEST from upstream metadata. Read it again through the
# Gateway to establish the metadata cache before the separate Gradle client.
expect_request_status 200 "$gateway_url/maven/${proxy_group}/${metadata_resource}"
expect_request_status 200 "$gateway_url/maven/${proxy_group}/${metadata_resource}"
[[ $(grep -c "GET /${metadata_resource} 200" "$proxy_log") == 1 ]]

# A separate Gradle client must resolve from the Gateway cache after the
# controlled upstream is unavailable.
kill "$proxy_pid"
wait "$proxy_pid" 2>/dev/null || true
proxy_pid=""

cat >"$workdir/settings.gradle" <<'EOF'
pluginManagement { repositories { gradlePluginPortal() } }
EOF
cat >"$workdir/build.gradle" <<EOF
repositories { maven { url = uri('${repository_url}'); allowInsecureProtocol = true; credentials { username = 'gradle-e2e'; password = '${GATEWAY_RESOLVER_TOKEN}' } } }
configurations { resolve }
dependencies { resolve '${GITEA_MAVEN_GROUP}:${GITEA_MAVEN_ARTIFACT}:1.+' }
tasks.register('resolveDependencies') { doLast { configurations.resolve.files.each { println it } } }
EOF
docker run --rm -v "$workdir:/work" -w /work gradle:8.14-jdk21 \
  gradle --no-daemon --quiet resolveDependencies >"$workdir/gradle.log" 2>&1 || {
    cat "$workdir/gradle.log" >&2
    exit 1
  }
grep -Fq "${GITEA_MAVEN_ARTIFACT}-${GITEA_MAVEN_VERSION}.jar" "$workdir/gradle.log" || {
  cat "$workdir/gradle.log" >&2
  exit 1
}

metrics=$(curl --silent --show-error --fail "$gateway_url/metrics")
for sample in 'artifact_gateway_maven_cache_requests_total{outcome="hit"}' 'artifact_gateway_maven_cache_requests_total{outcome="miss"}' 'artifact_gateway_maven_upstream_retries_total' 'artifact_gateway_maven_negative_cache_hits_total' 'artifact_gateway_maven_proxy_denied_total'; do
  grep -F "$sample" <<<"$metrics" | awk '$NF > 0 { found=1 } END { exit !found }'
done

# Cached content from a previously allowed endpoint must not survive a
# whitelist tightening after the gateway restarts.
GATEWAY_ADAPTER_MODE=gitea \
GATEWAY_GITEA_USERNAME="$GITEA_FIXTURE_USERNAME" \
GATEWAY_GITEA_TOKEN="$GITEA_FIXTURE_TOKEN" \
GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS="" \
docker compose --env-file "$environment_file" -f compose.yml up -d --force-recreate --wait gateway
expect_request_status 403 "$gateway_url/maven/${proxy_group}/${maven_resource}"

metrics=$(curl --silent --show-error --fail "$gateway_url/metrics")
grep -F 'artifact_gateway_maven_cache_invalidations_total' <<<"$metrics" | awk '$NF > 0 { found=1 } END { exit !found }'

printf 'Maven first-read and Gradle cached-resolution E2E passed: %s through %s\n' "$coordinate" "$repository_url"
