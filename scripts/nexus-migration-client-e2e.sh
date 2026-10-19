#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
for binary in docker go curl python3 tar java javac cc make; do
  command -v "$binary" >/dev/null || { printf 'Nexus migration client gate requires %s\n' "$binary" >&2; exit 1; }
done

work=$(mktemp -d)
project="artifact-gateway-migration-clients-$$"
compose=(docker compose --env-file /dev/null --project-name "$project" -f compose.integration.yml)
started=0
cleanup() {
  local result=$?
  if [[ "$started" == 1 ]]; then
    "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$work"
  trap - EXIT
  exit "$result"
}
trap cleanup EXIT

# Official, maintained Maven 3.9 release. Verify the fixed Apache digest before
# executing it; no user installation, settings, cache or Maven Central is used.
archive="$work/apache-maven-3.9.16-bin.tar.gz"
curl --disable --proto '=https' --tlsv1.2 --fail --silent --show-error --location \
  --connect-timeout 15 --max-time 120 --retry 2 \
  https://archive.apache.org/dist/maven/maven-3/3.9.16/binaries/apache-maven-3.9.16-bin.tar.gz \
  --output "$archive"
python3 - "$archive" <<'PY'
import hashlib
from pathlib import Path
import sys

expected = "831a8591fe20c8243b1dbe7d71e3244f31d1665b0804b2e825e38cbbe5ce0cafb8338851f90780735568773e0a6cd07bbec107cda0b896b008b861075358b6f6"
if hashlib.sha512(Path(sys.argv[1]).read_bytes()).hexdigest() != expected:
    raise SystemExit("official Maven 3.9.16 archive SHA-512 mismatch")
PY
tar -xzf "$archive" -C "$work"

# Build a fixed official curl release in the owned temporary directory. Its
# source archive digest is published by curl/curl's curl-8_14_1 release. Only
# loopback HTTP is exercised here; source HTTPS/TLS is covered separately.
curl_archive="$work/curl-8.14.1.tar.gz"
curl --disable --proto '=https' --tlsv1.2 --fail --silent --show-error --location \
  --connect-timeout 15 --max-time 120 --retry 2 \
  https://curl.se/download/curl-8.14.1.tar.gz --output "$curl_archive"
python3 - "$curl_archive" <<'PY'
import hashlib
from pathlib import Path
import sys

expected = "6766ada7101d292b42b8b15681120acd68effa4a9660935853cf6d61f0d984d4"
if hashlib.sha256(Path(sys.argv[1]).read_bytes()).hexdigest() != expected:
    raise SystemExit("official curl 8.14.1 source SHA-256 mismatch")
PY
tar -xzf "$curl_archive" -C "$work"
if ! (
  cd "$work/curl-8.14.1"
  ./configure --disable-shared --enable-static --disable-docs \
    --disable-threaded-resolver --disable-proxy --disable-ldap --disable-ldaps \
    --disable-ftp --disable-file --disable-rtsp --disable-dict --disable-telnet \
    --disable-tftp --disable-pop3 --disable-imap --disable-smb --disable-smtp \
    --disable-gopher --disable-mqtt --disable-websockets \
    --without-ssl --without-libpsl --without-libidn2 --without-brotli \
    --without-zstd --without-zlib --without-libssh2 --without-librtmp \
    --without-nghttp2 --without-nghttp3 --without-ngtcp2
  make -j4
) >"$work/curl-build.log" 2>&1; then
  cat "$work/curl-build.log" >&2
  exit 1
fi

started=1
"${compose[@]}" up -d --wait postgres rustfs
"${compose[@]}" run --rm --no-deps rustfs-ready
"${compose[@]}" run --rm --no-deps migrate
postgres_address=$("${compose[@]}" port postgres 5432)
rustfs_address=$("${compose[@]}" port rustfs 9000)
if ! TEST_DATABASE_URL="postgres://gateway:integration-password@${postgres_address}/gateway_test?sslmode=disable" \
  TEST_RUSTFS_ENDPOINT="http://${rustfs_address}" \
  TEST_RUSTFS_ACCESS_KEY=integration-rustfs TEST_RUSTFS_SECRET_KEY=integration-password \
  MIGRATION_MAVEN_BIN="$work/apache-maven-3.9.16/bin/mvn" \
  MIGRATION_CURL_BIN="$work/curl-8.14.1/src/curl" \
  go test -json -count=1 -tags=integration,mavenclient ./internal/nexusmigration/pgstore \
    -run '^TestPostgresMigrationTargetOnly(Maven|Raw)Client$' >"$work/clients.jsonl"; then
  cat "$work/clients.jsonl" >&2
  exit 1
fi

# A Go test process can succeed with no matching tests or skipped fixtures.
# Both official-client proofs must actually finish, rather than merely compile.
python3 - "$work/clients.jsonl" <<'PY'
import json
from pathlib import Path
import sys

required = {"TestPostgresMigrationTargetOnlyMavenClient", "TestPostgresMigrationTargetOnlyRawClient"}
passed = set()
for line in Path(sys.argv[1]).read_text().splitlines():
    event = json.loads(line)
    if event.get("Output"):
        print(event["Output"], end="")
    if event.get("Action") == "pass" and event.get("Test") in required:
        passed.add(event["Test"])
if passed != required:
    raise SystemExit("mandatory migration client proof was missing or skipped: " + ", ".join(sorted(required - passed)))
print("Nexus migration target-only client gate passed: pinned Maven 3.9.16 and curl 8.14.1, source stopped, empty artifact caches, exact inventory size/SHA-256. Loopback HTTP client proof; production TLS/cutover is not asserted.")
PY
