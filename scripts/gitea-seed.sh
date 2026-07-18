#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

require() {
  if [[ -z "${!1:-}" ]]; then
    printf 'Missing required %s in .env\n' "$1" >&2
    exit 1
  fi
}

if [[ ! -f .env ]]; then
  printf '%s\n' 'Missing .env. Copy .env.example, choose local-only credentials, then rerun.' >&2
  exit 1
fi

# shellcheck disable=SC1091
source .env
for name in GITEA_HTTP_PORT GITEA_ADMIN_USERNAME GITEA_ADMIN_PASSWORD GITEA_ADMIN_EMAIL GITEA_FIXTURE_USERNAME GITEA_FIXTURE_PASSWORD GITEA_FIXTURE_EMAIL GITEA_FIXTURE_ORG; do
  require "$name"
done

base_url="http://localhost:${GITEA_HTTP_PORT}"
registry="localhost:${GITEA_HTTP_PORT}"
compose=(docker compose --env-file .env -f compose.gitea.yml)
fixture_dir=.gitea-fixture
maven_group=com/example/gatewayfixture
maven_artifact=sample-library
maven_version=1.0.0
maven_path="$maven_group/$maven_artifact/$maven_version"

until curl --silent --show-error --fail "$base_url/api/healthz" >/dev/null; do sleep 1; done

create_user() {
  local username=$1 password=$2 email=$3 admin_flag=$4
  if "${compose[@]}" exec -T --user git gitea gitea --config /data/gitea/conf/app.ini admin user list | awk 'NR > 1 {print $2}' | grep -Fxq "$username"; then
    return
  fi
  local args=(gitea --config /data/gitea/conf/app.ini admin user create --username "$username" --password "$password" --email "$email" --must-change-password=false)
  if [[ "$admin_flag" == true ]]; then args+=(--admin); fi
  "${compose[@]}" exec -T --user git gitea "${args[@]}"
}

create_user "$GITEA_ADMIN_USERNAME" "$GITEA_ADMIN_PASSWORD" "$GITEA_ADMIN_EMAIL" true
create_user "$GITEA_FIXTURE_USERNAME" "$GITEA_FIXTURE_PASSWORD" "$GITEA_FIXTURE_EMAIL" false

if ! curl --silent --fail -u "$GITEA_ADMIN_USERNAME:$GITEA_ADMIN_PASSWORD" "$base_url/api/v1/orgs/$GITEA_FIXTURE_ORG" >/dev/null; then
  curl --silent --show-error --fail -u "$GITEA_ADMIN_USERNAME:$GITEA_ADMIN_PASSWORD" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"$GITEA_FIXTURE_ORG\",\"full_name\":\"Gateway test fixtures\"}" \
    "$base_url/api/v1/admin/users/$GITEA_ADMIN_USERNAME/orgs" >/dev/null
fi

team_json=$(curl --silent --show-error --fail -u "$GITEA_ADMIN_USERNAME:$GITEA_ADMIN_PASSWORD" "$base_url/api/v1/orgs/$GITEA_FIXTURE_ORG/teams")
team_id=$(printf '%s' "$team_json" | tr '{' '\n' | sed -n '/"name":"gateway-package-writers"/s/.*"id":\([0-9][0-9]*\).*/\1/p')
if [[ -z "$team_id" ]]; then
  team_json=$(curl --silent --show-error --fail -u "$GITEA_ADMIN_USERNAME:$GITEA_ADMIN_PASSWORD" \
    -H 'Content-Type: application/json' \
    -d '{"name":"gateway-package-writers","permission":"read","can_create_org_repo":false,"units_map":{"repo.packages":"write"}}' \
    "$base_url/api/v1/orgs/$GITEA_FIXTURE_ORG/teams")
  team_id=$(printf '%s' "$team_json" | sed -n 's/^{"id":\([0-9][0-9]*\).*/\1/p')
fi
if [[ -z "$team_id" ]]; then
  printf '%s\n' 'Unable to find or create the package-writer team.' >&2
  exit 1
fi
curl --silent --show-error --fail -X PUT -u "$GITEA_ADMIN_USERNAME:$GITEA_ADMIN_PASSWORD" \
  "$base_url/api/v1/teams/$team_id/members/$GITEA_FIXTURE_USERNAME" >/dev/null

mkdir -p "$fixture_dir/maven/$maven_path"
pom="$fixture_dir/maven/$maven_path/$maven_artifact-$maven_version.pom"
jar="$fixture_dir/maven/$maven_path/$maven_artifact-$maven_version.jar"
printf '%s\n' "<project xmlns=\"http://maven.apache.org/POM/4.0.0\"><modelVersion>4.0.0</modelVersion><groupId>com.example.gatewayfixture</groupId><artifactId>$maven_artifact</artifactId><version>$maven_version</version><packaging>jar</packaging></project>" >"$pom"
if [[ ! -f "$jar" ]]; then
  printf 'Artifact Gateway Gitea Maven fixture %s\n' "$maven_version" >"$fixture_dir/maven/fixture.txt"
  (cd "$fixture_dir/maven" && zip -q -j "$repo_root/$jar" fixture.txt)
fi

upload_maven() {
  local file=$1
  local filename
  local status
  filename=$(basename "$file")
  status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
    -u "$GITEA_FIXTURE_USERNAME:$GITEA_FIXTURE_PASSWORD" \
    --upload-file "$file" "$base_url/api/packages/$GITEA_FIXTURE_ORG/maven/$maven_path/$filename")
  if [[ "$status" != 200 && "$status" != 201 && "$status" != 409 ]]; then
    printf 'Maven upload failed for %s (HTTP %s)\n' "$filename" "$status" >&2
    exit 1
  fi
}

for artifact in "$pom" "$jar"; do
  upload_maven "$artifact"
  printf '%s' "$(shasum -a 1 "$artifact" | awk '{print $1}')" >"$artifact.sha1"
  printf '%s' "$(shasum -a 256 "$artifact" | awk '{print $1}')" >"$artifact.sha256"
  printf '%s' "$(md5 -q "$artifact")" >"$artifact.md5"
  upload_maven "$artifact.sha1"
  upload_maven "$artifact.sha256"
  upload_maven "$artifact.md5"
done

oci_image="$GITEA_FIXTURE_ORG/gateway-fixture:1.0.0"
printf '%s' "$GITEA_FIXTURE_PASSWORD" | docker login "$registry" --username "$GITEA_FIXTURE_USERNAME" --password-stdin >/dev/null
docker pull busybox:1.36 >/dev/null
docker tag busybox:1.36 "$registry/$oci_image"
docker push "$registry/$oci_image" >/dev/null

fixture_token=$(curl --silent --show-error --fail -u "$GITEA_FIXTURE_USERNAME:$GITEA_FIXTURE_PASSWORD" \
  -H 'Content-Type: application/json' -d "{\"name\":\"gateway-integration-$(date +%s)\",\"scopes\":[\"read:package\",\"write:package\"]}" \
  "$base_url/api/v1/users/$GITEA_FIXTURE_USERNAME/tokens" | sed -n 's/.*"sha1":"\([^"]*\)".*/\1/p')

if [[ -z "$fixture_token" ]]; then
  printf '%s\n' 'Unable to create fixture API token.' >&2
  exit 1
fi

umask 077
cat >"$fixture_dir/connection.env" <<EOF
GITEA_BASE_URL=$base_url
GITEA_OCI_REGISTRY=$registry
GITEA_MAVEN_REGISTRY=$base_url/api/packages/$GITEA_FIXTURE_ORG/maven
GITEA_FIXTURE_ORG=$GITEA_FIXTURE_ORG
GITEA_FIXTURE_USERNAME=$GITEA_FIXTURE_USERNAME
GITEA_FIXTURE_PASSWORD=$GITEA_FIXTURE_PASSWORD
GITEA_FIXTURE_TOKEN=$fixture_token
GITEA_OCI_IMAGE=$registry/$oci_image
GITEA_MAVEN_GROUP=com.example.gatewayfixture
GITEA_MAVEN_ARTIFACT=$maven_artifact
GITEA_MAVEN_VERSION=$maven_version
EOF

printf 'Fixture ready. Connection data: %s/%s\n' "$repo_root" "$fixture_dir/connection.env"
