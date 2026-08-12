#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
subject="$root/scripts/run-rustfs.sh"

expect_rejected() {
  local expected=$1
  shift
  local output
  if output=$(env "$@" sh "$subject" 2>&1); then
    printf 'RustFS launcher accepted invalid secrets: %s\n' "$*" >&2
    exit 1
  fi
  [[ "$output" == *"$expected"* ]] || {
    printf 'RustFS launcher error %q does not contain %q\n' "$output" "$expected" >&2
    exit 1
  }
}

expect_rejected 'RUSTFS_SECRET_KEY must be at least 8 characters' \
  RUSTFS_SECRET_KEY=short RUSTFS_RPC_SECRET=independent-rpc-secret-0123456789
expect_rejected 'RUSTFS_RPC_SECRET must be at least 32 characters' \
  RUSTFS_SECRET_KEY=valid-secret RUSTFS_RPC_SECRET=short
expect_rejected 'RUSTFS_RPC_SECRET must differ from RUSTFS_SECRET_KEY' \
  RUSTFS_SECRET_KEY=same-secret-value-01234567890123456789 \
  RUSTFS_RPC_SECRET=same-secret-value-01234567890123456789

printf '%s\n' 'RustFS launcher validation tests passed'
