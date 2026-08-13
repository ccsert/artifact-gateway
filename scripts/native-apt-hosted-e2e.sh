#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

debian_image='debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241'
project="artifact-gateway-apt-e2e-$$"
workdir=$(mktemp -d "${TMPDIR:-/tmp}/artifact-gateway-apt-e2e.XXXXXX")
env_file="$workdir/e2e.env"
admin_token='apt-e2e-admin-token'
resolver_token='apt-e2e-resolver-token'
signer_token='apt-e2e-reference-signer-token-0001'

chmod 0700 "$workdir"
{
  printf '%s\n' \
    'GATEWAY_HTTP_PORT=0' \
    'GATEWAY_POSTGRES_PORT=0' \
    'RUSTFS_API_PORT=0' \
    'RUSTFS_CONSOLE_PORT=0' \
    'GATEWAY_POSTGRES_PASSWORD=apt-e2e-postgres-password' \
    'RUSTFS_ACCESS_KEY=apt-e2e-access-key' \
    'RUSTFS_SECRET_KEY=apt-e2e-rustfs-secret-key-00000001' \
    'RUSTFS_RPC_SECRET=apt-e2e-rustfs-rpc-secret-00000001' \
    "GATEWAY_ADMIN_TOKEN=$admin_token" \
    "GATEWAY_RESOLVER_TOKEN=$resolver_token" \
    'GATEWAY_APT_SIGNER_ENDPOINT=http://127.0.0.1:18083/v1/sign-release' \
    "GATEWAY_APT_SIGNER_TOKEN=$signer_token" \
    'GATEWAY_APT_SIGNER_TIMEOUT=30s' \
    'REFERENCE_APT_SIGNER_RSA_BITS=2048'
} >"$env_file"
chmod 0600 "$env_file"

compose=(docker compose --env-file "$env_file" -p "$project" --profile apt-signer)
cleanup() {
  "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$workdir"
}
trap cleanup EXIT

"${compose[@]}" up -d --build --wait gateway reference-apt-signer

gateway_port=$("${compose[@]}" port gateway 8080 | tail -n 1)
gateway_port=${gateway_port##*:}
gateway_url="http://127.0.0.1:$gateway_port"
curl --silent --show-error --fail --retry 20 --retry-delay 1 "$gateway_url/readyz" >/dev/null

package_file="$workdir/artifact-gateway-e2e_1.0.0-1_all.deb"
docker run --rm --volume "$workdir:/work" "$debian_image" /bin/sh -ec '
  mkdir -p /tmp/artifact-gateway-e2e/DEBIAN /tmp/artifact-gateway-e2e/usr/share/artifact-gateway-e2e
  printf "%s\n" \
    "Package: artifact-gateway-e2e" \
    "Version: 1.0.0-1" \
    "Architecture: all" \
    "Maintainer: Artifact Gateway E2E <apt-e2e@example.test>" \
    "Description: real APT Hosted installation gate" \
    > /tmp/artifact-gateway-e2e/DEBIAN/control
  printf "%s\n" "installed-from-artifact-gateway" > /tmp/artifact-gateway-e2e/usr/share/artifact-gateway-e2e/installed.txt
  dpkg-deb --build --root-owner-group /tmp/artifact-gateway-e2e /work/artifact-gateway-e2e_1.0.0-1_all.deb >/dev/null
'

if command -v sha256sum >/dev/null 2>&1; then
  package_sha256=$(sha256sum "$package_file" | awk '{print $1}')
else
  package_sha256=$(shasum -a 256 "$package_file" | awk '{print $1}')
fi
package_size=$(wc -c <"$package_file" | tr -d ' ')

repository_response="$workdir/repository.json"
repository_status=$(curl --silent --show-error --output "$repository_response" --write-out '%{http_code}' \
  --request POST "$gateway_url/api/v2/repositories" \
  --header "Authorization: Bearer $admin_token" \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: apt-hosted-e2e-repository' \
  --data '{"name":"apt-hosted-e2e","format":"apt","type":"hosted"}')
if [[ "$repository_status" != 201 ]]; then
  printf 'create repository failed: HTTP %s\n' "$repository_status" >&2
  sed -n '1,20p' "$repository_response" >&2
  exit 1
fi
repository_id=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "$repository_response")
[[ -n "$repository_id" ]] || { printf 'repository response has no id\n' >&2; exit 1; }

session_response="$workdir/session.json"
session_body=$(printf '{"suite":"stable","component":"main","objectName":"artifact-gateway-e2e_1.0.0-1_all.deb","declaredDigest":"sha256:%s","declaredSize":%s,"expectedIdentity":"artifact-gateway-e2e@1.0.0-1#all"}' "$package_sha256" "$package_size")
session_status=$(curl --silent --show-error --output "$session_response" --write-out '%{http_code}' \
  --request POST "$gateway_url/api/v2/repositories/$repository_id/apt/publication-sessions" \
  --header "Authorization: Bearer $admin_token" \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: apt-hosted-e2e-session' \
  --data "$session_body")
if [[ "$session_status" != 201 ]]; then
  printf 'create publication session failed: HTTP %s\n' "$session_status" >&2
  sed -n '1,20p' "$session_response" >&2
  exit 1
fi
session_id=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "$session_response")
[[ -n "$session_id" ]] || { printf 'session response has no id\n' >&2; exit 1; }

upload_response="$workdir/upload.json"
upload_status=$(curl --silent --show-error --output "$upload_response" --write-out '%{http_code}' \
  --request PUT "$gateway_url/api/v2/repositories/$repository_id/apt/publication-sessions/$session_id/package" \
  --header "Authorization: Bearer $admin_token" \
  --header 'Content-Type: application/vnd.debian.binary-package' \
  --data-binary "@$package_file")
if [[ "$upload_status" != 200 ]]; then
  printf 'upload package failed: HTTP %s\n' "$upload_status" >&2
  sed -n '1,20p' "$upload_response" >&2
  exit 1
fi

snapshot_response="$workdir/snapshot.json"
snapshot_body=$(printf '{"suite":"stable","sequence":1,"publicationSessionIds":["%s"]}' "$session_id")
snapshot_status=$(curl --silent --show-error --output "$snapshot_response" --write-out '%{http_code}' \
  --request POST "$gateway_url/api/v2/repositories/$repository_id/apt/snapshots" \
  --header "Authorization: Bearer $admin_token" \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: apt-hosted-e2e-snapshot' \
  --data "$snapshot_body")
if [[ "$snapshot_status" != 201 ]] || ! grep -q '"state":"visible"' "$snapshot_response"; then
  printf 'publish snapshot failed: HTTP %s\n' "$snapshot_status" >&2
  sed -n '1,20p' "$snapshot_response" >&2
  exit 1
fi

public_key="$workdir/apt-release.asc"
"${compose[@]}" exec -T reference-apt-signer /usr/local/bin/reference-apt-signer-healthcheck public-key >"$public_key"
grep -q 'BEGIN PGP PUBLIC KEY BLOCK' "$public_key"

gateway_container=$("${compose[@]}" ps -q gateway)
gateway_network=$(docker inspect --format '{{range $name, $settings := .NetworkSettings.Networks}}{{$name}}{{end}}' "$gateway_container")
[[ -n "$gateway_network" ]] || { printf 'gateway network was not found\n' >&2; exit 1; }

apt_install() {
  docker run --rm \
    --network "$gateway_network" \
    --env "APT_E2E_RESOLVER_TOKEN=$resolver_token" \
    --volume "$public_key:/keys/artifact-gateway.asc:ro" \
    "$debian_image" /bin/sh -ec '
      rm -f /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources
      install -d -m 0755 /etc/apt/keyrings /etc/apt/auth.conf.d
      install -m 0644 /keys/artifact-gateway.asc /etc/apt/keyrings/artifact-gateway.asc
      printf "machine http://gateway:8080/apt/apt-hosted-e2e\nlogin resolver\npassword %s\n" "$APT_E2E_RESOLVER_TOKEN" > /etc/apt/auth.conf.d/artifact-gateway.conf
      chmod 0600 /etc/apt/auth.conf.d/artifact-gateway.conf
      printf "%s\n" "deb [arch=all signed-by=/etc/apt/keyrings/artifact-gateway.asc] http://gateway:8080/apt/apt-hosted-e2e stable main" > /etc/apt/sources.list.d/artifact-gateway.list
      apt-get -o Acquire::Retries=0 update
      apt-get -o Acquire::Retries=0 install -y --no-install-recommends artifact-gateway-e2e=1.0.0-1
      grep -Fxq installed-from-artifact-gateway /usr/share/artifact-gateway-e2e/installed.txt
    '
}

apt_install
"${compose[@]}" stop reference-apt-signer >/dev/null
apt_install

printf 'native APT Hosted E2E passed (signed publish plus two real apt installs; second with signer offline)\n'
