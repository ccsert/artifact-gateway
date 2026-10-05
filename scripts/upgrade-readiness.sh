#!/usr/bin/env bash
# Fixed v0.5.0 -> candidate alert schema gate; synthetic resources only, no .env.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
baseline=ea60aea333b29bb60d4ac1b8e2b2a8720563726f
[[ $(git rev-parse 'v0.5.0^{commit}') == "$baseline" ]] || {
  printf '%s\n' 'The fixed formal v0.5.0 commit is required.' >&2
  exit 1
}
BACKUPOPS_DOCKER_TEST=1 \
BACKUPOPS_DOCKER_CONTEXT="$(docker context show)" \
  go test -v -count=1 ./internal/backupops -run '^TestLocalAlertReleaseUpgrade$'
