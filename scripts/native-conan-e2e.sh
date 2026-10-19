#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
for binary in python3 docker go; do
  command -v "$binary" >/dev/null || { printf 'Native Conan E2E requires %s\n' "$binary" >&2; exit 1; }
done
workdir=$(mktemp -d)
project="artifact-gateway-conan-$$-${RANDOM}"
python3 - "$workdir/compose.conan.yml" <<'PY'
import pathlib, socket, sys
sockets = [socket.socket(), socket.socket()]
try:
    for s in sockets: s.bind(('127.0.0.1', 0))
    pg, s3 = [s.getsockname()[1] for s in sockets]
    pathlib.Path(sys.argv[1]).write_text(f'''services:
  postgres:
    ports: !override ["127.0.0.1:{pg}:5432"]
  rustfs:
    ports: !override ["127.0.0.1:{s3}:9000"]
''')
finally:
    for s in sockets: s.close()
PY
# Select fresh loopback ports once per fixture, then keep those bindings stable
# across real container restarts. Docker's empty host-port binding may move.
compose=(docker compose --env-file /dev/null --project-name "$project" -f "$repo_root/compose.integration.yml" -f "$workdir/compose.conan.yml")
started=0
cleanup() {
  local status=$?
  if [[ "$started" == 1 ]] && ! "${compose[@]}" down -v --remove-orphans; then status=1; fi
  rm -rf "$workdir"
  trap - EXIT
  exit "$status"
}
trap cleanup EXIT
export CONAN_HOME="$workdir/conan-home"
python3 -m venv "$workdir/client"
"$workdir/client/bin/python" -m pip install --disable-pip-version-check --no-input 'conan==2.21.0'
export CONAN_BINARY="$workdir/client/bin/conan"
[[ "$("$CONAN_BINARY" --version)" == 'Conan version 2.21.0' ]]
printf 'Official Conan 2.21.0; owned persistence fixture %s\n' "$project"
"$workdir/client/bin/python" -m pip freeze
started=1
"${compose[@]}" up -d --wait --wait-timeout 60 postgres rustfs
"${compose[@]}" run --rm --no-deps rustfs-ready
"${compose[@]}" run --rm --no-deps migrate
"${compose[@]}" run --rm --no-deps migrate
postgres_address=$("${compose[@]}" port postgres 5432)
rustfs_address=$("${compose[@]}" port rustfs 9000)
export TEST_DATABASE_URL="postgres://gateway:integration-password@${postgres_address}/gateway_test?sslmode=disable"
export TEST_RUSTFS_ENDPOINT="http://${rustfs_address}"
export TEST_RUSTFS_ACCESS_KEY=integration-rustfs
export TEST_RUSTFS_SECRET_KEY=integration-password
export TEST_CONAN_COMPOSE_PROJECT="$project"
export TEST_CONAN_COMPOSE_FILE="$repo_root/compose.integration.yml"
export TEST_CONAN_COMPOSE_OVERRIDE="$workdir/compose.conan.yml"
export CONAN_PERSISTENCE_REQUIRED=1
# Synthetic protocol signing material, separate from the fixture machine credential.
export TEST_CONAN_TOKEN_SECRET=synthetic-native-conan-signing-key-312-only
go test -json -count=1 -p 1 -tags=integration ./internal/app ./internal/repository \
  -run '^TestConan2Client|^TestPostgres.*Conan' \
  | tee "$workdir/results.jsonl"
python3 - "$workdir/results.jsonl" <<'PY'
import json, pathlib, sys
required = {'TestConan2ClientUploadsAndInstallsNative',
            'TestPostgresRustFSConanNativeClientRestartAndColdInstall',
            'TestPostgresConanNativePublication',
            'TestPostgresRustFSConanPromotionSharesVisibleRevision',
            'TestPostgresRustFSConanReplicationPublishesTargetOwnedRevision',
            'TestPostgresNativeConanLifecycleStateTransitions',
            'TestPostgresConanReferenceSearchProjection',
            'TestPostgresAndRustFSConanReclaimRetriesAndPreventsRestore',
            'TestPostgresConanGroupPreservesManagedRepositoryBinding',
            'TestPostgresArtifactIdentitiesUseResolvableRawAndConanAssets'}
passed = {e.get('Test') for e in map(json.loads, pathlib.Path(sys.argv[1]).read_text().splitlines())
          if e.get('Action') == 'pass'}
if required - passed:
    raise SystemExit('Required Conan native/persistence fixture did not pass: ' + ', '.join(sorted(required-passed)))
print('Verified all required Conan native, storage and restart client fixtures')
PY
