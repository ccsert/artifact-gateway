#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
recovery=0
if [[ "${1:-}" == --recovery && $# == 1 ]]; then
  recovery=1
elif [[ $# != 0 ]]; then
  printf '%s\n' 'usage: published-native-backup-test.sh [--recovery]' >&2
  exit 2
fi
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
mkdir -p "$workdir/public-assets"
for version in 0.5.0 0.6.0; do
  curl --proto '=https' --tlsv1.2 --fail --silent --show-error --location \
    --retry 2 --connect-timeout 10 --max-time 120 \
    "https://github.com/ccsert/artifact-gateway/releases/download/v$version/artifact-gateway_${version}_linux_amd64.tar.gz" \
    --output "$workdir/public-assets/artifact-gateway_${version}_linux_amd64.tar.gz"
  curl --proto '=https' --tlsv1.2 --fail --silent --show-error --location \
    --retry 2 --connect-timeout 10 --max-time 120 \
    "https://github.com/ccsert/artifact-gateway/releases/download/v$version/SHA256SUMS" \
    --output "$workdir/public-assets/v${version//./}-SHA256SUMS"
done
python3 - "$workdir" <<'PY'
import hashlib, json, sys, tarfile
from pathlib import Path
root = Path(sys.argv[1])
pins = json.loads(Path('internal/backupops/testdata/published-native-provenance.json').read_text())
for release in pins['releases']:
    archive = root / 'public-assets' / release['archive']['name']
    sums = root / 'public-assets' / ('v' + release['version'].replace('.', '') + '-SHA256SUMS')
    for file, pin in [(archive, release['archive']), (sums, release['checksums'])]:
        if file.stat().st_size != pin['size'] or 'sha256:' + hashlib.sha256(file.read_bytes()).hexdigest() != pin['sha256']:
            raise SystemExit('original published asset differs from fixed public provenance')
    if release['archive']['sha256'][7:] + '  ' + archive.name not in sums.read_text().splitlines():
        raise SystemExit('published checksum record disagrees with archive pin')
    destination = root / ('native-' + release['version'])
    destination.mkdir(mode=0o700)
    # Extraction belongs only to the fixture downloader, after original public
    # bytes are pinned. Product verification only streams the archive.
    with tarfile.open(archive, 'r:gz') as source:
        source.extractall(destination, filter='data')
    print('original public native asset provenance verified:', release['version'], release['archive']['sha256'])
PY
BACKUPOPS_NATIVE_ASSETS="$workdir" go test -v -count=1 ./internal/backupops -run '^TestPublishedNativeRelease$'
if [[ "$recovery" == 1 ]]; then
  # Explicit new random projects/volumes only, through the existing owned-target
  # fixture; no checkout .env or existing deployment is selected.
  BACKUPOPS_NATIVE_ASSETS="$workdir" BACKUPOPS_DOCKER_TEST=1 \
    BACKUPOPS_DOCKER_CONTEXT="$(docker context show)" \
    go test -v -count=1 ./internal/backupops -run '^TestPublishedNativeRecoveryIntegration$'
fi
