#!/usr/bin/env bash
# agctl end-to-end check against a real Gateway (issues #127 and #130).
#
# By default it boots an isolated Compose project from GATEWAY_ENV_FILE (or
# .env) on free ports and removes it afterwards. To reuse a Gateway that is
# already running, set CLI_E2E_SERVER and CLI_E2E_TOKEN (an admin token); the
# script then only creates repositories with unique names and deletes them.
#
# Scenario: repository create -> update -> visible in list -> delete accepted
# (deleting) -> bounded wait for deleted -> still readable as deleted. Around
# it: the exit-code matrix (2 usage, 4 not found, 5 unauthenticated, 7
# conflict, 8 wait timeout), --dry-run without business changes, idempotent
# create replay, and a 412 that never overwrites.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

for binary in go python3; do
  command -v "$binary" >/dev/null || {
    printf 'CLI E2E requires %s.\n' "$binary" >&2
    exit 1
  }
done

workdir=$(mktemp -d)
project=""

free_port() {
  python3 -c 'import socket; listener = socket.socket(); listener.bind(("127.0.0.1", 0)); print(listener.getsockname()[1]); listener.close()'
}

compose() {
  COMPOSE_PROJECT_NAME="$project" docker compose --env-file "$workdir/gateway.env" -f compose.yml "$@"
}

cleanup() {
  local status=$?
  if [[ -n "$project" ]]; then
    if [[ "$status" != 0 ]]; then
      compose logs gateway 2>/dev/null | tail -n 80 >&2 || true
    fi
    compose down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$workdir"
  trap - EXIT
  exit "$status"
}
trap cleanup EXIT

if [[ -n "${CLI_E2E_SERVER:-}" ]]; then
  [[ -n "${CLI_E2E_TOKEN:-}" ]] || {
    printf '%s\n' 'CLI_E2E_SERVER needs CLI_E2E_TOKEN (an admin token).' >&2
    exit 1
  }
  server=$CLI_E2E_SERVER
  admin_token=$CLI_E2E_TOKEN
else
  command -v docker >/dev/null || {
    printf '%s\n' 'CLI E2E requires docker, or CLI_E2E_SERVER and CLI_E2E_TOKEN for a running Gateway.' >&2
    exit 1
  }
  env_file=${GATEWAY_ENV_FILE:-.env}
  test -f "$env_file" || {
    printf '%s\n' 'CLI E2E requires a configured environment file (GATEWAY_ENV_FILE or .env).' >&2
    exit 1
  }
  project="artifact-gateway-cli-${RANDOM}-${RANDOM}"
  gateway_port=$(free_port)
  admin_token="cli-e2e-admin-${RANDOM}-${RANDOM}"
  awk -F= '
    $1 != "GATEWAY_HTTP_PORT" &&
    $1 != "GATEWAY_POSTGRES_PORT" &&
    $1 != "RUSTFS_API_PORT" &&
    $1 != "RUSTFS_CONSOLE_PORT" &&
    $1 != "GATEWAY_ADMIN_TOKEN" &&
    $1 != "GATEWAY_RESOLVER_TOKEN" { print }
  ' "$env_file" >"$workdir/gateway.env"
  printf 'GATEWAY_HTTP_PORT=%s\nGATEWAY_POSTGRES_PORT=%s\nRUSTFS_API_PORT=%s\nRUSTFS_CONSOLE_PORT=%s\nGATEWAY_ADMIN_TOKEN=%s\nGATEWAY_RESOLVER_TOKEN=%s\n' \
    "$gateway_port" "$(free_port)" "$(free_port)" "$(free_port)" \
    "$admin_token" "cli-e2e-resolver-${RANDOM}-${RANDOM}" >>"$workdir/gateway.env"
  compose up -d --build --wait
  server="http://127.0.0.1:${gateway_port}"
fi

go build -o "$workdir/agctl" ./cmd/agctl

# Every invocation runs without a profile file, with credentials only from
# the environment, and with stdin closed: agctl must never prompt.
agctl() {
  HOME="$workdir/home" AGCTL_SERVER="$server" AGCTL_TOKEN="$admin_token" AGCTL_PROFILE="" \
    "$workdir/agctl" "$@" </dev/null
}

# expect CODE OUTPUT -- ARGS...: run agctl, keep stdout in OUTPUT, and require
# the exit code.
expect() {
  local want=$1 output=$2
  shift 3
  local got=0
  agctl "$@" >"$output" 2>"$workdir/stderr" || got=$?
  if [[ "$got" != "$want" ]]; then
    printf 'agctl %s exited %s; expected %s.\n' "$*" "$got" "$want" >&2
    cat "$output" "$workdir/stderr" >&2
    exit 1
  fi
}

# field FILE PATH: read a dotted path from a JSON envelope; prints "null" for
# null and lowercase booleans.
field() {
  python3 - "$1" "$2" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    value = json.load(stream)
for part in sys.argv[2].split("."):
    value = value[int(part)] if isinstance(value, list) else value[part]
print(json.dumps(value) if not isinstance(value, str) else value)
PY
}

require() {
  local actual=$1 expected=$2 message=$3
  [[ "$actual" == "$expected" ]] || {
    printf '%s: got %s, expected %s.\n' "$message" "$actual" "$expected" >&2
    exit 1
  }
}

suffix="${RANDOM}${RANDOM}"
name="cli-e2e-npm-${suffix}"
out="$workdir/out.json"

# Exit-code matrix before any change.
got=0
HOME="$workdir/home" AGCTL_SERVER="$server" AGCTL_TOKEN="wrong-token" AGCTL_PROFILE="" \
  "$workdir/agctl" repo list --json >"$out" 2>/dev/null </dev/null || got=$?
require "$got" 5 "wrong token exit code"
require "$(field "$out" ok)" false "wrong token envelope"
expect 4 "$out" -- repo show 00000000-0000-4000-8000-000000000000 --json
require "$(field "$out" error.httpStatus)" 404 "missing repository status"
expect 2 "$out" -- repo create "Bad_Name" --format npm --json
require "$(field "$out" error.code)" usage_error "invalid name code"

# create --dry-run submits nothing.
expect 0 "$out" -- repo create "$name" --format npm --dry-run --json
require "$(field "$out" data.mode)" client-plan "create dry-run mode"
expect 0 "$out" -- repo list --page-size 200 --all --json
python3 - "$out" "$name" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    names = [item["name"] for item in json.load(stream)["data"]["items"]]
if sys.argv[2] in names:
    raise SystemExit("create --dry-run created the repository")
PY

# Create, then replay the same key: the same repository comes back.
key="cli-e2e-${suffix}"
expect 0 "$out" -- repo create "$name" --format npm --idempotency-key "$key" --json
repo_id=$(field "$out" data.id)
require "$(field "$out" data.state)" active "created state"
version=$(field "$out" data.version)
expect 0 "$out" -- repo create "$name" --format npm --idempotency-key "$key" --json
require "$(field "$out" data.id)" "$repo_id" "idempotent replay id"
expect 7 "$out" -- repo create "$name" --format npm --json
require "$(field "$out" error.code)" version_conflict "duplicate name code"

# Update: dry-run changes nothing, a real update bumps the version, and a
# stale If-Match fails with 412 without overwriting.
expect 0 "$out" -- repo update "$repo_id" --anonymous-read=true --dry-run --json
require "$(field "$out" data.headers.If-Match)" "$version" "update dry-run If-Match"
expect 0 "$out" -- repo show "$repo_id" --json
require "$(field "$out" data.version)" "$version" "version after update dry-run"
require "$(field "$out" data.anonymousRead)" false "anonymousRead after update dry-run"
expect 0 "$out" -- repo update "$repo_id" --anonymous-read=true --json
require "$(field "$out" data.anonymousRead)" true "updated anonymousRead"
updated_version=$(field "$out" data.version)
[[ "$updated_version" != "$version" ]] || {
  printf '%s\n' 'update did not change the repository version' >&2
  exit 1
}
expect 7 "$out" -- repo update "$repo_id" --anonymous-read=false --if-match "$version" --json
require "$(field "$out" error.httpStatus)" 412 "stale If-Match status"
expect 0 "$out" -- repo show "$repo_id" --json
require "$(field "$out" data.anonymousRead)" true "anonymousRead after 412"
require "$(field "$out" data.version)" "$updated_version" "version after 412"
# A hosted repository refuses proxy settings on the server side (400).
expect 1 "$out" -- repo update "$repo_id" --endpoint https://registry.npmjs.org/ --json
require "$(field "$out" error.httpStatus)" 400 "hosted endpoint update status"
expect 0 "$out" -- repo show "$repo_id" --json
require "$(field "$out" data.version)" "$updated_version" "version after refused update"

# List: the repository is visible, one page at a time.
expect 0 "$out" -- repo list --page-size 1 --json
require "$(field "$out" meta.pagination.mode)" cursor "list pagination mode"
require "$(field "$out" meta.pagination.pages)" 1 "default list reads one page"
expect 0 "$out" -- repo list --all --page-size 200 --json
python3 - "$out" "$repo_id" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    envelope = json.load(stream)
ids = [item["id"] for item in envelope["data"]["items"]]
if sys.argv[2] not in ids:
    raise SystemExit("the created repository is not visible in the list")
if envelope["meta"]["pagination"]["complete"] is not True:
    raise SystemExit("--all did not reach the end of the listing")
PY

# Delete: confirmation and dry-run first, then acceptance, which is not
# completion, then a bounded wait for the terminal state.
expect 2 "$out" -- repo delete "$repo_id" --json
expect 0 "$out" -- repo delete "$repo_id" --dry-run --json
require "$(field "$out" data.current.state)" active "delete dry-run state"
expect 0 "$out" -- repo show "$repo_id" --json
require "$(field "$out" data.state)" active "state after delete dry-run"
expect 0 "$out" -- repo delete "$repo_id" --yes --json
require "$(field "$out" data.status)" accepted "delete status"
require "$(field "$out" data.terminal)" false "delete is not terminal on acceptance"
expect 0 "$out" -- repo show "$repo_id" --json
state=$(field "$out" data.state)
[[ "$state" == deleting || "$state" == deleted ]] || {
  printf 'state after delete is %s; expected deleting or deleted.\n' "$state" >&2
  exit 1
}

# A short wait times out with exit 8 and leaves the deletion running. The
# worker scans once a minute, so a one-second budget almost always expires
# first; when it does not, the repository is already deleted.
second="cli-e2e-raw-${suffix}"
expect 0 "$out" -- repo create "$second" --format raw --json
second_id=$(field "$out" data.id)
got=0
agctl repo delete "$second_id" --yes --wait --timeout 1s --json >"$out" 2>"$workdir/stderr" || got=$?
if [[ "$got" == 8 ]]; then
  require "$(field "$out" error.code)" wait_timeout "wait timeout code"
  require "$(field "$out" data.id)" "$second_id" "wait timeout id"
  require "$(field "$out" data.terminal)" false "wait timeout terminal"
else
  require "$got" 0 "short wait exit code"
fi

# The worker advances both repositories to deleted; wait for each within a
# bounded budget. A repeated delete of a deleting repository is accepted again.
for id in "$repo_id" "$second_id"; do
  expect 0 "$out" -- repo show "$id" --json
  if [[ "$(field "$out" data.state)" == deleted ]]; then
    continue
  fi
  expect 0 "$out" -- repo delete "$id" --yes --wait --timeout 150s --json
  require "$(field "$out" data.status)" deleted "waited delete status"
  require "$(field "$out" data.terminal)" true "waited delete terminal"
done

# deleted is a terminal management state: the metadata stays readable and a
# further delete is refused as not found.
expect 0 "$out" -- repo show "$repo_id" --json
require "$(field "$out" data.state)" deleted "final state"
expect 4 "$out" -- repo delete "$repo_id" --yes --json

printf 'agctl CLI E2E passed against %s (repository %s)\n' "$server" "$repo_id"
