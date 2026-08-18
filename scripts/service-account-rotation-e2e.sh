#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

env_file=${GATEWAY_ENV_FILE:-.env}
test -f "$env_file" || {
  printf '%s\n' 'Service Account rotation E2E requires a configured environment file.' >&2
  exit 1
}

for binary in curl docker python3; do
  command -v "$binary" >/dev/null || {
    printf 'Service Account rotation E2E requires %s.\n' "$binary" >&2
    exit 1
  }
done

free_port() {
  python3 -c 'import socket; listener = socket.socket(); listener.bind(("127.0.0.1", 0)); print(listener.getsockname()[1]); listener.close()'
}

project="artifact-gateway-service-account-${RANDOM}-${RANDOM}"
workdir=$(mktemp -d)
isolated_env="$workdir/gateway.env"
gateway_port=$(free_port)
admin_token="service-account-gate-admin-${RANDOM}-${RANDOM}"

awk -F= '
  $1 != "GATEWAY_HTTP_PORT" &&
  $1 != "GATEWAY_POSTGRES_PORT" &&
  $1 != "RUSTFS_API_PORT" &&
  $1 != "RUSTFS_CONSOLE_PORT" &&
  $1 != "GATEWAY_ADMIN_TOKEN" &&
  $1 != "GATEWAY_RESOLVER_TOKEN" { print }
' "$env_file" >"$isolated_env"
printf 'GATEWAY_HTTP_PORT=%s\nGATEWAY_POSTGRES_PORT=%s\nRUSTFS_API_PORT=%s\nRUSTFS_CONSOLE_PORT=%s\nGATEWAY_ADMIN_TOKEN=%s\nGATEWAY_RESOLVER_TOKEN=%s\n' \
  "$gateway_port" "$(free_port)" "$(free_port)" "$(free_port)" \
  "$admin_token" "service-account-gate-resolver-${RANDOM}-${RANDOM}" >>"$isolated_env"

compose() {
  COMPOSE_PROJECT_NAME="$project" docker compose --env-file "$isolated_env" -f compose.yml "$@"
}

cleanup() {
  local status=$?
  compose down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$workdir"
  trap - EXIT
  exit "$status"
}
trap cleanup EXIT

base="http://127.0.0.1:${gateway_port}"

json_field() {
  local file=$1 field=$2
  python3 - "$file" "$field" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    value = json.load(stream)
for part in sys.argv[2].split("."):
    value = value[part]
print(value)
PY
}

request() {
  local expected=$1 method=$2 path=$3 token=$4 output=$5
  shift 5
  local actual
  actual=$(curl --silent --show-error --output "$output" --write-out '%{http_code}' \
    --request "$method" -H "Authorization: Bearer $token" "$@" "$base$path")
  if [[ "$actual" != "$expected" ]]; then
    printf '%s %s returned HTTP %s; expected %s.\n' "$method" "$path" "$actual" "$expected" >&2
    exit 1
  fi
}

request_basic() {
  local expected=$1 method=$2 path=$3 username=$4 password=$5 output=$6
  shift 6
  local actual
  actual=$(curl --silent --show-error --output "$output" --write-out '%{http_code}' \
    --request "$method" --user "${username}:${password}" "$@" "$base$path")
  if [[ "$actual" != "$expected" ]]; then
    printf '%s %s with Basic authentication returned HTTP %s; expected %s.\n' "$method" "$path" "$actual" "$expected" >&2
    exit 1
  fi
}

assert_identity() {
  local token=$1 expected_actor=$2 output="$workdir/identity.json"
  request 200 GET /api/v2/identity "$token" "$output"
  [[ "$(json_field "$output" actor)" == "$expected_actor" ]] || {
    printf '%s\n' 'Service Account credential resolved to a different actor.' >&2
    exit 1
  }
  [[ "$(json_field "$output" kind)" == service_account_credential ]] || {
    printf '%s\n' 'Service Account credential resolved to a different authentication kind.' >&2
    exit 1
  }
}

compose up -d --build --wait

repository_response="$workdir/repository.json"
request 201 POST /api/v2/repositories "$admin_token" "$repository_response" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: service-account-rotation-repository' \
  --data '{"name":"service-account-rotation-releases","format":"raw"}'
repository_id=$(json_field "$repository_response" id)
repository_version=$(json_field "$repository_response" version)

account_response="$workdir/service-account.json"
request 201 POST /api/v2/service-accounts "$admin_token" "$account_response" \
  -H 'Content-Type: application/json' \
  --data '{"name":"release-bot","description":"Service Account rotation release gate"}'
account_id=$(json_field "$account_response" id)
account_version=$(json_field "$account_response" version)
principal="service-account:${account_id}"
account_page="$workdir/service-accounts.json"
request 200 GET '/api/v2/service-accounts?pageSize=1' "$admin_token" "$account_page"
python3 - "$account_page" "$account_id" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    page = json.load(stream)
if [item["id"] for item in page["items"]] != [sys.argv[2]]:
    raise SystemExit("Service Account page did not return the created stable principal.")
PY

old_response="$workdir/old-credential.json"
new_response="$workdir/new-credential.json"
request 201 POST "/api/v2/service-accounts/${account_id}/credentials" "$admin_token" "$old_response" \
  -H 'Content-Type: application/json' --data '{"name":"jenkins-old"}'
request 201 POST "/api/v2/service-accounts/${account_id}/credentials" "$admin_token" "$new_response" \
  -H 'Content-Type: application/json' --data '{"name":"jenkins-new"}'
old_credential_id=$(json_field "$old_response" id)
old_token=$(json_field "$old_response" token)
new_token=$(json_field "$new_response" token)

grant_response="$workdir/grants.json"
request 200 PUT "/api/v2/repositories/${repository_id}/grants" "$admin_token" "$grant_response" \
  -H 'Content-Type: application/json' -H "If-Match: ${repository_version}" \
  --data "[{\"principal\":\"${principal}\",\"scopes\":[\"repositories:write\"]}]"

assert_identity "$old_token" "$principal"
assert_identity "$new_token" "$principal"
request 200 GET "/api/v2/repositories/${repository_id}" "$old_token" "$workdir/old-read.json"
request 200 GET "/api/v2/repositories/${repository_id}" "$new_token" "$workdir/new-read.json"
raw_path="/raw/service-account-rotation-releases/releases/app.txt"
request_basic 201 PUT "$raw_path" jenkins "$old_token" "$workdir/raw-publish.txt" \
  -H 'Content-Type: text/plain' --data-binary 'service account rotation artifact'
request_basic 200 GET "$raw_path" jenkins "$new_token" "$workdir/raw-read.txt"
[[ "$(cat "$workdir/raw-read.txt")" == 'service account rotation artifact' ]] || {
  printf '%s\n' 'Rotated Service Account credential read different Raw artifact bytes.' >&2
  exit 1
}

request 200 DELETE "/api/v2/service-accounts/${account_id}/credentials/${old_credential_id}" "$admin_token" "$workdir/revoked.json"
request 401 GET /api/v2/identity "$old_token" "$workdir/old-rejected.json"
request_basic 401 GET "$raw_path" jenkins "$old_token" "$workdir/old-basic-rejected.txt"
assert_identity "$new_token" "$principal"
request 200 GET "/api/v2/repositories/${repository_id}" "$new_token" "$workdir/new-read-after-rotation.json"
request_basic 200 GET "$raw_path" jenkins "$new_token" "$workdir/new-basic-after-rotation.txt"

request 200 PUT "/api/v2/service-accounts/${account_id}" "$admin_token" "$workdir/disabled.json" \
  -H 'Content-Type: application/json' -H "If-Match: ${account_version}" \
  --data '{"state":"disabled"}'
request 401 GET /api/v2/identity "$new_token" "$workdir/new-rejected.json"
request_basic 401 GET "$raw_path" jenkins "$new_token" "$workdir/new-basic-rejected.txt"
request 409 POST "/api/v2/service-accounts/${account_id}/credentials" "$admin_token" "$workdir/disabled-issuance.json" \
  -H 'Content-Type: application/json' --data '{"name":"must-not-issue"}'

audits="$workdir/audits.json"
request 200 GET /api/v1/audits "$admin_token" "$audits"
for operation in service_account.create service_account.credential.create service_account.credential.revoke service_account.update; do
  grep -Fq "\"Operation\":\"${operation}\"" "$audits" || {
    printf 'Management audit %s was not recorded.\n' "$operation" >&2
    exit 1
  }
done
if grep -Fq 'agc_' "$audits"; then
  printf '%s\n' 'A Service Account credential leaked into audit output.' >&2
  exit 1
fi

printf '%s\n' 'Service Account rotation E2E passed: stable Repository Grant, overlapping credentials, isolated revocation, account disable, and sanitized audit.'
