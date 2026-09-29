#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
base_ref=${GATEWAY_CARGO_UPGRADE_FROM_REF:-395c13fe291b6d14083e5918ce2f8c766618274d}
base_revision=$(git rev-parse "$base_ref^{commit}")
current_revision=$(git rev-parse HEAD)
environment_file=${GATEWAY_ENV_FILE:-.env}
test -f "$environment_file" || { printf '%s\n' 'Cargo upgrade readiness requires a configured environment file.' >&2; exit 1; }
[[ $(cargo --version) == cargo\ 1.96.0\ * ]] || { printf '%s\n' 'Cargo 1.96.0 is required.' >&2; exit 1; }

free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

project="artifact-gateway-cargo-upgrade-${RANDOM}-${RANDOM}"
workspace=$(mktemp -d)
old_tree="$workspace/old"
mkdir -p "$old_tree"
git archive "$base_revision" | tar -x -C "$old_tree"
isolated_environment="$workspace/environment"
awk -F= '$1 != "GATEWAY_HTTP_PORT" && $1 != "GATEWAY_POSTGRES_PORT" && $1 != "RUSTFS_API_PORT" && $1 != "RUSTFS_CONSOLE_PORT" && $1 != "COMPOSE_PROFILES" { print }' "$environment_file" >"$isolated_environment"
gateway_port=$(free_port)
rustfs_port=$(free_port)
printf 'GATEWAY_HTTP_PORT=%s\nGATEWAY_POSTGRES_PORT=%s\nRUSTFS_API_PORT=%s\nRUSTFS_CONSOLE_PORT=%s\n' \
  "$gateway_port" "$(free_port)" "$rustfs_port" "$(free_port)" >>"$isolated_environment"
gateway_url="http://127.0.0.1:$gateway_port"
admin_token=$(awk -F= '$1 == "GATEWAY_ADMIN_TOKEN" { print substr($0, index($0, "=") + 1) }' "$isolated_environment")
test -n "$admin_token"
admin=(-H "Authorization: Bearer $admin_token")
old_compose=(docker compose --project-name "$project" --env-file "$isolated_environment" -f "$old_tree/compose.yml")
current_compose=(docker compose --project-name "$project" --env-file "$isolated_environment" -f "$root/compose.yml")
gateway_image="${project}-gateway:latest"
cleanup() {
  "${current_compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  "${old_compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  docker image rm "$gateway_image" >/dev/null 2>&1 || true
  chmod -R u+w "$workspace" >/dev/null 2>&1 || true
  rm -rf "$workspace"
}
trap cleanup EXIT

"${old_compose[@]}" build --build-arg "VERSION=$(tr -d '[:space:]' <"$old_tree/VERSION")" \
  --build-arg "REVISION=$base_revision" gateway
"${old_compose[@]}" up -d --no-build --wait

source_name="cargo-upgrade-source-${RANDOM}"
other_name="cargo-upgrade-other-${RANDOM}"
group_name="cargo-upgrade-group-${RANDOM}"
source_id=$(python3 -c 'import uuid; print(uuid.uuid4())')
other_id=$(python3 -c 'import uuid; print(uuid.uuid4())')
group_id=$(python3 -c 'import uuid; print(uuid.uuid4())')
psql() {
  "${old_compose[@]}" exec -T postgres psql -X -A -t -v ON_ERROR_STOP=1 -U gateway -d gateway -c "$1"
}
psql "BEGIN;
INSERT INTO hosted_repositories(id,name,format) VALUES
('$source_id','$source_name','cargo'),('$other_id','$other_name','cargo');
INSERT INTO hosted_groups(id,name,format) VALUES ('$group_id','$group_name','cargo');
INSERT INTO hosted_group_members(group_id,repository_id,position) VALUES
('$group_id','$source_id',0),('$group_id','$other_id',1);
COMMIT;" >/dev/null

crate_name=cargo-upgrade-fixture
crate_version=1.0.0
index_path=ca/rg/cargo-upgrade-fixture
mkdir -p "$workspace/package/src" "$workspace/publish-home"
cat >"$workspace/package/Cargo.toml" <<'TOML'
[package]
name = "cargo-upgrade-fixture"
version = "1.0.0"
edition = "2024"
license = "MIT"
description = "Artifact Gateway schema upgrade fixture"
TOML
printf 'fn main() { println!("Cargo upgrade fixture"); }\n' >"$workspace/package/src/main.rs"
printf '[registries.upgrade]\nindex = "sparse+%s/cargo/%s/"\ncredential-provider = "cargo:token"\n' \
  "$gateway_url" "$source_name" >"$workspace/publish-home/config.toml"
(cd "$workspace/package" && CARGO_HOME="$workspace/publish-home" \
  CARGO_REGISTRIES_UPGRADE_TOKEN="$admin_token" HTTP_PROXY='' HTTPS_PROXY='' ALL_PROXY='' \
  NO_PROXY=127.0.0.1,localhost cargo publish --registry upgrade --allow-dirty --no-verify) >/dev/null

index_digest() {
  curl --silent --show-error --fail "${admin[@]}" "$gateway_url/cargo/$1/$index_path" |
    shasum -a 256 | awk '{print $1}'
}
archive_digest() {
  curl --silent --show-error --fail "${admin[@]}" \
    "$gateway_url/cargo/$1/api/v1/crates/$crate_name/$crate_version/download" |
    shasum -a 256 | awk '{print $1}'
}
assert_install() {
  local repository_name=$1 stage=$2 home="$workspace/home-$2-$1" output="$workspace/install-$2-$1"
  mkdir -p "$home"
  printf '[registries.upgrade]\nindex = "sparse+%s/cargo/%s/"\ncredential-provider = "cargo:token"\n' \
    "$gateway_url" "$repository_name" >"$home/config.toml"
  CARGO_HOME="$home" CARGO_TARGET_DIR="$workspace/target-$stage-$repository_name" \
    CARGO_REGISTRIES_UPGRADE_TOKEN="$admin_token" HTTP_PROXY='' HTTPS_PROXY='' ALL_PROXY='' \
    NO_PROXY=127.0.0.1,localhost cargo install "$crate_name" --registry upgrade --root "$output" >/dev/null
  test -x "$output/bin/$crate_name"
}

source_index_digest=$(index_digest "$source_name")
group_index_digest=$(index_digest "$group_name")
digest=$(archive_digest "$source_name")
identity_state=$(psql "SELECT digest||'|'||metadata_digest FROM native_cargo_identity_reservations WHERE repository_id='$source_id' AND collision_key='$crate_name' AND version_key='$crate_version'")
[[ -n "$identity_state" ]] || { printf '%s\n' 'Base Cargo identity reservation is missing.' >&2; exit 1; }
[[ $(archive_digest "$group_name") == "$digest" ]] || { printf '%s\n' 'Base Group archive digest differs.' >&2; exit 1; }
[[ $(psql "SELECT source_repository_id FROM native_cargo_group_versions WHERE group_id='$group_id' AND name='$crate_name' AND version='$crate_version'") == "$source_id" ]] || {
  printf '%s\n' 'Base Group owner is wrong.' >&2; exit 1;
}
assert_install "$source_name" base
assert_install "$group_name" base
baseline_migrations=$(psql "SELECT count(*) FROM artifact_gateway_schema_migrations WHERE filename IN ('000131_cargo_tombstone_reclaim.sql','000132_cargo_distribution.sql')")
[[ "$baseline_migrations" == 1 ]] || { printf '%s\n' 'Base database does not have the expected pre-distribution schema.' >&2; exit 1; }

"${old_compose[@]}" stop gateway
"${current_compose[@]}" build --build-arg "VERSION=$(tr -d '[:space:]' <VERSION)-main.${current_revision:0:12}" \
  --build-arg "REVISION=$current_revision" gateway
"${current_compose[@]}" up -d --no-build --wait

upgraded_migrations=$(psql "SELECT count(*) FROM artifact_gateway_schema_migrations WHERE filename IN ('000131_cargo_tombstone_reclaim.sql','000132_cargo_distribution.sql')")
[[ "$upgraded_migrations" == 2 ]] || { printf '%s\n' 'Forward migration did not reach Cargo distribution schema.' >&2; exit 1; }
"${current_compose[@]}" run --rm --no-deps migrate >"$workspace/migration-replay.log" 2>&1 || {
  cat "$workspace/migration-replay.log" >&2; exit 1;
}
grep -Fq 'Skipping applied migration 000132_cargo_distribution.sql.' "$workspace/migration-replay.log"
[[ $(index_digest "$source_name") == "$source_index_digest" ]] || { printf '%s\n' 'Upgraded source index changed.' >&2; exit 1; }
[[ $(index_digest "$group_name") == "$group_index_digest" ]] || { printf '%s\n' 'Upgraded Group index changed.' >&2; exit 1; }
[[ $(psql "SELECT digest||'|'||metadata_digest FROM native_cargo_identity_reservations WHERE repository_id='$source_id' AND collision_key='$crate_name' AND version_key='$crate_version'") == "$identity_state" ]] || {
  printf '%s\n' 'Upgraded Cargo identity reservation changed.' >&2; exit 1;
}
for repository_name in "$source_name" "$group_name"; do
  [[ $(archive_digest "$repository_name") == "$digest" ]] || { printf 'Upgraded %s archive digest changed.\n' "$repository_name" >&2; exit 1; }
  assert_install "$repository_name" upgraded
done
[[ $(psql "SELECT source_repository_id FROM native_cargo_group_versions WHERE group_id='$group_id' AND name='$crate_name' AND version='$crate_version'") == "$source_id" ]] || {
  printf '%s\n' 'Upgraded Group owner changed.' >&2; exit 1;
}
[[ $(psql "SELECT string_agg(repository_id::text, ',' ORDER BY position) FROM hosted_group_members WHERE group_id='$group_id'") == "$source_id,$other_id" ]] || {
  printf '%s\n' 'Upgraded Group member order changed.' >&2; exit 1;
}
printf 'Cargo upgrade readiness passed: %s -> %s; migrated PostgreSQL and reused RustFS; source/Group index, digest, owner, member order and fresh Cargo 1.96 installs preserved.\n' \
  "$base_revision" "$current_revision"
