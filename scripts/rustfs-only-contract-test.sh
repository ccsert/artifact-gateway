#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

active_paths=(
  go.mod
  go.sum
  internal/app
  internal/config
  internal/objectstore
  internal/preflight
  cmd
  compose.yml
  compose.integration.yml
  .github/workflows
  scripts/integration-test.sh
  scripts/upgrade-readiness.sh
  README.md
  ARCHITECTURE.md
  docs/apt-hosted-roadmap.md
  docs/backend-completion-checklist.md
  docs/full-artifact-repository-goal.md
  docs/full-artifact-repository-roadmap.md
  docs/kubernetes-deployment.md
  docs/recovery-runbook.md
  docs/release-readiness.md
)

if rg --line-number --ignore-case --glob '!**/*_test.go' 'minio|GATEWAY_S3_|TEST_S3_|github.com/minio' "${active_paths[@]}"; then
  printf '%s\n' 'active runtime, dependency, deployment, or roadmap files still contain legacy object-store compatibility' >&2
  exit 1
fi

if [[ -e compose.upgrade-minio.yml || -e docs/rustfs-migration.md || -e cmd/s3-migrate || -e internal/objectstore/s3_migration.go ]]; then
  printf '%s\n' 'legacy MinIO cutover tooling is still shipped' >&2
  exit 1
fi

services=$(docker compose -f compose.integration.yml config --services)
if grep -Eiq '(^|-)minio($|-)' <<<"$services"; then
  printf '%s\n' 'integration topology still starts MinIO' >&2
  exit 1
fi
grep -Fxq rustfs <<<"$services" || {
  printf '%s\n' 'integration topology does not contain RustFS' >&2
  exit 1
}

printf '%s\n' 'RustFS-only contract checks passed'
