#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

environment_file=${GATEWAY_ENV_FILE:-.env}

if [[ ! -f "$environment_file" ]]; then
  printf '%s\n' 'Missing .env. Copy .env.example, choose local-only credentials, then rerun.' >&2
  exit 1
fi

docker compose --env-file "$environment_file" -f compose.gitea.yml up -d --wait
