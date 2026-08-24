#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

for binary in curl mvn gradle go python3; do
  command -v "$binary" >/dev/null || {
    printf 'Native Maven E2E requires %s\n' "$binary" >&2
    exit 1
  }
done

port=$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')
gateway_url="http://127.0.0.1:${port}"
workdir=$(mktemp -d)
fixture_pid=""

cleanup() {
  local status=$?
  if [[ -n "$fixture_pid" ]]; then
    kill "$fixture_pid" 2>/dev/null || true
    wait "$fixture_pid" 2>/dev/null || true
  fi
  rm -rf "$workdir"
  trap - EXIT
  exit "$status"
}
trap cleanup EXIT

go build -o "$workdir/native-maven-fixture" ./cmd/native-maven-fixture
LISTEN_ADDR="127.0.0.1:${port}" "$workdir/native-maven-fixture" >"$workdir/gateway.log" 2>&1 &
fixture_pid=$!
until curl --silent --show-error --fail "$gateway_url/livez" >/dev/null; do
  if ! kill -0 "$fixture_pid" 2>/dev/null; then
    cat "$workdir/gateway.log" >&2
    exit 1
  fi
  sleep 0.1
done

status() {
  curl --silent --show-error --output "$workdir/last-response" --write-out '%{http_code}' "$@"
}

expect_status() {
  local expected=$1
  shift
  local actual
  actual=$(status "$@")
  if [[ "$actual" != "$expected" ]]; then
    printf 'Expected HTTP %s, got %s: %s\n' "$expected" "$actual" "$*" >&2
    cat "$workdir/last-response" >&2
    cat "$workdir/gateway.log" >&2
    exit 1
  fi
}

basic=(--user fixture:fixture-secret)

# Strict publication remains available as an explicit repository policy. A
# complete coordinate stays invisible until the Gateway commit, while client
# checksum sidecars and metadata remain non-authoritative assertions.
strict_pom='<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>strict-widget</artifactId><version>0.0.1</version></project>'
expect_status 201 "${basic[@]}" -X PUT --data-binary "$strict_pom" "$gateway_url/repository/maven/strict-deploys/org/example/strict-widget/0.0.1/strict-widget-0.0.1.pom"
expect_status 201 "${basic[@]}" -X PUT --data-binary strict-bytecode "$gateway_url/repository/maven/strict-deploys/org/example/strict-widget/0.0.1/strict-widget-0.0.1.jar"
expect_status 201 "${basic[@]}" -X PUT --data-binary deadbeef "$gateway_url/repository/maven/strict-deploys/org/example/strict-widget/0.0.1/strict-widget-0.0.1.jar.sha256"
expect_status 201 "${basic[@]}" -X PUT --data-binary '<metadata/>' "$gateway_url/repository/maven/strict-deploys/org/example/strict-widget/maven-metadata.xml"
expect_status 404 "${basic[@]}" "$gateway_url/repository/maven/strict-deploys/org/example/strict-widget/0.0.1/strict-widget-0.0.1.jar"
expect_status 200 "${basic[@]}" -H 'Idempotency-Key: strict-release' -H 'Content-Type: application/json' \
  --data '{"expectedAssetNames":["strict-widget-0.0.1.pom","strict-widget-0.0.1.jar"]}' \
  "$gateway_url/repository/maven/strict-deploys/coordinates/org.example:strict-widget:0.0.1:commit"
expect_status 200 "${basic[@]}" "$gateway_url/repository/maven/strict-deploys/org/example/strict-widget/0.0.1/strict-widget-0.0.1.jar"
expect_status 200 "${basic[@]}" -H 'Idempotency-Key: strict-release' -H 'Content-Type: application/json' \
  --data '{"expectedAssetNames":["strict-widget-0.0.1.jar","strict-widget-0.0.1.pom"]}' \
  "$gateway_url/repository/maven/strict-deploys/coordinates/org.example:strict-widget:0.0.1:commit"
expect_status 409 "${basic[@]}" -H 'Idempotency-Key: strict-release-other' -H 'Content-Type: application/json' \
  --data '{"expectedAssetNames":["strict-widget-0.0.1.pom","strict-widget-0.0.1.jar"]}' \
  "$gateway_url/repository/maven/strict-deploys/coordinates/org.example:strict-widget:0.0.1:commit"

maven_dir="$workdir/maven"
mkdir -p "$maven_dir"
cat >"$maven_dir/settings.xml" <<EOF
<settings><servers><server><id>native</id><username>fixture</username><password>fixture-secret</password></server></servers></settings>
EOF
cat >"$maven_dir/pom.xml" <<EOF
<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>maven-widget</artifactId><version>1.2.3</version><distributionManagement><repository><id>native</id><url>${gateway_url}/repository/deploys</url></repository></distributionManagement></project>
EOF
(cd "$maven_dir" && mvn --batch-mode --settings settings.xml -Dmaven.repo.local="$workdir/maven-deploy-repository" deploy)
expect_status 200 "${basic[@]}" "$gateway_url/repository/deploys/org/example/maven-widget/1.2.3/maven-widget-1.2.3.jar"
expect_status 409 "${basic[@]}" -H 'Idempotency-Key: direct-mode-does-not-commit' -H 'Content-Type: application/json' \
  --data '{"expectedAssetNames":["maven-widget-1.2.3.pom","maven-widget-1.2.3.jar"]}' \
  "$gateway_url/repository/maven/deploys/coordinates/org.example:maven-widget:1.2.3:commit"

resolve_dir="$workdir/maven-resolve"
mkdir -p "$resolve_dir"
cat >"$resolve_dir/pom.xml" <<EOF
<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>fixture</groupId><artifactId>resolver</artifactId><version>1</version><repositories><repository><id>native</id><url>${gateway_url}/repository/deploys</url></repository></repositories><dependencies><dependency><groupId>org.example</groupId><artifactId>maven-widget</artifactId><version>1.2.3</version></dependency></dependencies></project>
EOF
(cd "$resolve_dir" && mvn --batch-mode --settings "$maven_dir/settings.xml" -Dmaven.repo.local="$workdir/maven-resolve-repository" dependency:resolve)

gradle_dir="$workdir/gradle"
mkdir -p "$gradle_dir/src/main/java/org/example"
cat >"$gradle_dir/settings.gradle" <<'EOF'
rootProject.name = 'gradle-widget'
EOF
cat >"$gradle_dir/src/main/java/org/example/Widget.java" <<'EOF'
package org.example; public final class Widget { }
EOF
cat >"$gradle_dir/build.gradle" <<EOF
plugins {
  id 'java'
  id 'maven-publish'
}
group = 'org.example'
version = '2.0.0-SNAPSHOT'
repositories {
  maven {
    url = uri('${gateway_url}/repository/deploys')
    credentials { username = 'fixture'; password = 'fixture-secret' }
    allowInsecureProtocol = true
  }
}
publishing {
  publications {
    create('maven', MavenPublication) {
      from components.java
    }
  }
  repositories {
    maven {
      url = uri('${gateway_url}/repository/deploys')
      credentials { username = 'fixture'; password = 'fixture-secret' }
      allowInsecureProtocol = true
    }
  }
}
configurations { nativeResolve }
dependencies { nativeResolve 'org.example:gradle-widget:2.0.0-SNAPSHOT' }
tasks.register('resolveNative') { doLast { configurations.nativeResolve.files.each { println it } } }
EOF
if ! (cd "$gradle_dir" && GRADLE_USER_HOME="$workdir/gradle-user-home" gradle --no-daemon --quiet publish); then
  cat "$workdir/gateway.log" >&2
  exit 1
fi
(cd "$gradle_dir" && GRADLE_USER_HOME="$workdir/gradle-user-home" gradle --no-daemon --quiet resolveNative | grep -F 'gradle-widget-2.0.0-SNAPSHOT.jar')

expect_status 200 "${basic[@]}" "$gateway_url/repository/deploys/org/example/maven-widget/1.2.3/maven-widget-1.2.3.jar.sha256"
expect_status 200 "${basic[@]}" "$gateway_url/repository/deploys/org/example/gradle-widget/2.0.0-SNAPSHOT/maven-metadata.xml"

printf 'Native Maven/Gradle E2E passed through Nexus-compatible roots; strict Gateway opt-in also passed through %s\n' "$gateway_url"
