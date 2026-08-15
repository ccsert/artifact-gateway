#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

for command_name in docker openssl python3 go; do
  command -v "$command_name" >/dev/null 2>&1 || {
    printf '%s is required for the APT signer rotation E2E.\n' "$command_name" >&2
    exit 1
  }
done

debian_image='debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241'
project="artifact-gateway-apt-rotation-$$"
workdir=$(mktemp -d "${TMPDIR:-/tmp}/artifact-gateway-apt-rotation.XXXXXX")
env_file="$workdir/e2e.env"
admin_token='apt-rotation-admin-token'
resolver_token='apt-rotation-resolver-token'
signer_token='apt-rotation-external-signer-token-0001'

chmod 0700 "$workdir"
cleanup() {
  docker compose --env-file "$env_file" -p "$project" -f compose.yml -f deploy/compose/apt-signer-rotation.yml \
    --profile apt-signer-rotation down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$workdir"
}
trap cleanup EXIT

openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 2 \
  -subj '/CN=Artifact Gateway APT Signer E2E Root' \
  -keyout "$workdir/ca.key" -out "$workdir/ca.pem" >/dev/null 2>&1
openssl req -newkey rsa:2048 -sha256 -nodes \
  -subj '/CN=reference-apt-signer-old' \
  -addext 'subjectAltName=DNS:reference-apt-signer-old,DNS:reference-apt-signer-next,IP:127.0.0.1' \
  -keyout "$workdir/tls.key" -out "$workdir/tls.csr" >/dev/null 2>&1
openssl x509 -req -sha256 -days 2 -copy_extensions copy \
  -in "$workdir/tls.csr" -CA "$workdir/ca.pem" -CAkey "$workdir/ca.key" -CAcreateserial \
  -out "$workdir/tls.crt" >/dev/null 2>&1
chmod 0600 "$workdir/ca.key"
# The source directory is 0700. The TLS init container needs read access to
# this bind-mounted input before copying it to a signer-owned 0600 volume.
chmod 0644 "$workdir/ca.pem" "$workdir/tls.crt" "$workdir/tls.key"
: >"$workdir/trusted-public-keys.asc"

write_environment() {
  local service=$1 fingerprints=$2 temporary="$env_file.next"
  {
    printf '%s\n' \
      'GATEWAY_HTTP_PORT=0' \
      'GATEWAY_POSTGRES_PORT=0' \
      'RUSTFS_API_PORT=0' \
      'RUSTFS_CONSOLE_PORT=0' \
      'GATEWAY_POSTGRES_PASSWORD=apt-rotation-postgres-password' \
      'RUSTFS_ACCESS_KEY=apt-rotation-access-key' \
      'RUSTFS_SECRET_KEY=apt-rotation-rustfs-secret-key-000001' \
      'RUSTFS_RPC_SECRET=apt-rotation-rustfs-rpc-secret-000001' \
      "GATEWAY_ADMIN_TOKEN=$admin_token" \
      "GATEWAY_RESOLVER_TOKEN=$resolver_token" \
      "GATEWAY_APT_SIGNER_TOKEN=$signer_token" \
      'GATEWAY_APT_SIGNER_TIMEOUT=30s' \
      'REFERENCE_APT_SIGNER_RSA_BITS=2048' \
      "APT_SIGNER_ROTATION_DIR=$workdir" \
      "APT_SIGNER_ROTATION_SERVICE=$service" \
      "GATEWAY_APT_SIGNER_TRUSTED_FINGERPRINTS=$fingerprints"
  } >"$temporary"
  chmod 0600 "$temporary"
  mv "$temporary" "$env_file"
}

write_environment reference-apt-signer-old aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
compose=(docker compose --env-file "$env_file" -p "$project" -f compose.yml -f deploy/compose/apt-signer-rotation.yml --profile apt-signer-rotation)
"${compose[@]}" build reference-apt-signer-old reference-apt-signer-next reference-apt-signer-old-keygen reference-apt-signer-next-keygen reference-apt-signer-tls-init
"${compose[@]}" run --rm --no-deps -T reference-apt-signer-tls-init
"${compose[@]}" run --rm --no-deps -T reference-apt-signer-old-keygen >"$workdir/old.asc"
"${compose[@]}" run --rm --no-deps -T reference-apt-signer-next-keygen >"$workdir/next.asc"
"${compose[@]}" up -d --wait reference-apt-signer-old reference-apt-signer-next

for signer_service in reference-apt-signer-old reference-apt-signer-next; do
  signer_container=$("${compose[@]}" ps -q "$signer_service")
  signer_key_mount_writable=$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/var/lib/reference-apt-signer"}}{{.RW}}{{end}}{{end}}' "$signer_container")
  [[ "$signer_key_mount_writable" == false ]] || {
    printf '%s private-key volume is not mounted read-only\n' "$signer_service" >&2
    exit 1
  }
  signer_tls_mount_writable=$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/run/tls"}}{{.RW}}{{end}}{{end}}' "$signer_container")
  [[ "$signer_tls_mount_writable" == false ]] || {
    printf '%s TLS material volume is not mounted read-only\n' "$signer_service" >&2
    exit 1
  }
done

grep -q 'BEGIN PGP PUBLIC KEY BLOCK' "$workdir/old.asc"
grep -q 'BEGIN PGP PUBLIC KEY BLOCK' "$workdir/next.asc"
"${compose[@]}" exec -T reference-apt-signer-old /usr/local/bin/reference-apt-signer-healthcheck public-key >"$workdir/old-served.asc"
"${compose[@]}" exec -T reference-apt-signer-next /usr/local/bin/reference-apt-signer-healthcheck public-key >"$workdir/next-served.asc"
cmp -s "$workdir/old.asc" "$workdir/old-served.asc"
cmp -s "$workdir/next.asc" "$workdir/next-served.asc"

go run ./cmd/reference-apt-signer-keyring "$workdir/old.asc" "$workdir/next.asc" >"$workdir/rotation-public-keys.asc"
fingerprints=$(go run ./cmd/reference-apt-signer-keyring --fingerprints "$workdir/old.asc" "$workdir/next.asc")
old_fingerprint=${fingerprints%%,*}
next_fingerprint=${fingerprints##*,}
[[ "$old_fingerprint" =~ ^[0-9a-f]{40}$ && "$next_fingerprint" =~ ^[0-9a-f]{40}$ && "$old_fingerprint" != "$next_fingerprint" ]] || {
  printf 'rotation signer fingerprints are invalid or identical\n' >&2
  exit 1
}

install -m 0644 "$workdir/old.asc" "$workdir/trusted-public-keys.asc"
write_environment reference-apt-signer-old "$old_fingerprint"
"${compose[@]}" up -d --build --wait gateway

refresh_gateway_url() {
  local published_port
  published_port=$("${compose[@]}" port gateway 8080 | tail -n 1)
  [[ -n "$published_port" ]] || return 1
  gateway_url="http://127.0.0.1:${published_port##*:}"
}

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
  printf 'Gateway did not return readiness 204; last HTTP status was %s.\n' "$ready_code" >&2
  return 1
}

restart_gateway() {
  "${compose[@]}" up -d --force-recreate --wait gateway >/dev/null
  wait_gateway_ready
}

wait_gateway_ready
curl_request=(curl --noproxy '*' --connect-timeout 2 --max-time 45 --silent --show-error)
package_file="$workdir/artifact-gateway-rotation_1.0.0-1_all.deb"
docker run --rm --volume "$workdir:/work" "$debian_image" /bin/sh -ec '
  mkdir -p /tmp/artifact-gateway-rotation/DEBIAN /tmp/artifact-gateway-rotation/usr/share/artifact-gateway-rotation
  printf "%s\n" \
    "Package: artifact-gateway-rotation" \
    "Version: 1.0.0-1" \
    "Architecture: all" \
    "Maintainer: Artifact Gateway Rotation <apt-rotation@example.test>" \
    "Description: external signer rotation acceptance package" \
    > /tmp/artifact-gateway-rotation/DEBIAN/control
  printf "%s\n" "installed-from-rotated-signer" > /tmp/artifact-gateway-rotation/usr/share/artifact-gateway-rotation/installed.txt
  dpkg-deb --build --root-owner-group /tmp/artifact-gateway-rotation /work/artifact-gateway-rotation_1.0.0-1_all.deb >/dev/null
'
package_sha256=$(shasum -a 256 "$package_file" | awk '{print $1}')
package_size=$(wc -c <"$package_file" | tr -d ' ')

repository_response="$workdir/repository.json"
repository_status=$("${curl_request[@]}" --output "$repository_response" --write-out '%{http_code}' \
  --request POST "$gateway_url/api/v2/repositories" \
  --header "Authorization: Bearer $admin_token" \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: apt-signer-rotation-repository' \
  --data '{"name":"apt-signer-rotation","format":"apt","type":"hosted"}')
[[ "$repository_status" == 201 ]] || {
  printf 'create rotation repository failed: HTTP %s\n' "$repository_status" >&2
  sed -n '1,20p' "$repository_response" >&2
  exit 1
}
repository_id=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "$repository_response")

session_response="$workdir/session.json"
session_body=$(printf '{"suite":"stable","component":"main","objectName":"artifact-gateway-rotation_1.0.0-1_all.deb","declaredDigest":"sha256:%s","declaredSize":%s,"expectedIdentity":"artifact-gateway-rotation@1.0.0-1#all"}' "$package_sha256" "$package_size")
session_status=$("${curl_request[@]}" --output "$session_response" --write-out '%{http_code}' \
  --request POST "$gateway_url/api/v2/repositories/$repository_id/apt/publication-sessions" \
  --header "Authorization: Bearer $admin_token" \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: apt-signer-rotation-session' \
  --data "$session_body")
[[ "$session_status" == 201 ]] || {
  printf 'create rotation publication session failed: HTTP %s\n' "$session_status" >&2
  sed -n '1,20p' "$session_response" >&2
  exit 1
}
session_id=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "$session_response")

upload_status=$("${curl_request[@]}" --output "$workdir/upload.json" --write-out '%{http_code}' \
  --request PUT "$gateway_url/api/v2/repositories/$repository_id/apt/publication-sessions/$session_id/package" \
  --header "Authorization: Bearer $admin_token" \
  --header 'Content-Type: application/vnd.debian.binary-package' \
  --data-binary "@$package_file")
[[ "$upload_status" == 200 ]] || {
  printf 'upload rotation package failed: HTTP %s\n' "$upload_status" >&2
  exit 1
}

publish_snapshot() {
  local sequence=$1 key=$2 status body
  local response="$workdir/snapshot-$sequence.json"
  body=$(printf '{"suite":"stable","sequence":%s,"publicationSessionIds":["%s"]}' "$sequence" "$session_id")
  status=$("${curl_request[@]}" --output "$response" --write-out '%{http_code}' \
    --request POST "$gateway_url/api/v2/repositories/$repository_id/apt/snapshots" \
    --header "Authorization: Bearer $admin_token" \
    --header 'Content-Type: application/json' \
    --header "Idempotency-Key: $key" \
    --data "$body")
  if [[ "$status" != 201 ]] || ! grep -q '"state":"visible"' "$response"; then
    printf 'publish rotation snapshot %s failed: HTTP %s\n' "$sequence" "$status" >&2
    sed -n '1,20p' "$response" >&2
    return 1
  fi
}

assert_signing_state() {
  local readiness=$1 role=$2 sequence=$3 expected_fingerprints=$4
  local state_file="$workdir/state-$sequence-$role.json"
  "${curl_request[@]}" --fail --header "Authorization: Bearer $admin_token" \
    "$gateway_url/api/v2/repositories/$repository_id/apt/signing-state" >"$state_file"
  python3 - "$state_file" "$readiness" "$role" "$sequence" "$expected_fingerprints" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    state = json.load(source)
expected_fingerprints = sys.argv[5].split(",")
snapshot = state.get("currentSnapshot") or {}
if state.get("signerMode") != "remote" or state.get("readiness") != sys.argv[2]:
    raise SystemExit(f"unexpected signer mode/readiness: {state}")
if state.get("currentKeyRole") != sys.argv[3] or snapshot.get("sequence") != int(sys.argv[4]):
    raise SystemExit(f"unexpected key role/snapshot sequence: {state}")
if state.get("trustedFingerprints") != expected_fingerprints:
    raise SystemExit(f"unexpected trusted fingerprints: {state}")
PY
}

gateway_container=$("${compose[@]}" ps -q gateway)
gateway_network=$(docker inspect --format '{{range $name, $settings := .NetworkSettings.Networks}}{{$name}}{{end}}' "$gateway_container")
[[ -n "$gateway_network" ]] || {
  printf 'Gateway network was not found\n' >&2
  exit 1
}
if docker inspect --format '{{range .Mounts}}{{println .Name .Destination}}{{end}}' "$gateway_container" | grep -q 'gateway-apt-signer-'; then
  printf 'Gateway unexpectedly mounts a signer private-key volume\n' >&2
  exit 1
fi

apt_verify() {
  local keyring=$1
  docker run --rm \
    --network "$gateway_network" \
    --env "APT_ROTATION_RESOLVER_TOKEN=$resolver_token" \
    --volume "$keyring:/keys/artifact-gateway.asc:ro" \
    "$debian_image" /bin/sh -ec '
      rm -f /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources
      install -d -m 0755 /etc/apt/keyrings /etc/apt/auth.conf.d
      install -m 0644 /keys/artifact-gateway.asc /etc/apt/keyrings/artifact-gateway.asc
      printf "machine http://gateway:8080/apt/apt-signer-rotation\nlogin resolver\npassword %s\n" "$APT_ROTATION_RESOLVER_TOKEN" > /etc/apt/auth.conf.d/artifact-gateway.conf
      chmod 0600 /etc/apt/auth.conf.d/artifact-gateway.conf
      printf "%s\n" "deb [arch=all signed-by=/etc/apt/keyrings/artifact-gateway.asc] http://gateway:8080/apt/apt-signer-rotation stable main" > /etc/apt/sources.list.d/artifact-gateway.list
      apt-get -o Acquire::Retries=0 update >/dev/null
      apt-get -o Acquire::Retries=0 install -y --no-install-recommends artifact-gateway-rotation=1.0.0-1 >/dev/null
      grep -Fxq installed-from-rotated-signer /usr/share/artifact-gateway-rotation/installed.txt
    '
}

apt_rejects() {
  local keyring=$1 expected_key_id=${next_fingerprint: -16}
  local rejection_log="$workdir/old-key-rejection.log"
  if docker run --rm \
    --network "$gateway_network" \
    --env "APT_ROTATION_RESOLVER_TOKEN=$resolver_token" \
    --env 'LC_ALL=C' \
    --volume "$keyring:/keys/artifact-gateway.asc:ro" \
    "$debian_image" /bin/sh -ec '
      rm -f /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources
      install -d -m 0755 /etc/apt/keyrings /etc/apt/auth.conf.d
      install -m 0644 /keys/artifact-gateway.asc /etc/apt/keyrings/artifact-gateway.asc
      printf "machine http://gateway:8080/apt/apt-signer-rotation\nlogin resolver\npassword %s\n" "$APT_ROTATION_RESOLVER_TOKEN" > /etc/apt/auth.conf.d/artifact-gateway.conf
      chmod 0600 /etc/apt/auth.conf.d/artifact-gateway.conf
      printf "%s\n" "deb [arch=all signed-by=/etc/apt/keyrings/artifact-gateway.asc] http://gateway:8080/apt/apt-signer-rotation stable main" > /etc/apt/sources.list.d/artifact-gateway.list
      apt-get -o Acquire::Retries=0 update
    ' >"$rejection_log" 2>&1; then
    printf 'APT unexpectedly trusted a snapshot signed outside the client keyring\n' >&2
    return 1
  fi
  if ! grep -Eqi "NO_PUBKEY[[:space:]]+$expected_key_id" "$rejection_log"; then
    printf 'APT failed for an unexpected reason instead of rejecting the signing key\n' >&2
    sed -n '1,80p' "$rejection_log" >&2
    return 1
  fi
}

publish_snapshot 1 apt-signer-rotation-old
assert_signing_state ready active 1 "$old_fingerprint"
apt_verify "$workdir/old.asc"

install -m 0644 "$workdir/rotation-public-keys.asc" "$workdir/trusted-public-keys.asc"
write_environment reference-apt-signer-old "$old_fingerprint,$next_fingerprint"
restart_gateway
assert_signing_state rotation_overlap active 1 "$old_fingerprint,$next_fingerprint"
apt_verify "$workdir/rotation-public-keys.asc"

write_environment reference-apt-signer-next "$old_fingerprint,$next_fingerprint"
restart_gateway
publish_snapshot 2 apt-signer-rotation-next
assert_signing_state rotation_overlap next 2 "$old_fingerprint,$next_fingerprint"
apt_verify "$workdir/rotation-public-keys.asc"
apt_rejects "$workdir/old.asc"

install -m 0644 "$workdir/next.asc" "$workdir/trusted-public-keys.asc"
write_environment reference-apt-signer-next "$next_fingerprint"
restart_gateway
publish_snapshot 3 apt-signer-rotation-retire-old
assert_signing_state ready active 3 "$next_fingerprint"
apt_verify "$workdir/next.asc"

printf '%s\n' 'APT external HTTPS signer rotation E2E passed (old, overlap, next, old-key rejection, and retirement).'
