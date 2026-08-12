#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
subject="$repo_root/scripts/local-dev.sh"
fake_command="$repo_root/scripts/testdata/local-dev/fake-command.sh"
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

mkdir -p "$workdir/bin" "$workdir/state"
for command_name in curl docker id launchctl node npm uname; do
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
if output=$($subject start 2>&1); then
  fail 'start replaced legacy MinIO data with an empty RustFS volume'
fi
assert_contains "$output" 'Legacy MinIO data was detected'
if grep -Fq ' up -d ' "$workdir/state/docker.log"; then
  fail 'start invoked Compose after detecting legacy MinIO data'
fi

GATEWAY_RUSTFS_MIGRATION_CONFIRMED=1 $subject guard

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
