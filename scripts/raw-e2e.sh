#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

environment_file=${GATEWAY_ENV_FILE:-.env}
test -f "$environment_file" || { printf '%s\n' 'Raw E2E requires a configured environment file.' >&2; exit 1; }
# shellcheck disable=SC1091
source "$environment_file"

for name in GATEWAY_HTTP_PORT GATEWAY_ADMIN_TOKEN GATEWAY_RESOLVER_TOKEN; do
  test -n "${!name:-}" || { printf 'Missing required %s\n' "$name" >&2; exit 1; }
done

free_port() {
  python3 -c 'import socket; listener = socket.socket(); listener.bind(("127.0.0.1", 0)); print(listener.getsockname()[1]); listener.close()'
}

gateway_url="http://localhost:${GATEWAY_HTTP_PORT}"
fixture_port=$(free_port)
run_id=${RAW_E2E_RUN_ID:-"$(date +%s)-${RANDOM}"}
group="raw-ready-${run_id}"
private_group="raw-private-${run_id}"
denied_group="raw-denied-${run_id}"
workdir=$(mktemp -d)
fixture_pid=""

cleanup() {
  local status=$?
  if [[ -n "$fixture_pid" ]]; then kill "$fixture_pid" 2>/dev/null || true; fi
  rm -rf "$workdir"
  exit "$status"
}
trap cleanup EXIT

mkdir -p "$workdir/release"
printf '%s' 'raw release artifact' >"$workdir/release/app.txt"
printf '%064d\n' 0 >"$workdir/release/app.txt.sha256"
python3 -m http.server "$fixture_port" --bind 127.0.0.1 --directory "$workdir" >"$workdir/fixture.log" 2>&1 &
fixture_pid=$!
until curl --silent --show-error --fail "http://localhost:${fixture_port}/release/app.txt" >/dev/null; do
  kill -0 "$fixture_pid" 2>/dev/null || { cat "$workdir/fixture.log" >&2; exit 1; }
  sleep 1
done

create_group() {
  local name=$1 anonymous=$2 member_type=$3 endpoint=$4 hosts=${5:-[]}
  local payload status
  payload=$(printf '{"name":"%s","anonymous":%s,"cacheQuotaBytes":1048576,"members":[{"name":"fixture","type":"%s","endpoint":"%s","position":0,"anonymous":%s,"allowedHosts":%s}]}' \
    "$name" "$anonymous" "$member_type" "$endpoint" "$anonymous" "$hosts")
  status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
    -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" -H 'Content-Type: application/json' \
    --data "$payload" "$gateway_url/api/v1/raw/groups")
  [[ "$status" == 201 ]] || { printf 'Creating Raw Group %s returned HTTP %s\n' "$name" "$status" >&2; exit 1; }
}

expect_status() {
  local expected=$1 actual=$2 description=$3
  [[ "$actual" == "$expected" ]] || { printf '%s: expected HTTP %s, got %s\n' "$description" "$expected" "$actual" >&2; exit 1; }
}

authenticated_status() {
  curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
    -H "Authorization: Bearer $GATEWAY_RESOLVER_TOKEN" "$1"
}

endpoint="http://host.docker.internal:${fixture_port}"
create_group "$group" true hosted "$endpoint"
create_group "$private_group" false hosted "$endpoint"
create_group "$denied_group" true proxy 'https://example.com' '["not-example.com"]'

response=$(curl --silent --show-error "$gateway_url/raw/$group/release/app.txt")
[[ "$response" == 'raw release artifact' ]] || { printf 'Raw anonymous GET returned %q\n' "$response" >&2; exit 1; }
expect_status 200 "$(curl --silent --show-error --head --output /dev/null --write-out '%{http_code}' "$gateway_url/raw/$group/release/app.txt")" 'Raw HEAD'
range=$(curl --silent --show-error -H 'Range: bytes=4-10' "$gateway_url/raw/$group/release/app.txt")
[[ "$range" == 'release' ]] || { printf 'Raw range returned %q\n' "$range" >&2; exit 1; }
expect_status 401 "$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' "$gateway_url/raw/$private_group/release/app.txt")" 'Raw anonymous denial'
expect_status 400 "$(authenticated_status "$gateway_url/raw/$group/release/%2Funsafe")" 'Raw encoded slash rejection'
expect_status 404 "$(authenticated_status "$gateway_url/raw/$group/release/missing.txt")" 'Raw first missing request'
expect_status 404 "$(authenticated_status "$gateway_url/raw/$group/release/missing.txt")" 'Raw negative cache request'
expect_status 403 "$(authenticated_status "$gateway_url/raw/$denied_group/release/app.txt")" 'Raw Proxy allowlist denial'

# The first response was cached by the running Gateway. Removing the upstream
# proves the next read is a cache hit rather than an accidental source retry.
kill "$fixture_pid"
wait "$fixture_pid" 2>/dev/null || true
fixture_pid=""
response=$(curl --silent --show-error "$gateway_url/raw/$group/release/app.txt")
[[ "$response" == 'raw release artifact' ]] || { printf 'Raw cached GET returned %q after upstream shutdown\n' "$response" >&2; exit 1; }

audits=$(curl --silent --show-error --fail -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" "$gateway_url/api/v1/audits?group=$group")
grep -Eq '"Format":"raw"' <<<"$audits" || { printf '%s\n' 'Raw audit format was not recorded.' >&2; exit 1; }
grep -Eq '"Actor":"anonymous"' <<<"$audits" || { printf '%s\n' 'Raw anonymous audit actor was not recorded.' >&2; exit 1; }
grep -Eq '"CacheDisposition":"hit"' <<<"$audits" || { printf '%s\n' 'Raw cache-hit audit was not recorded.' >&2; exit 1; }
metrics=$(curl --silent --show-error --fail "$gateway_url/metrics")
grep -Eq 'artifact_gateway_raw_cache_requests_total\{outcome="hit"\} [1-9]' <<<"$metrics" || { printf '%s\n' 'Raw cache-hit metric did not increment.' >&2; exit 1; }

printf '%s\n' 'Raw HTTP E2E passed: live Gateway GET/HEAD/range, anonymous policy, canonical rejection, negative cache, allowlist denial, cache recovery, audit, and metrics.'
