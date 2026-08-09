#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
temporary=$(mktemp -d /tmp/artifact-gateway-pypi-e2e.XXXXXX)
cleanup() {
  rm -rf -- "$temporary"
}
trap cleanup EXIT

python3 -m venv "$temporary/venv"
"$temporary/venv/bin/python" -m pip install --disable-pip-version-check --quiet twine

cd "$root"
ARTIFACT_GATEWAY_PYPI_CLI_E2E=1 \
ARTIFACT_GATEWAY_PYTHON="$temporary/venv/bin/python" \
go test ./internal/app -run '^TestNativePyPIRealTwineUploadAndPipDownload$' -count=1 -v
