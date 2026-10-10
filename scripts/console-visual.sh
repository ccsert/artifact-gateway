#!/usr/bin/env bash
set -euo pipefail

# Console visual baseline (#270). Screenshots are only comparable when rendered
# with the same fonts and Chromium build, so local runs and CI both execute in
# the pinned Playwright container. Pass --update-snapshots to accept changes.

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
version=$(node -p "require('$root/console/node_modules/@playwright/test/package.json').version" 2>/dev/null ||
  node -p "require('$root/console/package-lock.json').packages['node_modules/@playwright/test'].version")
image="mcr.microsoft.com/playwright:v${version}-noble"

docker run --rm --init --ipc=host \
  -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
  -v "$root:/work" \
  -v artifact-gateway-console-visual-node-modules:/work/console/node_modules \
  -w /work/console \
  "$image" \
  bash -c '
    npm ci --no-audit --no-fund --loglevel=error
    status=0
    npx playwright test -c playwright.visual.config.ts "$@" || status=$?
    # Hand generated baselines and reports back to the invoking user.
    chown -R "$HOST_UID:$HOST_GID" e2e/visual/__screenshots__ test-results 2>/dev/null || true
    exit $status
  ' \
  console-visual "$@"
