#!/bin/sh
set -eu

if [ "${#RUSTFS_SECRET_KEY}" -lt 8 ]; then
  printf '%s\n' 'RUSTFS_SECRET_KEY must be at least 8 characters' >&2
  exit 1
fi
if [ "${#RUSTFS_RPC_SECRET}" -lt 32 ]; then
  printf '%s\n' 'RUSTFS_RPC_SECRET must be at least 32 characters' >&2
  exit 1
fi
if [ "$RUSTFS_RPC_SECRET" = "$RUSTFS_SECRET_KEY" ]; then
  printf '%s\n' 'RUSTFS_RPC_SECRET must differ from RUSTFS_SECRET_KEY' >&2
  exit 1
fi

exec /entrypoint.sh rustfs
