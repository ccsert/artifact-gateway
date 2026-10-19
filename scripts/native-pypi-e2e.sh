#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
temporary=$(mktemp -d /tmp/artifact-gateway-pypi-e2e.XXXXXX)
cleanup() {
  rm -rf -- "$temporary"
}
trap cleanup EXIT

python3 -m venv "$temporary/venv"
"$temporary/venv/bin/python" -m pip install --disable-pip-version-check --quiet 'pip==26.2' 'twine==6.2.0'
"$temporary/venv/bin/python" - <<'PY'
import hashlib, importlib.metadata, pathlib, sys
for package, expected in [('pip', '26.2'), ('twine', '6.2.0')]:
    actual = importlib.metadata.version(package)
    assert actual == expected, (package, actual)
    print(f'Official {package} client: {actual}')
print('Python executable SHA-256:', hashlib.sha256(pathlib.Path(sys.executable).read_bytes()).hexdigest())
PY

cd "$root"
ARTIFACT_GATEWAY_PYPI_CLI_E2E=1 \
ARTIFACT_GATEWAY_PYTHON="$temporary/venv/bin/python" \
go test ./internal/app -run '^TestNativePyPIRealTwineUploadAndPipDownload$|^TestPyPIYankRealPipHostedProxyGroup$' -count=1 -v
