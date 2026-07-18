#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

if [[ ! -f .env ]]; then
  printf '%s\n' 'Missing .env. Copy .env.example, choose local-only credentials, then rerun.' >&2
  exit 1
fi

docker compose --env-file .env -f compose.gitea.yml down --volumes --remove-orphans
rm -rf .gitea-fixture

