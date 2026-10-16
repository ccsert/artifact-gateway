#!/usr/bin/env bash
# agctl end-to-end check against a real Gateway (issues #127, #129, #130,
# #131, #132, #133, #134, #135 and #335).
#
# By default it boots an isolated Compose project from GATEWAY_ENV_FILE (or
# .env) on free ports and removes it afterwards. To reuse a Gateway that is
# already running, set CLI_E2E_SERVER and CLI_E2E_TOKEN (an admin token); the
# script then only creates repositories with unique names and deletes them.
# CLI_E2E_RESOLVER_TOKEN (the Gateway's resolver token) adds the checks that
# need a credential without repository permissions; without it they are
# skipped. CLI_E2E_OIDC_ISSUER (the Gateway's configured OIDC issuer) adds
# the identity link checks; the isolated Gateway gets a synthetic issuer.
#
# Scenario: repository create -> update -> visible in list -> Group create,
# members, resolution, rename, reorder, delete -> repository delete accepted
# (deleting) -> bounded wait for deleted -> still readable as deleted. Around
# it: the exit-code matrix (2 usage, 4 not found, 5 unauthenticated, 7
# conflict, 8 wait timeout), --dry-run without business changes, idempotent
# create replay, and a 412 that never overwrites. Then artifacts: a Raw file
# and a Maven POM are searched (one repository and globally), browsed,
# listed, inspected and deleted; a credential without permissions is refused
# (exit 6), sees an empty global search, and the refusal is found in the audit
# log by its request id; status, diagnostics, runtime nodes, overview and
# lifecycle jobs are read. Authorization: a single grant is written, checked
# with access check (exit 6 when denied), narrowed by resource prefix,
# replaced by a template (dry-run diff, stale If-Match 412, apply) and
# removed; the grant write is found in the audit log, also through a bounded
# --follow stream; runtime logs are read as a page and as a bounded stream.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

for binary in curl go python3; do
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
  resolver_token=${CLI_E2E_RESOLVER_TOKEN:-}
  oidc_issuer=${CLI_E2E_OIDC_ISSUER:-}
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
  resolver_token="cli-e2e-resolver-${RANDOM}-${RANDOM}"
  awk -F= '
    $1 != "GATEWAY_HTTP_PORT" &&
    $1 != "GATEWAY_POSTGRES_PORT" &&
    $1 != "RUSTFS_API_PORT" &&
    $1 != "RUSTFS_CONSOLE_PORT" &&
    $1 != "GATEWAY_ADMIN_TOKEN" &&
    $1 != "GATEWAY_RESOLVER_TOKEN" &&
    $1 !~ /^GATEWAY_OIDC_/ { print }
  ' "$env_file" >"$workdir/gateway.env"
  printf 'GATEWAY_HTTP_PORT=%s\nGATEWAY_POSTGRES_PORT=%s\nRUSTFS_API_PORT=%s\nRUSTFS_CONSOLE_PORT=%s\nGATEWAY_ADMIN_TOKEN=%s\nGATEWAY_RESOLVER_TOKEN=%s\n' \
    "$gateway_port" "$(free_port)" "$(free_port)" "$(free_port)" \
    "$admin_token" "$resolver_token" >>"$workdir/gateway.env"
  # A synthetic OIDC issuer for the identity-link checks. Nothing listens on
  # it: linking only compares the issuer, and opaque bearer tokens are
  # rejected as JWTs before any key fetch.
  oidc_issuer="http://127.0.0.1:1/cli-e2e-oidc-${RANDOM}"
  printf 'GATEWAY_OIDC_ISSUER=%s\nGATEWAY_OIDC_AUDIENCE=agctl-cli-e2e\nGATEWAY_OIDC_JWKS_URL=%s/jwks\n' \
    "$oidc_issuer" "$oidc_issuer" >>"$workdir/gateway.env"
  compose up -d --build --wait
  server="http://127.0.0.1:${gateway_port}"
fi

# Match the canonical issuer returned by configuration and identity writes.
oidc_issuer=$(python3 - "$oidc_issuer" <<'PY2'
import sys

print(sys.argv[1].strip().rstrip("/"))
PY2
)

go build -o "$workdir/agctl" ./cmd/agctl

# Every invocation runs without a profile file, with credentials only from
# the environment, and with stdin closed: agctl must never prompt.
agctl() {
  HOME="$workdir/home" AGCTL_SERVER="$server" AGCTL_TOKEN="${cli_test_token:-$admin_token}" AGCTL_PROFILE="" \
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

# Groups: a second npm repository joins the first. Create (dry-run, replay),
# inspect members and resolution order, rename with a stale If-Match that
# never overwrites, reorder members, and delete without touching members.
member="cli-e2e-npm-member-${suffix}"
expect 0 "$out" -- repo create "$member" --format npm --json
member_id=$(field "$out" data.id)
group="cli-e2e-group-${suffix}"
expect 0 "$out" -- group create "$group" --format npm --member "$repo_id" --member "$member_id" --dry-run --json
require "$(field "$out" data.mode)" client-plan "group create dry-run mode"
expect 0 "$out" -- group list --all --page-size 200 --json
python3 - "$out" "$group" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    names = [item["name"] for item in json.load(stream)["data"]["items"]]
if sys.argv[2] in names:
    raise SystemExit("group create --dry-run created the Group")
PY2
group_key="cli-e2e-group-${suffix}"
expect 0 "$out" -- group create "$group" --format npm --member "$repo_id" --member "$member_id" --idempotency-key "$group_key" --json
group_id=$(field "$out" data.id)
group_version=$(field "$out" data.version)
expect 0 "$out" -- group create "$group" --format npm --member "$repo_id" --member "$member_id" --idempotency-key "$group_key" --json
require "$(field "$out" data.id)" "$group_id" "idempotent Group replay id"
expect 1 "$out" -- group create "cli-e2e-bad-${suffix}" --format npm --member 00000000-0000-4000-8000-000000000000 --json
require "$(field "$out" error.httpStatus)" 400 "Group with an unknown member"
expect 0 "$out" -- group members list "$group_id" --json
require "$(field "$out" data.items.0.repositoryId)" "$repo_id" "first configured member"
require "$(field "$out" data.items.1.repositoryId)" "$member_id" "second configured member"
expect 0 "$out" -- group resolution "$group_id" --json
require "$(field "$out" data.members.0.repositoryId)" "$repo_id" "first resolved member"
require "$(field "$out" data.excludedMemberCount)" 0 "excluded members"

expect 0 "$out" -- group update "$group_id" --name "${group}-renamed" --dry-run --json
expect 0 "$out" -- group show "$group_id" --json
require "$(field "$out" data.name)" "$group" "name after update dry-run"
expect 0 "$out" -- group update "$group_id" --name "${group}-renamed" --anonymous-read=true --json
require "$(field "$out" data.name)" "${group}-renamed" "renamed Group"
require "$(field "$out" data.anonymousRead)" true "Group anonymousRead"
require "$(field "$out" data.members.1.repositoryId)" "$member_id" "members kept by the update"
renamed_version=$(field "$out" data.version)
expect 7 "$out" -- group update "$group_id" --name "${group}-stale" --if-match "$group_version" --json
require "$(field "$out" error.httpStatus)" 412 "stale Group If-Match"
expect 0 "$out" -- group show "$group_id" --json
require "$(field "$out" data.name)" "${group}-renamed" "name after 412"
require "$(field "$out" data.version)" "$renamed_version" "version after 412"

expect 0 "$out" -- group members set "$group_id" --member "$member_id" --member "$repo_id" --json
require "$(field "$out" data.members.0.repositoryId)" "$member_id" "reordered first member"
expect 7 "$out" -- group members set "$group_id" --member "$repo_id" --if-match "$renamed_version" --json
expect 0 "$out" -- group resolution "$group_id" --json
require "$(field "$out" data.members.0.repositoryId)" "$member_id" "resolution follows the new order"

expect 2 "$out" -- group delete "$group_id" --json
expect 0 "$out" -- group delete "$group_id" --dry-run --json
expect 0 "$out" -- group show "$group_id" --json
expect 0 "$out" -- group delete "$group_id" --yes --json
require "$(field "$out" data.deleted)" true "Group deleted"
expect 4 "$out" -- group show "$group_id" --json
expect 0 "$out" -- repo show "$member_id" --json
require "$(field "$out" data.state)" active "member kept after Group deletion"
expect 0 "$out" -- repo delete "$member_id" --yes --json

# Artifacts (#131): one Raw file and one Maven POM, published through the
# native protocols, then read back through agctl.
content_name="cli-e2e-raw-content-${suffix}"
expect 0 "$out" -- repo create "$content_name" --format raw --json
content_id=$(field "$out" data.id)
printf 'agctl e2e payload' >"$workdir/app.txt"
content_digest="sha256:$(python3 -c 'import hashlib, sys; print(hashlib.sha256(open(sys.argv[1], "rb").read()).hexdigest())' "$workdir/app.txt")"
curl --silent --show-error --fail --output /dev/null -X PUT -H "Authorization: Bearer $admin_token" \
  --data-binary "@$workdir/app.txt" "$server/raw/$content_name/releases/app.txt"

expect 0 "$out" -- repo capabilities "$content_id" --json
python3 - "$out" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    operations = json.load(stream)["data"]["operations"]
if "publish" not in operations or "read" not in operations:
    raise SystemExit(f"raw hosted capabilities miss read/publish: {operations}")
PY2
expect 0 "$out" -- repo connect "$content_id" --json
require "$(field "$out" data.baseUrl)" "$server" "connect base URL"
require "$(field "$out" data.snippets.1.label)" upload "hosted raw upload snippet"
if grep -q -- "$admin_token" "$out"; then
  printf '%s\n' 'repo connect printed the configured token' >&2
  exit 1
fi

expect 0 "$out" -- artifact search --repo "$content_id" --q releases/ --json
require "$(field "$out" data.items.0.coordinate)" releases/app.txt "repository search hit"
expect 0 "$out" -- artifact search --global --q app.txt --match contains --format raw --page-size 200 --all --json
python3 - "$out" "$content_name" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    envelope = json.load(stream)
hits = [hit for hit in envelope["data"]["items"] if hit["repositoryName"] == sys.argv[2]]
if [hit["coordinate"] for hit in hits] != ["releases/app.txt"]:
    raise SystemExit(f"global search did not find exactly the uploaded file: {hits}")
if envelope["meta"]["scope"]["searchedRepositories"] < 1:
    raise SystemExit("global search reports no searched repositories")
PY2
expect 0 "$out" -- artifact browse --repo "$content_id" --json
require "$(field "$out" data.items.0.name)" releases "browse root"
node=$(field "$out" data.items.0.id)
expect 0 "$out" -- repo browse "$content_id" --parent "$node" --json
require "$(field "$out" data.items.0.name)" app.txt "browse child"
require "$(field "$out" data.items.0.size)" 17 "browse child size"
expect 0 "$out" -- artifact identities --repo "$content_id" --purpose distribution --json
require "$(field "$out" data.items.0.digest)" "$content_digest" "identity digest"
expect 4 "$out" -- artifact intelligence show --repo "$content_id" --coordinate releases/app.txt --digest "$content_digest" --json
expect 0 "$out" -- artifact usage --repo "$content_id" --json
require "$(field "$out" meta.pagination.mode)" offset "usage window mode"

content_group="cli-e2e-raw-group-${suffix}"
expect 0 "$out" -- group create "$content_group" --format raw --member "$content_id" --json
content_group_id=$(field "$out" data.id)
expect 0 "$out" -- group browse "$content_group_id" --json
require "$(field "$out" meta.scope.groupName)" "$content_group" "group browse scope"
require "$(field "$out" data.items.0.name)" releases "group browse root"

maven_name="cli-e2e-maven-${suffix}"
expect 0 "$out" -- repo create "$maven_name" --format maven --json
maven_id=$(field "$out" data.id)
pom='<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>com.example.agctl</groupId><artifactId>e2e</artifactId><version>1.0.0</version><packaging>pom</packaging></project>'
curl --silent --show-error --fail --output /dev/null -X PUT -H "Authorization: Bearer $admin_token" \
  --data-binary "$pom" "$server/repository/maven/$maven_name/com/example/agctl/e2e/1.0.0/e2e-1.0.0.pom"
expect 0 "$out" -- maven coordinates --repo "$maven_id" --json
require "$(field "$out" data.items.0.coordinate)" com.example.agctl:e2e:1.0.0 "Maven coordinate"
expect 0 "$out" -- artifact list --repo "$maven_id" --json
require "$(field "$out" meta.pagination.mode)" none "artifact list pagination"
artifact_id=$(field "$out" data.items.0.id)
expect 0 "$out" -- artifact show "$artifact_id" --repo "$maven_id" --json
require "$(field "$out" data.artifact.state)" visible "artifact state"
require "$(field "$out" data.intelligence.status)" none "artifact intelligence"
require "$(field "$out" data.quarantine.status)" none "artifact quarantine"
expect 2 "$out" -- artifact delete "$artifact_id" --repo "$maven_id" --json
expect 0 "$out" -- artifact delete "$artifact_id" --repo "$maven_id" --dry-run --json
expect 0 "$out" -- artifact show "$artifact_id" --repo "$maven_id" --json
require "$(field "$out" data.artifact.state)" visible "state after delete dry-run"
expect 0 "$out" -- artifact delete "$artifact_id" --repo "$maven_id" --yes --json
require "$(field "$out" data.status)" accepted "artifact delete status"
expect 0 "$out" -- artifact show "$artifact_id" --repo "$maven_id" --json
require "$(field "$out" data.artifact.state)" deleted "state after delete"
expect 0 "$out" -- artifact delete "$artifact_id" --repo "$maven_id" --yes --json
expect 4 "$out" -- artifact show 00000000-0000-4000-8000-000000000000 --repo "$maven_id" --json

oci_name="cli-e2e-oci-${suffix}"
expect 0 "$out" -- repo create "$oci_name" --format oci --json
oci_id=$(field "$out" data.id)
expect 0 "$out" -- oci images --repo "$oci_id" --json
require "$(field "$out" data.items)" "[]" "empty OCI repository"

# A credential without repository permissions: refused for one repository
# (exit 6), an empty global search rather than an error, and the refusal is
# in the audit log under the request id the CLI reported.
if [[ -n "$resolver_token" ]]; then
  got=0
  HOME="$workdir/home" AGCTL_SERVER="$server" AGCTL_TOKEN="$resolver_token" AGCTL_PROFILE="" \
    "$workdir/agctl" artifact search --repo "$content_id" --q releases/ --json >"$out" 2>/dev/null </dev/null || got=$?
  require "$got" 6 "search without permission"
  denied_request=$(field "$out" error.requestId)
  HOME="$workdir/home" AGCTL_SERVER="$server" AGCTL_TOKEN="$resolver_token" AGCTL_PROFILE="" \
    "$workdir/agctl" artifact search --global --q app.txt --match contains --json >"$out" </dev/null
  require "$(field "$out" data.items)" "[]" "global search without permission"
  expect 0 "$out" -- audit list --outcome access_denied --repository "$content_name" --page-size 200 --all --json
  python3 - "$out" "$denied_request" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    records = json.load(stream)["data"]["items"]
if not any(record.get("requestId") == sys.argv[2] for record in records):
    raise SystemExit(f"no audit record for request {sys.argv[2]}")
PY2
  expect 0 "$out" -- audit list --bounded --limit 50 --outcome access_denied --repository "$content_name" --csv
  grep -q "$denied_request" "$out" || {
    printf '%s\n' 'the CSV audit window misses the refusal' >&2
    exit 1
  }
else
  printf '%s\n' 'CLI_E2E_RESOLVER_TOKEN is not set: skipping permission and audit checks' >&2
fi

# Authorization (#134). A simulated member principal is granted read on the
# Raw repository, checked, narrowed by resource prefix, replaced by a template
# and removed; every write is conditional or keyed to one grant row.
grant_started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
principal="user:cli-e2e-${suffix}"
expect 0 "$out" -- grant list --repo "$content_id" --json
grants_version=$(field "$out" data.version)
expect 2 "$out" -- grant upsert --repo "$content_id" --principal "$principal" --json
expect 0 "$out" -- grant upsert --repo "$content_id" --principal "$principal" --scope repositories:read --dry-run --json
require "$(field "$out" data.current.change)" create "grant upsert dry-run"
expect 0 "$out" -- grant list --repo "$content_id" --json
require "$(field "$out" data.version)" "$grants_version" "grant version after dry-run"
expect 0 "$out" -- grant upsert --repo "$content_id" --principal "$principal" --scope repositories:read --json
require "$(field "$out" data.change)" upserted "grant upsert"
expect 0 "$out" -- grant upsert --repo "$content_id" --principal "$principal" --scope repositories:write --resource-prefix releases/ --json
expect 0 "$out" -- access check --repo "$content_id" --actor "$principal" --role member --require read --json
require "$(field "$out" data.permissions.read.allowed)" true "granted read"
expect 6 "$out" -- access check --repo "$content_id" --actor "$principal" --role member --require write --json
require "$(field "$out" error.code)" not_permitted "write without the prefix"
expect 0 "$out" -- access check --repo "$content_id" --actor "$principal" --role member --resource releases/app.txt --require write --json
expect 6 "$out" -- access check --repo "$content_id" --actor "$principal" --role member --resource other/app.txt --require write --json
expect 0 "$out" -- grant list --all-repositories --json
python3 - "$out" "$content_id" "$principal" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    records = json.load(stream)["data"]["items"]
mine = sorted((record.get("resourcePrefix") or "", ",".join(record["scopes"])) for record in records
              if record["repositoryId"] == sys.argv[2] and record["principal"] == sys.argv[3])
if mine != [("", "repositories:read"), ("releases/", "repositories:write")]:
    raise SystemExit(f"aggregated grants do not show both rows: {mine}")
PY2
expect 0 "$out" -- audit list --operation repository.grants.upsert --repository "$content_name" --from "$grant_started" --json
python3 - "$out" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    records = json.load(stream)["data"]["items"]
if len(records) < 2:
    raise SystemExit(f"expected the two grant writes in the audit log, got {len(records)}")
PY2
expect 0 "$out" -- audit list --follow --from "$grant_started" --operation repository.grants.upsert --repository "$content_name" --max-events 2 --json
python3 - "$out" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    events = [json.loads(line) for line in stream]
kinds = [event["type"] for event in events]
if kinds != ["entry", "entry", "end"] or events[-1]["data"]["reason"] != "max_events":
    raise SystemExit(f"bounded audit stream: {kinds} {events[-1]}")
PY2

template_response="$workdir/template.json"
curl --silent --show-error --fail --output "$template_response" -X POST -H "Authorization: Bearer $admin_token" \
  -H 'Content-Type: application/json' \
  --data "{\"name\":\"cli-e2e-template-${suffix}\",\"grants\":[{\"principal\":\"$principal\",\"scopes\":[\"repositories:read\",\"repositories:intelligence\"]}]}" \
  "$server/api/v2/authorization-templates"
template_id=$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["id"])' "$template_response")
expect 0 "$out" -- template show "$template_id" --json
require "$(field "$out" data.grants.0.principal)" "$principal" "template grant"
expect 0 "$out" -- grant list --repo "$content_id" --json
grants_version=$(field "$out" data.version)
expect 0 "$out" -- template apply "$template_id" --repo "$content_id" --dry-run --json
python3 - "$out" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    diff = json.load(stream)["data"]["current"]["diff"]
summary = (len(diff["added"]), len(diff["removed"]), len(diff["changed"]), len(diff["kept"]))
if summary != (0, 1, 1, 0):
    raise SystemExit(f"template diff (added, removed, changed, kept) = {summary}")
PY2
expect 2 "$out" -- template apply "$template_id" --repo "$content_id" --json
expect 7 "$out" -- template apply "$template_id" --repo "$content_id" --yes --if-match 0 --json
expect 0 "$out" -- grant list --repo "$content_id" --json
require "$(field "$out" data.version)" "$grants_version" "grant version after a refused apply"
expect 0 "$out" -- template apply "$template_id" --repo "$content_id" --yes --json
expect 0 "$out" -- grant list --repo "$content_id" --json
require "$(field "$out" data.grants)" "[{\"principal\": \"$principal\", \"scopes\": [\"repositories:read\", \"repositories:intelligence\"]}]" "grants after apply"
expect 0 "$out" -- grant delete --repo "$content_id" --principal "$principal" --dry-run --json
expect 0 "$out" -- grant delete --repo "$content_id" --principal "$principal" --yes --json
expect 4 "$out" -- grant delete --repo "$content_id" --principal "$principal" --yes --json
expect 6 "$out" -- access check --repo "$content_id" --actor "$principal" --role member --require read --json

# Concurrent writes to different principals preserve both rows. Each process
# has separate result files; neither reads and replaces the whole grant set.
concurrent_a="user:cli-e2e-a-${suffix}"
concurrent_b="user:cli-e2e-b-${suffix}"
agctl grant upsert --repo "$content_id" --principal "$concurrent_a" --scope repositories:read --json >"$workdir/grant-a.json" 2>"$workdir/grant-a.err" &
grant_a_pid=$!
agctl grant upsert --repo "$content_id" --principal "$concurrent_b" --scope repositories:write --json >"$workdir/grant-b.json" 2>"$workdir/grant-b.err" &
grant_b_pid=$!
wait "$grant_a_pid"
wait "$grant_b_pid"
expect 0 "$out" -- grant list --repo "$content_id" --json
python3 - "$out" "$concurrent_a" "$concurrent_b" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    grants = {row["principal"]: row["scopes"] for row in json.load(stream)["data"]["grants"]}
if grants.get(sys.argv[2]) != ["repositories:read"] or grants.get(sys.argv[3]) != ["repositories:write"]:
    raise SystemExit(f"concurrent single-row writes overwrote one another: {grants}")
PY2
expect 0 "$out" -- grant delete --repo "$content_id" --principal "$concurrent_a" --yes --json
expect 0 "$out" -- grant delete --repo "$content_id" --principal "$concurrent_b" --yes --json

# A real non-admin credential checks itself, cannot simulate an administrator
# or change grants, and loses read access after its grant is revoked.
if [[ -n "$project" && -n "$resolver_token" ]]; then
  cli_test_token=$resolver_token expect 0 "$out" -- auth whoami --json
  member_actor=$(field "$out" data.actor)
  expect 0 "$out" -- grant upsert --repo "$content_id" --principal "$member_actor" --scope repositories:read --json
  cli_test_token=$resolver_token expect 0 "$out" -- access check --repo "$content_id" --require read --json
  require "$(field "$out" data.simulated)" false "real caller access check"
  cli_test_token=$resolver_token expect 6 "$out" -- access check --repo "$content_id" --require write --json
  expect 0 "$out" -- grant list --repo "$content_id" --json
  grants_version=$(field "$out" data.version)
  cli_test_token=$resolver_token expect 6 "$out" -- access check --repo "$content_id" --actor "$principal" --role admin --require admin --json
  cli_test_token=$resolver_token expect 6 "$out" -- grant upsert --repo "$content_id" --principal "$member_actor" --scope repositories:admin --json
  expect 0 "$out" -- grant list --repo "$content_id" --json
  require "$(field "$out" data.version)" "$grants_version" "refused member write leaves grants unchanged"
  expect 0 "$out" -- audit list --operation repository.grants.upsert --actor "$member_actor" --repository "$content_name" --from "$grant_started" --json
  require "$(field "$out" data.items)" "[]" "refused member write has no mutation audit"
  expect 0 "$out" -- grant delete --repo "$content_id" --principal "$member_actor" --yes --json
  # With no remaining authority the API hides the repository's existence.
  cli_test_token=$resolver_token expect 4 "$out" -- access check --repo "$content_id" --require read --json
fi
curl --silent --show-error --fail --output /dev/null -X DELETE -H "Authorization: Bearer $admin_token" "$server/api/v2/authorization-templates/$template_id"

# The anonymous switch is shared with every other check on this Gateway: it is
# read, re-set to its own value, and refused on a stale version, never flipped.
expect 0 "$out" -- access anonymous show --json
anonymous_enabled=$(field "$out" data.enabled)
anonymous_version=$(field "$out" data.version)
expect 0 "$out" -- access anonymous set --enabled="$anonymous_enabled" --yes --dry-run --json
expect 0 "$out" -- access anonymous show --json
require "$(field "$out" data.version)" "$anonymous_version" "anonymous version after dry-run"
expect 0 "$out" -- access anonymous set --enabled="$anonymous_enabled" --yes --json
expect 7 "$out" -- access anonymous set --enabled="$anonymous_enabled" --yes --if-match "$anonymous_version" --json
expect 0 "$out" -- access anonymous show --json
require "$(field "$out" data.enabled)" "$anonymous_enabled" "anonymous switch unchanged"

# Runtime logs (#135): one page, then a bounded stream that ends on its budget.
expect 0 "$out" -- runtime logs --limit 5 --json
require "$(field "$out" meta.scope.scope)" local "runtime log scope"
expect 2 "$out" -- runtime logs --follow --json
expect 0 "$out" -- runtime logs --follow --duration 3s --interval 1s --json
python3 - "$out" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    events = [json.loads(line) for line in stream]
if any(event["schemaVersion"] != 1 for event in events) or events[-1]["type"] != "end" or events[-1]["data"]["reason"] != "duration":
    raise SystemExit(f"bounded log stream did not end on its duration: {events[-1:]}")
if events[0]["type"] != "scope" or events[0]["data"]["scope"] != "local" or not events[0]["data"]["instanceId"] or not events[0]["data"]["sessionId"]:
    raise SystemExit("log stream must identify its local process, even without entries")
PY2
after_cursor=$(python3 - "$out" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    events = [json.loads(line) for line in stream]
print(events[-1]["data"]["resume"]["afterCursor"])
PY2
)
expect 0 "$out" -- runtime logs --after-cursor "$after_cursor" --follow --duration 1s --json

# Machine identity (#133), the #127 CI playbook: a service account gets a
# credential whose secret only reaches a private file, is granted read on the
# Raw repository, reads with that credential, is rotated (the old credential
# answers 401, exit 5), and loses access with its grant. No secret may reach
# any output.
nosecret() {
  local secret=$1
  if grep -q -- "$secret" "$out" "$workdir/stderr"; then
    printf '%s\n' 'a secret reached agctl output' >&2
    exit 1
  fi
}
account="cli-e2e-bot-${suffix}"
expect 0 "$out" -- service-account create "$account" --description "agctl E2E" --json
account_id=$(field "$out" data.id)
expect 0 "$out" -- service-account show "$account_id" --json
require "$(field "$out" data.state)" active "service account state"
secrets="$workdir/secrets"
mkdir -m 700 "$secrets"
expect 2 "$out" -- credential issue --service-account "$account_id" --name e2e --json
expect 0 "$out" -- credential issue --service-account "$account_id" --name e2e-old --secret-output "$secrets/old" --dry-run --json
[[ ! -e "$secrets/old" ]] || {
  printf '%s\n' 'credential issue --dry-run created the secret file' >&2
  exit 1
}
expect 0 "$out" -- credential issue --service-account "$account_id" --name e2e-old --secret-output "$secrets/old" --verbose --json
old_credential=$(field "$out" data.id)
old_token=$(cat "$secrets/old")
nosecret "$old_token"
[[ "$(python3 -c 'import os, stat, sys; print(oct(stat.S_IMODE(os.stat(sys.argv[1]).st_mode)))' "$secrets/old")" == 0o600 ]] || {
  printf '%s\n' 'the secret file is not private' >&2
  exit 1
}
expect 2 "$out" -- credential issue --service-account "$account_id" --name e2e-old --secret-output "$secrets/old" --json
expect 0 "$out" -- grant upsert --repo "$content_id" --principal "service-account:$account_id" --scope repositories:read --json
cli_test_token=$old_token expect 0 "$out" -- artifact search --repo "$content_id" --q releases/ --json
require "$(field "$out" data.items.0.coordinate)" releases/app.txt "search with the service account credential"
cli_test_token=$old_token expect 6 "$out" -- grant list --repo "$content_id" --json
expect 0 "$out" -- credential issue --service-account "$account_id" --name e2e-new --secret-output "$secrets/new" --json
new_token=$(cat "$secrets/new")
nosecret "$new_token"
expect 2 "$out" -- credential revoke "$old_credential" --service-account "$account_id" --json
expect 0 "$out" -- credential revoke "$old_credential" --service-account "$account_id" --yes --json
expect 0 "$out" -- credential revoke "$old_credential" --service-account "$account_id" --yes --json
cli_test_token=$old_token expect 5 "$out" -- artifact search --repo "$content_id" --q releases/ --json
cli_test_token=$new_token expect 0 "$out" -- artifact search --repo "$content_id" --q releases/ --json
expect 0 "$out" -- credential list --service-account "$account_id" --json
python3 - "$out" "$old_credential" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    items = json.load(stream)["data"]["items"]
states = {item["name"]: bool(item.get("revokedAt")) for item in items}
if states != {"e2e-old": True, "e2e-new": False} or "token" in json.dumps(items):
    raise SystemExit(f"credential list after rotation: {states}")
PY2
# Group reads follow Group writes (#359): administering every member lets the
# account read, list, and replace the members of a Group; a Group it only
# partly administers answers 404 and stays out of its list.
expect 0 "$out" -- grant upsert --repo "$content_id" --principal "service-account:$account_id" --scope repositories:admin --json
expect 0 "$out" -- repo create "cli-e2e-raw-other-${suffix}" --format raw --json
other_raw_id=$(field "$out" data.id)
expect 0 "$out" -- group create "cli-e2e-owned-${suffix}" --format raw --member "$content_id" --json
owned_group=$(field "$out" data.id)
owned_version=$(field "$out" data.version)
expect 0 "$out" -- group create "cli-e2e-mixed-${suffix}" --format raw --member "$content_id" --member "$other_raw_id" --json
mixed_group=$(field "$out" data.id)
cli_test_token=$new_token expect 0 "$out" -- group show "$owned_group" --json
cli_test_token=$new_token expect 0 "$out" -- group resolution "$owned_group" --json
cli_test_token=$new_token expect 4 "$out" -- group show "$mixed_group" --json
cli_test_token=$new_token expect 0 "$out" -- group list --all --json
python3 - "$out" "$owned_group" "$mixed_group" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    ids = {item["id"] for item in json.load(stream)["data"]["items"]}
if sys.argv[2] not in ids or sys.argv[3] in ids:
    raise SystemExit(f"member administrator lists {sorted(ids)}")
PY2
cli_test_token=$new_token expect 0 "$out" -- group members set "$owned_group" --member "$content_id" --json
[[ "$(field "$out" data.version)" != "$owned_version" ]] || {
  printf '%s\n' 'members set did not report the new Group version' >&2
  exit 1
}
expect 0 "$out" -- group delete "$owned_group" --yes --json
expect 0 "$out" -- group delete "$mixed_group" --yes --json
expect 0 "$out" -- repo delete "$other_raw_id" --yes --json
expect 0 "$out" -- grant delete --repo "$content_id" --principal "service-account:$account_id" --yes --json
cli_test_token=$new_token expect 6 "$out" -- artifact search --repo "$content_id" --q releases/ --json
expect 0 "$out" -- service-account update "$account_id" --state disabled --json
require "$(field "$out" data.state)" disabled "disabled service account"
cli_test_token=$new_token expect 5 "$out" -- auth whoami --json
expect 7 "$out" -- credential issue --service-account "$account_id" --name refused --secret-output "$secrets/refused" --json
[[ ! -e "$secrets/refused" ]] || {
  printf '%s\n' 'a refused issue created the secret file' >&2
  exit 1
}
expect 0 "$out" -- audit list --operation service_account.credential.create --from "$grant_started" --json
python3 - "$out" "$account_id" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    records = [record for record in json.load(stream)["data"]["items"] if sys.argv[2] in record.get("resource", "")]
if len(records) != 2:
    raise SystemExit(f"expected two credential issues in the audit log, got {len(records)}")
PY2

# Platform API keys (#133): the secret again reaches only its file.
expect 0 "$out" -- api-key create "cli-e2e-key-${suffix}" --role member --secret-output "$secrets/key" --json
key_id=$(field "$out" data.id)
key_token=$(cat "$secrets/key")
nosecret "$key_token"
cli_test_token=$key_token expect 0 "$out" -- auth whoami --json
expect 0 "$out" -- api-key list --json
python3 - "$out" "$key_id" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    items = json.load(stream)["data"]["items"]
if [item["id"] for item in items if item["id"] == sys.argv[2]] != [sys.argv[2]] or "token" in json.dumps(items):
    raise SystemExit("api-key list does not show the key, or shows a secret")
PY2
expect 0 "$out" -- api-key revoke "$key_id" --yes --json
cli_test_token=$key_token expect 5 "$out" -- auth whoami --json

# Users (#335): a pending user becomes a member, which grants no repository
# access by itself; an explicit grant does. Ending its session rejects the
# session token; the password is reset from a private file; the user is
# deleted. The password never reaches any output.
user="cli-e2e-user-${suffix}"
user_password="Cli-e2e-${suffix}-pw"
printf '%s\n' "$user_password" >"$secrets/password"
chmod 600 "$secrets/password"
expect 2 "$out" -- user create "$user" --role none --json
chmod 644 "$secrets/password"
expect 2 "$out" -- user create "$user" --role none --password-file "$secrets/password" --json
nosecret "$user_password"
chmod 600 "$secrets/password"
expect 0 "$out" -- user create "$user" --role none --password-file "$secrets/password" --dry-run --json
nosecret "$user_password"
expect 0 "$out" -- user list --search "$user" --json
require "$(field "$out" meta.pagination.total)" 0 "password preflight and dry-run create no user"
expect 0 "$out" -- auth whoami --json
admin_actor=$(field "$out" data.actor)
user_started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
expect 0 "$out" -- user create "$user" --role none --password-file "$secrets/password" --verbose --json
nosecret "$user_password"
user_id=$(field "$out" data.id)
require "$(field "$out" data.role)" none "pending user role"
login="$workdir/login.json"
curl --silent --show-error --fail --output "$login" -H 'Content-Type: application/json' \
  --data "{\"username\":\"$user\",\"password\":\"$user_password\"}" "$server/auth/login"
session_token=$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["token"])' "$login")
expect 0 "$out" -- user update "$user_id" --role member --display-name "CLI E2E" --json
require "$(field "$out" data.role)" member "member role"
expect 6 "$out" -- access check --repo "$content_id" --actor "user:$user" --role member --require read --json
expect 0 "$out" -- grant upsert --repo "$content_id" --principal "user:$user" --scope repositories:read --json
expect 0 "$out" -- access check --repo "$content_id" --actor "user:$user" --role member --require read --json
expect 0 "$out" -- user sessions list "$user_id" --json
session_id=$(field "$out" data.items.0.id)
cli_test_token=$session_token expect 0 "$out" -- auth whoami --json
# A member session cannot manage accounts or identities (403, exit 6).
cli_test_token=$session_token expect 6 "$out" -- user list --json
cli_test_token=$session_token expect 6 "$out" -- user identities list "$user_id" --json
require "$(field "$out" error.httpStatus)" 403 "member identity list status"
nosecret "$session_token"
cli_test_token=$session_token expect 6 "$out" -- user identities unlink "$user_id" --identity 00000000-0000-4000-8000-000000000000 --yes --json
nosecret "$session_token"
if [[ -n "$oidc_issuer" ]]; then
  cli_test_token=$session_token expect 6 "$out" -- user identities link "$user_id" --issuer "$oidc_issuer" --subject "cli-e2e-self-${suffix}" --json
  nosecret "$session_token"
fi
expect 0 "$out" -- user sessions revoke "$user_id" --session "$session_id" --yes --json
expect 0 "$out" -- user sessions revoke "$user_id" --session "$session_id" --yes --json
cli_test_token=$session_token expect 5 "$out" -- auth whoami --json
expect 0 "$out" -- user sessions revoke "$user_id" --all --yes --json
expect 7 "$out" -- user update "$user_id" --description stale --if-match 1 --json
expect 0 "$out" -- user reset-password "$user_id" --password-file "$secrets/password" --must-change-password --json
nosecret "$user_password"
require "$(field "$out" data.mustChangePassword)" true "reset requires a change"
expect 0 "$out" -- user identities list "$user_id" --json
require "$(field "$out" data.items)" "[]" "no linked identities"
expect 4 "$out" -- user identities unlink "$user_id" --identity 00000000-0000-4000-8000-000000000000 --yes --json
require "$(field "$out" error.httpStatus)" 404 "missing identity status"
# Identity links (#335) against the configured OIDC issuer: dry-run submits
# nothing; a link is listed; the same subject on another user, or again on
# this one, is a 409 (exit 7); an issuer other than the configured one is
# refused; unlink removes it, and a second unlink answers 404 (exit 4). Both
# changes reach the audit log, and no token reaches any output.
if [[ -n "$oidc_issuer" ]]; then
  subject="cli-e2e-subject-${suffix}"
  identity_started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  expect 2 "$out" -- user identities link "$user_id" --issuer "$oidc_issuer" --json
  expect 0 "$out" -- user identities link "$user_id" --issuer "$oidc_issuer" --subject "$subject" --dry-run --json
  require "$(field "$out" data.mode)" client-plan "link dry-run mode"
  expect 0 "$out" -- user identities list "$user_id" --json
  require "$(field "$out" data.items)" "[]" "link dry-run links nothing"
  expect 0 "$out" -- user identities link "$user_id" --issuer "$oidc_issuer" --subject "$subject" --verbose --json
  nosecret "$admin_token"
  identity_id=$(field "$out" data.id)
  require "$(field "$out" data.userId)" "$user_id" "linked identity user"
  require "$(field "$out" data.subject)" "$subject" "linked identity subject"
  require "$(field "$out" data.kind)" oidc "linked identity kind"
  expect 0 "$out" -- user identities list "$user_id" --json
  require "$(field "$out" data.items.0.id)" "$identity_id" "listed identity"
  require "$(field "$out" data.items.0.issuer)" "$oidc_issuer" "listed identity issuer"
  require "$(python3 -c 'import json, sys; print(len(json.load(open(sys.argv[1]))["data"]["items"]))' "$out")" 1 "a single linked identity"
  expect 0 "$out" -- user identities list "$user_id"
  grep -q -- "$subject" "$out" || {
    printf '%s\n' 'the identity table misses the subject' >&2
    exit 1
  }
  other_user="cli-e2e-other-${suffix}"
  expect 0 "$out" -- user create "$other_user" --role none --password-file "$secrets/password" --json
  other_user_id=$(field "$out" data.id)
  expect 7 "$out" -- user identities link "$other_user_id" --issuer "$oidc_issuer" --subject "$subject" --json
  require "$(field "$out" error.httpStatus)" 409 "identity conflict status"
  nosecret "$admin_token"
  expect 7 "$out" -- user identities link "$user_id" --issuer "$oidc_issuer/" --subject "$subject" --json
  expect 0 "$out" -- user identities list "$other_user_id" --json
  require "$(field "$out" data.items)" "[]" "a conflicting link links nothing"
  expect 1 "$out" -- user identities link "$other_user_id" --issuer "https://issuer.invalid/cli-e2e" --subject "$subject" --json
  require "$(field "$out" error.httpStatus)" 400 "foreign issuer status"
  expect 2 "$out" -- user identities unlink "$user_id" --identity "$identity_id" --json
  expect 0 "$out" -- user identities unlink "$user_id" --identity "$identity_id" --dry-run --json
  expect 0 "$out" -- user identities list "$user_id" --json
  require "$(field "$out" data.items.0.id)" "$identity_id" "unlink dry-run keeps the identity"
  expect 4 "$out" -- user identities unlink "$other_user_id" --identity "$identity_id" --yes --json
  expect 0 "$out" -- user identities unlink "$user_id" --identity "$identity_id" --yes --json
  require "$(field "$out" data.unlinked)" true "unlinked"
  nosecret "$admin_token"
  expect 0 "$out" -- user identities list "$user_id" --json
  require "$(field "$out" data.items)" "[]" "no identity after unlink"
  expect 4 "$out" -- user identities unlink "$user_id" --identity "$identity_id" --yes --json
  require "$(field "$out" error.httpStatus)" 404 "second unlink status"
  # Once unlinked, the subject is free for another user.
  expect 0 "$out" -- user identities link "$other_user_id" --issuer "$oidc_issuer" --subject "$subject" --json
  expect 0 "$out" -- audit list --from "$identity_started" --page-size 200 --all --json
  python3 - "$out" "$user_id" "$identity_id" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    records = json.load(stream)["data"]["items"]
resource = f"users/{sys.argv[2]}/identities/{sys.argv[3]}"
operations = sorted(record["operation"] for record in records if record.get("resource") == resource)
if operations != ["user.identity.link", "user.identity.unlink"]:
    raise SystemExit(f"identity audit for {resource}: {operations}")
PY2
  expect 0 "$out" -- user delete "$other_user_id" --yes --json
else
  printf '%s\n' 'CLI E2E: CLI_E2E_OIDC_ISSUER is not set; identity link checks skipped.' >&2
fi
expect 0 "$out" -- user list --search "$user" --json
require "$(field "$out" meta.pagination.total)" 1 "user search"
expect 0 "$out" -- grant delete --repo "$content_id" --principal "user:$user" --yes --json
expect 2 "$out" -- user delete "$user_id" --json
expect 0 "$out" -- user delete "$user_id" --dry-run --json
expect 0 "$out" -- user delete "$user_id" --yes --json
expect 4 "$out" -- user show "$user_id" --json
# The user's lifecycle is in the audit log, by the administrator: create,
# the role update, the session list, both single-session revokes (the second
# is idempotent), revoke-all, the password reset and the delete. Refused,
# dry-run and stale (412) attempts change nothing and record nothing here.
expect 0 "$out" -- audit list --from "$user_started" --page-size 200 --all --json
python3 - "$out" "$user_id" "$admin_actor" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    records = json.load(stream)["data"]["items"]
resource = f"users/{sys.argv[2]}"
mine = [record for record in records if record.get("resource") == resource]
got = sorted((record["operation"], record["status"]) for record in mine)
want = sorted([
    ("user.create", 201),
    ("user.update", 200),
    ("user.session.list", 200),
    ("user.session.revoke", 200),
    ("user.session.revoke", 200),
    ("user.sessions.revoke", 200),
    ("user.password.reset", 200),
    ("user.delete", 204),
])
if got != want:
    raise SystemExit(f"user lifecycle audit for {resource}: {got}")
actors = {record.get("actor") for record in mine}
if actors != {sys.argv[3]}:
    raise SystemExit(f"user lifecycle audit actors for {resource}: {sorted(map(str, actors))}")
PY2

# Publishing (#132). A Raw tree is uploaded, re-run (everything skipped by
# digest), changed (only the changed file goes up), and read back byte for
# byte; a Maven coordinate is published through a session, and a committed
# session is never reused.
publish_repo="cli-e2e-publish-${suffix}"
expect 0 "$out" -- repo create "$publish_repo" --format raw --json
publish_id=$(field "$out" data.id)
tree="$workdir/tree"
mkdir -p "$tree/docs"
printf 'one' >"$tree/one.txt"
printf 'two' >"$tree/docs/two words.txt"
printf 'three' >"$tree/docs/three.txt"
publish_counts() {
  python3 - "$out" <<'PY2'
import json
import sys
from collections import Counter

with open(sys.argv[1], encoding="utf-8") as stream:
    items = json.load(stream)["data"]["items"]
print(",".join(f"{status}={count}" for status, count in sorted(Counter(item["status"] for item in items).items())))
PY2
}
expect 0 "$out" -- publish raw "$tree" --repo "$publish_id" --to releases/1.0 --dry-run --json
require "$(publish_counts)" "planned=3" "publish dry-run"
expect 0 "$out" -- publish raw "$tree" --repo "$publish_id" --to releases/1.0 --parallel 2 --json
require "$(publish_counts)" "uploaded=3" "first publish"
expect 0 "$out" -- publish raw "$tree" --repo "$publish_id" --to releases/1.0 --json
require "$(publish_counts)" "skipped=3" "re-run publish"
printf 'three, changed' >"$tree/docs/three.txt"
expect 0 "$out" -- publish raw "$tree" --repo "$publish_id" --to releases/1.0 --json
require "$(publish_counts)" "skipped=2,uploaded=1" "changed publish"
[[ "$(curl --silent --show-error --fail -H "Authorization: Bearer $admin_token" "$server/raw/$publish_repo/releases/1.0/docs/two%20words.txt")" == two ]] || {
  printf '%s\n' 'the published file does not read back' >&2
  exit 1
}
expect 0 "$out" -- artifact search --repo "$publish_id" --q releases/1.0/docs/ --json
python3 - "$out" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    coordinates = sorted(item["coordinate"] for item in json.load(stream)["data"]["items"])
# Raw coordinates are canonical: each segment escaped like Go's url.PathEscape.
if coordinates != ["releases/1.0/docs/three.txt", "releases/1.0/docs/two%20words.txt"]:
    raise SystemExit(f"published files in search: {coordinates}")
PY2
expect 2 "$out" -- publish raw "$tree/one.txt" --repo "$publish_id" --to ../escape --json
expect 2 "$out" -- publish raw "$tree" --repo "$maven_id" --json

maven_publish="cli-e2e-maven-publish-${suffix}"
expect 0 "$out" -- repo create "$maven_publish" --format maven --json
maven_publish_id=$(field "$out" data.id)
mkdir -p "$workdir/maven"
for version in 2.0.0 2.0.1; do
  printf '<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>com.example.agctl</groupId><artifactId>publish</artifactId><version>%s</version></project>' "$version" >"$workdir/maven/publish-$version.pom"
  printf 'jar %s' "$version" >"$workdir/maven/publish-$version.jar"
done
maven_key="cli-e2e-maven-${suffix}"
expect 0 "$out" -- publish maven --repo "$maven_publish_id" --coordinate com.example.agctl:publish:2.0.0 --pom "$workdir/maven/publish-2.0.0.pom" --file "$workdir/maven/publish-2.0.0.jar" --idempotency-key "$maven_key" --json
require "$(field "$out" data.state)" committed "maven publish"
require "$(field "$out" data.artifact.coordinate)" com.example.agctl:publish:2.0.0 "published coordinate"
expect 7 "$out" -- publish maven --repo "$maven_publish_id" --coordinate com.example.agctl:publish:2.0.0 --pom "$workdir/maven/publish-2.0.0.pom" --file "$workdir/maven/publish-2.0.0.jar" --idempotency-key "$maven_key" --json
expect 0 "$out" -- publish create --repo "$maven_publish_id" --coordinate com.example.agctl:publish:2.0.1 --pom "$workdir/maven/publish-2.0.1.pom" --file "$workdir/maven/publish-2.0.1.jar" --json
session_id=$(field "$out" data.id)
require "$(field "$out" data.state)" open "new session"
curl --silent --show-error --fail --output "$workdir/session.json" -H "Authorization: Bearer $admin_token" "$server/api/v2/publish-sessions/$session_id"
expect 0 "$out" -- publish upload "$session_id" --file "$workdir/maven/publish-2.0.1.pom" --file "$workdir/maven/publish-2.0.1.jar" --json
require "$(publish_counts)" "uploaded=2" "session upload"
expect 0 "$out" -- publish upload "$session_id" --file "$workdir/maven/publish-2.0.1.jar" --json
expect 0 "$out" -- publish commit "$session_id" --json
require "$(field "$out" data.artifact.coordinate)" com.example.agctl:publish:2.0.1 "committed coordinate"
expect 0 "$out" -- publish status "$session_id" --json
require "$(field "$out" data.state)" committed "session state"
expect 7 "$out" -- publish commit "$session_id" --json
expect 0 "$out" -- maven coordinates --repo "$maven_publish_id" --json
python3 - "$out" <<'PY2'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    coordinates = sorted(item["coordinate"] for item in json.load(stream)["data"]["items"])
if coordinates != ["com.example.agctl:publish:2.0.0", "com.example.agctl:publish:2.0.1"]:
    raise SystemExit(f"published coordinates: {coordinates}")
PY2

# Session token to a private file (#129): the token reaches only the file.
login_user="cli-e2e-login-${suffix}"
expect 0 "$out" -- user create "$login_user" --role member --password-file "$secrets/password" --json
login_user_id=$(field "$out" data.id)
got=0
HOME="$workdir/home" AGCTL_SERVER="$server" AGCTL_TOKEN="" AGCTL_PROFILE="" \
  "$workdir/agctl" auth login --username "$login_user" --password-stdin --secret-output "$secrets/session" --verbose --json \
  <"$secrets/password" >"$out" 2>"$workdir/stderr" || got=$?
require "$got" 0 "login to a secret file"
nosecret "$(cat "$secrets/session")"
nosecret "$user_password"
require "$(field "$out" data.secretOutput.delivered)" true "login token delivered"
cli_test_token=$(cat "$secrets/session") expect 0 "$out" -- auth whoami --json
require "$(field "$out" data.actor)" "user:$login_user" "login identity"
expect 0 "$out" -- user delete "$login_user_id" --yes --json

# Runtime reads (#135). status is healthy or degraded on a running stack and
# exits 0 for both; --strict fails exactly when it is not healthy.
expect 0 "$out" -- status --json
overall=$(field "$out" data.overall)
[[ "$overall" == healthy || "$overall" == degraded ]] || {
  printf 'status reports %s on a running Gateway.\n' "$overall" >&2
  exit 1
}
got=0
agctl status --strict --json >"$out" 2>/dev/null || got=$?
if [[ "$overall" == healthy ]]; then require "$got" 0 "strict healthy status"; else require "$got" 1 "strict degraded status"; fi
expect 0 "$out" -- diagnostics --json
expect 0 "$out" -- runtime nodes --json
expect 0 "$out" -- overview show --json
expect 0 "$out" -- lifecycle-job list --limit 20 --json
expect 0 "$out" -- lifecycle-job list --repo "$maven_id" --json
expect 4 "$out" -- lifecycle-job show no-such-job --json

expect 0 "$out" -- group delete "$content_group_id" --yes --json
for id in "$content_id" "$maven_id" "$oci_id"; do
  expect 0 "$out" -- repo delete "$id" --yes --json
done

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
