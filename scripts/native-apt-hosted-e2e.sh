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

refresh_gateway_url() {
  local published_port
  published_port=$("${compose[@]}" port gateway 8080 | tail -n 1)
  [[ -n "$published_port" ]] || return 1
  gateway_url="http://127.0.0.1:${published_port##*:}"
}

refresh_gateway_url

wait_gateway_ready() {
  local ready_code=000
  for _ in $(seq 1 80); do
    refresh_gateway_url || {
      sleep 0.25
      continue
    }
    ready_code=$(curl --noproxy '*' --silent --show-error --output /dev/null --write-out '%{http_code}' \
      --max-time 2 "$gateway_url/readyz" 2>/dev/null || true)
    [[ "$ready_code" == 204 ]] && return 0
    sleep 0.25
  done
  printf 'Gateway did not return readiness 204 after restart; last HTTP status was %s.\n' "$ready_code" >&2
  return 1
}

wait_gateway_ready

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

signing_state() {
  curl --silent --show-error --fail \
    --header "Authorization: Bearer $admin_token" \
    "$gateway_url/api/v2/repositories/$repository_id/apt/signing-state"
}

capture_signed_snapshot() {
  local capture_dir=$1 state_file=$2 manifest_file release_digest inrelease_digest
  local release_file inrelease_file detached_file package_copy
  mkdir -p "$capture_dir"
  manifest_file="$capture_dir/SHA256SUMS"
  release_file="$capture_dir/Release"
  inrelease_file="$capture_dir/InRelease"
  detached_file="$capture_dir/Release.gpg"
  package_copy="$capture_dir/package.deb"
  : >"$manifest_file"

  read -r release_digest inrelease_digest < <(python3 - "$state_file" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    snapshot = json.load(source)["currentSnapshot"]
print(snapshot["releaseDigest"], snapshot["inReleaseDigest"])
PY
  )

  fetch_and_record() {
    local path=$1 destination=$2 expected_digest=${3:-} expected_size=${4:-}
    local actual_digest actual_size
    curl --silent --show-error --fail --user "resolver:$resolver_token" \
      "$gateway_url/apt/apt-hosted-e2e/$path" >"$destination"
    actual_digest="sha256:$(shasum -a 256 "$destination" | awk '{print $1}')"
    actual_size=$(wc -c <"$destination" | tr -d ' ')
    if [[ -n "$expected_digest" && "$actual_digest" != "$expected_digest" ]]; then
      printf 'APT snapshot asset %s digest mismatch: expected %s, got %s.\n' "$path" "$expected_digest" "$actual_digest" >&2
      return 1
    fi
    if [[ -n "$expected_size" && "$actual_size" != "$expected_size" ]]; then
      printf 'APT snapshot asset %s size mismatch: expected %s, got %s.\n' "$path" "$expected_size" "$actual_size" >&2
      return 1
    fi
    printf '%s  %s\n' "$actual_digest" "$path" >>"$manifest_file"
  }

  fetch_and_record 'dists/stable/Release' "$release_file" "$release_digest"
  fetch_and_record 'dists/stable/InRelease' "$inrelease_file" "$inrelease_digest"
  fetch_and_record 'dists/stable/Release.gpg' "$detached_file"
  fetch_and_record 'pool/main/a/artifact-gateway-e2e/artifact-gateway-e2e_1.0.0-1_all.deb' \
    "$package_copy" "sha256:$package_sha256" "$package_size"

  local index=0 digest size relative direct_path by_hash_path
  while read -r digest size relative; do
    [[ "$digest" =~ ^[0-9a-f]{64}$ && "$size" =~ ^[0-9]+$ && -n "$relative" ]] || {
      printf 'APT Release contains an invalid SHA256 entry.\n' >&2
      return 1
    }
    direct_path="dists/stable/$relative"
    by_hash_path="${direct_path%/*}/by-hash/SHA256/$digest"
    fetch_and_record "$direct_path" "$capture_dir/index-$index" "sha256:$digest" "$size"
    fetch_and_record "$by_hash_path" "$capture_dir/by-hash-$index" "sha256:$digest" "$size"
    index=$((index + 1))
  done < <(awk '/^SHA256:$/ { found = 1; next } found && NF == 3 { print $1, $2, $3 }' "$release_file")
  [[ "$index" == 2 ]] || {
    printf 'APT Release did not describe exactly Packages and Packages.gz.\n' >&2
    return 1
  }
}

apt_install
original_state="$workdir/original-signing-state.json"
original_capture="$workdir/original-snapshot"
signing_state >"$original_state"
python3 - "$original_state" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    state = json.load(source)
snapshot = state.get("currentSnapshot") or {}
if state.get("readiness") != "fixture" or snapshot.get("sequence") != 1 or snapshot.get("state") != "visible":
    raise SystemExit("initial APT signing state is not the visible fixture snapshot at sequence 1")
PY
capture_signed_snapshot "$original_capture" "$original_state"

backup_dir="$workdir/backup"
COMPOSE_PROJECT_NAME="$project" GATEWAY_ENV_FILE="$env_file" \
  "$root/scripts/backup-drill.sh" "$backup_dir"
wait_gateway_ready
"${compose[@]}" up -d --force-recreate --wait reference-apt-signer >/dev/null

mutation_response="$workdir/mutation-snapshot.json"
mutation_body=$(printf '{"suite":"stable","sequence":2,"publicationSessionIds":["%s"]}' "$session_id")
mutation_status=$(curl --silent --show-error --output "$mutation_response" --write-out '%{http_code}' \
  --request POST "$gateway_url/api/v2/repositories/$repository_id/apt/snapshots" \
  --header "Authorization: Bearer $admin_token" \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: apt-hosted-e2e-post-backup-mutation' \
  --data "$mutation_body")
if [[ "$mutation_status" != 201 ]] || ! grep -q '"state":"visible"' "$mutation_response"; then
  printf 'publish post-backup mutation failed: HTTP %s\n' "$mutation_status" >&2
  sed -n '1,20p' "$mutation_response" >&2
  exit 1
fi
mutated_state="$workdir/mutated-signing-state.json"
signing_state >"$mutated_state"
python3 - "$original_state" "$mutated_state" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    original = json.load(source)["currentSnapshot"]
with open(sys.argv[2], encoding="utf-8") as source:
    mutated = json.load(source)["currentSnapshot"]
if mutated.get("sequence") != 2 or mutated.get("id") == original.get("id") or mutated.get("releaseDigest") == original.get("releaseDigest"):
    raise SystemExit("post-backup APT mutation did not replace the visible signed snapshot")
PY

COMPOSE_PROJECT_NAME="$project" GATEWAY_ENV_FILE="$env_file" \
  "$root/scripts/restore-drill.sh" "$backup_dir"
wait_gateway_ready
restored_state="$workdir/restored-signing-state.json"
restored_capture="$workdir/restored-snapshot"
signing_state >"$restored_state"
python3 - "$original_state" "$restored_state" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    original = json.load(source)["currentSnapshot"]
with open(sys.argv[2], encoding="utf-8") as source:
    restored = json.load(source)["currentSnapshot"]
if restored != original:
    raise SystemExit("restored APT signing evidence does not exactly match the backed-up immutable snapshot")
PY
capture_signed_snapshot "$restored_capture" "$restored_state"
if ! cmp -s "$original_capture/SHA256SUMS" "$restored_capture/SHA256SUMS"; then
  diff -u "$original_capture/SHA256SUMS" "$restored_capture/SHA256SUMS" >&2 || true
  printf 'restored APT signed snapshot bytes do not match the backup.\n' >&2
  exit 1
fi

"${compose[@]}" stop reference-apt-signer >/dev/null
apt_install

printf 'native APT Hosted E2E passed (signed publish, exact PostgreSQL/RustFS restore, and offline-signer install)\n'
