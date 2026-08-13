#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
subject="$repo_root/scripts/local-dev.sh"
fake_command="$repo_root/scripts/testdata/local-dev/fake-command.sh"
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

mkdir -p "$workdir/bin" "$workdir/state"
for command_name in curl docker id launchctl node npm stat uname; do
  ln -s "$fake_command" "$workdir/bin/$command_name"
done
printf '%s\n' 'GATEWAY_HTTP_PORT=18081' 'GATEWAY_CONSOLE_PORT=4174' >"$workdir/.env"

export PATH="$workdir/bin:/usr/bin:/bin"
export LOCAL_DEV_TEST_STATE="$workdir/state"
export GATEWAY_ENV_FILE="$workdir/.env"
export GATEWAY_DEV_STATE_DIR="$workdir/dev-state"

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  exit 1
}

output=$($subject bootstrap-rustfs-env)
assert_migrated_key() {
  local name=$1 minimum_length=$2 value
  value=$(awk -F= -v name="$name" '$1 == name { print substr($0, index($0, "=") + 1); exit }' "$workdir/.env")
  [[ ${#value} -ge $minimum_length ]] || fail "$name was not generated with the required length"
}
assert_migrated_key RUSTFS_ACCESS_KEY 16
assert_migrated_key RUSTFS_SECRET_KEY 32
assert_migrated_key RUSTFS_RPC_SECRET 32
rustfs_secret=$(awk -F= '$1 == "RUSTFS_SECRET_KEY" { print substr($0, index($0, "=") + 1); exit }' "$workdir/.env")
rustfs_rpc_secret=$(awk -F= '$1 == "RUSTFS_RPC_SECRET" { print substr($0, index($0, "=") + 1); exit }' "$workdir/.env")
[[ "$rustfs_secret" != "$rustfs_rpc_secret" ]] || fail 'migration reused the S3 secret as the RPC secret'
[[ "$output" != *"$rustfs_secret"* && "$output" != *"$rustfs_rpc_secret"* ]] || fail 'bootstrap printed generated credentials'
backup_count=$(find "$GATEWAY_DEV_STATE_DIR/rustfs-env-backups" -maxdepth 1 -name '.env.before-rustfs-*' | wc -l | tr -d ' ')
[[ "$backup_count" == 1 ]] || fail 'bootstrap did not create exactly one rollback copy'
$subject bootstrap-rustfs-env >/dev/null
second_backup_count=$(find "$GATEWAY_DEV_STATE_DIR/rustfs-env-backups" -maxdepth 1 -name '.env.before-rustfs-*' | wc -l | tr -d ' ')
[[ "$second_backup_count" == 1 ]] || fail 'idempotent bootstrap created another rollback copy'
cp "$workdir/.env" "$workdir/.env.before-failure-tests"
awk '!/^RUSTFS_(ACCESS_KEY|SECRET_KEY|RPC_SECRET)=/' "$workdir/.env.before-failure-tests" >"$workdir/.env"
export LOCAL_DEV_TEST_MODE=env-write-fails
if output=$($subject bootstrap-rustfs-env 2>&1); then
  fail 'RustFS credential bootstrap succeeded after the atomic write failed'
fi
if compgen -G "$workdir/.env.rustfs.*" >/dev/null; then
  fail 'failed RustFS credential bootstrap left a temporary file containing secrets'
fi

cp "$workdir/.env.before-failure-tests" "$workdir/.env"

assert_contains() {
  local actual=$1
  local expected=$2
  [[ "$actual" == *"$expected"* ]] || fail "expected output to contain: $expected"
}

reset_state() {
  rm -f "$workdir/state/managed" "$workdir/state/docker.log" "$workdir/state/launchctl.log" "$workdir/state/runtime.log"
}

reset_state
export LOCAL_DEV_TEST_MODE=managed
output=$($subject start)
assert_contains "$output" 'http://127.0.0.1:4174/'
assert_contains "$output" 'http://127.0.0.1:18081/readyz'
[[ -f "$workdir/state/managed" ]] || fail 'start did not submit the Console supervisor job'
grep -Fq 'compose --env-file' "$workdir/state/docker.log" || fail 'start did not invoke Docker Compose'
grep -Fq 'VITE_GATEWAY_PROXY_TARGET=http://127.0.0.1:18081' "$workdir/state/launchctl.log" || fail 'start did not configure the Console API proxy'
grep -Fq -- '--strictPort' "$workdir/state/launchctl.log" || fail 'start allowed Vite to drift to another port'

reset_state
export LOCAL_DEV_TEST_MODE=legacy-minio
if output=$(GATEWAY_RUSTFS_MIGRATION_CONFIRMED=0 $subject start 2>&1); then
  fail 'start replaced legacy MinIO data with an empty RustFS volume'
fi
assert_contains "$output" 'Unsupported legacy MinIO resources were detected'
if grep -Fq ' up -d ' "$workdir/state/docker.log"; then
  fail 'start invoked Compose after detecting legacy MinIO data'
fi

if output=$(GATEWAY_RUSTFS_MIGRATION_CONFIRMED=1 GATEWAY_RUSTFS_MIGRATION_MANIFEST_SHA256="sha256:$(printf 'a%.0s' {1..64})" $subject guard 2>&1); then
  fail 'legacy MinIO guard accepted the removed migration bypass'
fi
assert_contains "$output" 'no longer provides an in-place compatibility or migration bypass'

export LOCAL_DEV_TEST_MODE=managed
$subject start >/dev/null
output=$($subject status)
assert_contains "$output" 'Console API'
assert_contains "$output" 'HTTP 200'
assert_contains "$output" 'HTTP 204'

$subject stop >/dev/null
[[ ! -f "$workdir/state/managed" ]] || fail 'stop left the managed Console running'
$subject stop >/dev/null

reset_state
export LOCAL_DEV_TEST_MODE=unmanaged
output=$($subject start)
assert_contains "$output" 'not managed by this checkout; it was left unchanged'
if [[ -f "$workdir/state/launchctl.log" ]] && grep -Fq 'submit' "$workdir/state/launchctl.log"; then
  fail 'start replaced an unmanaged Console'
fi

reset_state
export LOCAL_DEV_TEST_MODE=proxy-failed
if output=$($subject status 2>&1); then
  fail 'status succeeded while the Console API proxy was failing'
fi
assert_contains "$output" 'HTTP 500'

reset_state
export LOCAL_DEV_TEST_MODE=console-exits
if output=$($subject start 2>&1); then
  fail 'start succeeded after the Console supervisor exited'
fi
assert_contains "$output" 'Console exited before becoming ready'

missing_environment="$workdir/missing.env"
if output=$(GATEWAY_ENV_FILE="$missing_environment" $subject start 2>&1); then
  fail 'start succeeded without an environment file'
fi
assert_contains "$output" 'Missing'

reset_state
export LOCAL_DEV_TEST_OS=Linux
export LOCAL_DEV_TEST_MODE=managed
mkdir -p "$GATEWAY_DEV_STATE_DIR"
printf '%s %s\n' "$$" 0 >"$GATEWAY_DEV_STATE_DIR/console.pid"
$subject stop >/dev/null
kill -0 "$$" || fail 'Linux PID reuse test unexpectedly killed its caller'
[[ ! -f "$GATEWAY_DEV_STATE_DIR/console.pid" ]] || fail 'stop did not discard a stale Linux PID identity'
unset LOCAL_DEV_TEST_OS

printf 'local-dev tests passed\n'
