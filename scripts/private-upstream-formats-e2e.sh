#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
for binary in docker go curl tar node python3 cargo; do
  command -v "$binary" >/dev/null || { printf 'Private upstream acceptance requires %s\n' "$binary" >&2; exit 1; }
done
context=$(docker context show)
endpoint=$(docker context inspect "$context" --format '{{(index .Endpoints "docker").Host}}')
case "$endpoint" in
  unix://*) ;;
  *) printf 'Private upstream acceptance requires a local Docker Unix socket.\n' >&2; exit 1 ;;
esac
project="artifact-gateway-private-formats-${BASHPID}-${RANDOM}"
workdir=$(mktemp -d)
evidence="$root/output/private-upstream-formats-e2e/$project"
mkdir -p "$evidence" "$workdir/bin"
started=0
compose=(docker --context "$context" compose --env-file /dev/null --project-name "$project" -f compose.integration.yml)
cleanup() {
  local result=$?
  trap - EXIT
  local clients
  clients=$(docker --context "$context" ps -aq --filter "label=io.artifact-gateway.private-formats=$project")
  if [[ -n "$clients" ]]; then
    docker --context "$context" rm -f $clients >/dev/null 2>&1 || result=1
    clients=$(docker --context "$context" ps -aq --filter "label=io.artifact-gateway.private-formats=$project")
  fi
  if [[ -n "$clients" ]]; then result=1; fi
  if [[ "$started" == 1 ]]; then "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || result=1; fi
  local remaining
  remaining=$(docker --context "$context" ps -aq --filter "label=com.docker.compose.project=$project")
  if [[ -n "$remaining" ]]; then
    printf 'Owned private upstream containers remain: %s\n' "$remaining" >&2
    result=1
  fi
  local volumes networks
  volumes=$(docker --context "$context" volume ls -q --filter "label=com.docker.compose.project=$project")
  networks=$(docker --context "$context" network ls -q --filter "label=com.docker.compose.project=$project")
  if [[ -n "$volumes" || -n "$networks" ]]; then result=1; fi
  printf 'Private upstream owned project=%s cleanup=%s containers=%s volumes=%s networks=%s\n' "$project" "$result" "${remaining:-0}" "${volumes:-0}" "${networks:-0}"
  python3 - "$evidence/cleanup.json" "$project" "$result" "$remaining" "$volumes" "$networks" "$clients" <<'PYCLEAN'
import json, pathlib, sys
path, project, result, containers, volumes, networks, clients = sys.argv[1:]
pathlib.Path(path).write_text(json.dumps({'project':project, 'exitCode':int(result),
    'remainingContainers':containers, 'remainingVolumes':volumes,
    'remainingNetworks':networks, 'remainingClients':clients}, indent=2)+'\n')
PYCLEAN
  rm -rf "$workdir"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# Even `conan --version` initializes its home. Bind every bootstrap/version
# process before installing or invoking the client; per-case homes are set by Go.
export CONAN_HOME="$workdir/conan-bootstrap-home"
mkdir -m 0700 "$CONAN_HOME"
python3 - "$workdir" "$CONAN_HOME" <<'PYCONANHOME'
import os, pathlib, sys
work, home = map(pathlib.Path, sys.argv[1:])
if home.is_symlink() or home.resolve().parent != work.resolve() or home.stat().st_uid != os.getuid():
    raise SystemExit('Conan bootstrap home is not owned by this fixture')
PYCONANHOME
printf 'Private upstream owned project=%s docker-context=%s\n' "$project" "$context"
python3 - "$evidence/source.json" <<'PYSOURCE'
import hashlib, json, pathlib, subprocess, sys
files = set(subprocess.check_output(['git','ls-files','-c','-o','--exclude-standard','-z']).decode().split('\0'))-{''}
record = {'head':subprocess.check_output(['git','rev-parse','HEAD']).decode().strip(),
          'tree':subprocess.check_output(['git','rev-parse','HEAD^{tree}']).decode().strip(),
          'trackedClean':not subprocess.check_output(['git','status','--porcelain','--untracked-files=no']).strip(),
          'fileSHA256':{name:hashlib.sha256(pathlib.Path(name).read_bytes()).hexdigest() for name in sorted(files) if pathlib.Path(name).is_file()}}
pathlib.Path(sys.argv[1]).write_text(json.dumps(record,indent=2)+'\n')
PYSOURCE
# Resolve only already-installed fixed-version binaries. A rustup proxy loses
# its default toolchain under the client's owned HOME and must never install one.
python3 - "$workdir" "$evidence/cargo-toolchain.json" <<'PYCARGO'
import hashlib, json, os, pathlib, shutil, subprocess, sys
work, destination = map(pathlib.Path, sys.argv[1:])
home, cache = work/'cargo-bootstrap-home', work/'cargo-bootstrap-cache'
home.mkdir(mode=0o700)
cache.mkdir(mode=0o700)
environment = {'PATH':os.defpath, 'HOME':str(home), 'CARGO_HOME':str(cache),
               'TMPDIR':str(work), 'RUSTUP_AUTO_INSTALL':'0'}
installed = pathlib.Path(os.environ.get('RUSTUP_HOME', str(pathlib.Path.home()/'.rustup')))/'toolchains'
selected = None
for directory in sorted(installed.glob('*/bin')):
    tools = {}
    for name in ('cargo', 'rustc'):
        binary = directory/name
        if not binary.is_file() or not os.access(binary, os.X_OK) or binary.resolve().parent != directory.resolve():
            break
        try:
            result = subprocess.run([str(binary.resolve()), '--version'], cwd=work, env=environment,
                                    capture_output=True, text=True, timeout=15)
        except (OSError, subprocess.TimeoutExpired):
            break
        if result.returncode or not result.stdout.startswith(name+' 1.96.0 '):
            break
        tools[name] = {'path':str(binary.resolve()), 'version':result.stdout.strip(),
                       'sha256':hashlib.sha256(binary.read_bytes()).hexdigest()}
    if len(tools) == 2:
        selected = tools
        break
if selected is None:
    raise SystemExit('Private upstream acceptance requires already-installed cargo and rustc 1.96.0; no toolchain was installed')
ambient = shutil.which('cargo')
selected['selection'] = 'read-only installed binaries; no rustup invocation or default toolchain'
selected['ambientCargo'] = {'path':ambient, 'resolved':str(pathlib.Path(ambient).resolve()) if ambient else None, 'used':False}
destination.write_text(json.dumps(selected, indent=2)+'\n')
for name in ('cargo', 'rustc'):
    (work/(name+'-path')).write_text(selected[name]['path'])
PYCARGO
export PRIVATE_FORMATS_CARGO="$(cat "$workdir/cargo-path")"
export PRIVATE_FORMATS_RUSTC="$(cat "$workdir/rustc-path")"
export CARGO_HOME="$workdir/cargo-bootstrap-cache"
# Install only the fixture's fixed official clients, using public registries
# and private configuration/cache directories. No machine-wide install occurs.
python3 -m venv "$workdir/client"
PIP_CONFIG_FILE=/dev/null "$workdir/client/bin/python" -m pip --isolated install --index-url https://pypi.org/simple --cache-dir "$workdir/pip-cache" --disable-pip-version-check --no-input 'pip==26.2' 'conan==2.21.0'
export PRIVATE_FORMATS_PYTHON="$workdir/client/bin/python"
export CONAN_BINARY="$workdir/client/bin/conan"
[[ "$("$CONAN_BINARY" --version)" == 'Conan version 2.21.0' ]]
[[ $("$PRIVATE_FORMATS_CARGO" --version) == cargo\ 1.96.0\ * ]] || { printf 'Official Cargo 1.96.0 is required.\n' >&2; exit 1; }
"$PRIVATE_FORMATS_PYTHON" -m pip --version
"$CONAN_BINARY" --version
"$PRIVATE_FORMATS_CARGO" --version
"$PRIVATE_FORMATS_RUSTC" --version
curl --version | head -1
curl --disable --proto '=https' --tlsv1.2 --fail --silent --show-error --location --connect-timeout 15 --max-time 120 --retry 2 https://registry.npmjs.org/npm/-/npm-11.6.2.tgz --output "$workdir/npm.tgz"
python3 - "$workdir/npm.tgz" <<'PYNPM'
import base64, hashlib, pathlib, sys
expected = '7iKzNfy8lWYs3zq4oFPa8EXZz5xt9gQNKJZau3B1ErLBb6bF7sBJ00x09485DOvRT2l5Gerbl3VlZNT57MxJVA=='
if base64.b64encode(hashlib.sha512(pathlib.Path(sys.argv[1]).read_bytes()).digest()).decode() != expected:
    raise SystemExit('official npm 11.6.2 archive integrity mismatch')
PYNPM
mkdir "$workdir/npm-client"
tar -xzf "$workdir/npm.tgz" -C "$workdir/npm-client"
printf '#!/usr/bin/env bash\nexec node %q "$@"\n' "$workdir/npm-client/package/bin/npm-cli.js" >"$workdir/bin/npm"
chmod +x "$workdir/bin/npm"
export PRIVATE_FORMATS_NPM="$workdir/bin/npm"
[[ "$("$PRIVATE_FORMATS_NPM" --version)" == '11.6.2' ]]
export TEST_PRIVATE_FORMATS_DOCKER_HOST="$endpoint"
export TEST_PRIVATE_FORMATS_PROJECT="$project"
export TEST_PRIVATE_FORMATS_NETWORK="${project}_default"
"$PRIVATE_FORMATS_PYTHON" -m pip freeze >"$evidence/python-packages.txt"
python3 - "$evidence/clients.json" "$workdir/npm.tgz" <<'PYCLIENT'
import hashlib,json,os,pathlib,subprocess,sys
commands = {'npm':[os.environ['PRIVATE_FORMATS_NPM'],'--version'],
            'pip':[os.environ['PRIVATE_FORMATS_PYTHON'],'-m','pip','--version'],
            'cargo':[os.environ['PRIVATE_FORMATS_CARGO'],'--version'],
            'rustc':[os.environ['PRIVATE_FORMATS_RUSTC'],'--version'],
            'conan':[os.environ['CONAN_BINARY'],'--version'],
            'curl':['curl','--version']}
versions = {name:subprocess.check_output(command,text=True).splitlines()[0] for name,command in commands.items()}
record = {'versions':versions, 'npmArchiveSHA512':hashlib.sha512(pathlib.Path(sys.argv[2]).read_bytes()).hexdigest(),
          'aptImage':'debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241',
          'pythonPackagesSHA256':hashlib.sha256((pathlib.Path(sys.argv[1]).parent/'python-packages.txt').read_bytes()).hexdigest()}
record['cargoToolchain'] = json.loads((pathlib.Path(sys.argv[1]).parent/'cargo-toolchain.json').read_text())
home = pathlib.Path(os.environ['CONAN_HOME'])
if home.is_symlink() or home.resolve().parent != pathlib.Path(sys.argv[2]).resolve().parent or home.stat().st_uid != os.getuid():
    raise SystemExit('Conan version probes escaped the owned bootstrap home')
record['conanBootstrapHomeOwned'] = True
record['conanBootstrapHome'] = str(home.resolve())
pathlib.Path(sys.argv[1]).write_text(json.dumps(record,indent=2)+'\n')
PYCLIENT
started=1
"${compose[@]}" up -d --wait postgres rustfs
"${compose[@]}" run --rm --no-deps rustfs-ready
"${compose[@]}" run --rm --no-deps migrate
"${compose[@]}" run --rm --no-deps migrate
postgres_address=$("${compose[@]}" port postgres 5432)
rustfs_address=$("${compose[@]}" port rustfs 9000)
if ! TEST_DATABASE_URL="postgres://gateway:integration-password@${postgres_address}/gateway_test?sslmode=disable" \
TEST_RUSTFS_ENDPOINT="http://${rustfs_address}" \
TEST_RUSTFS_ACCESS_KEY=integration-rustfs \
TEST_RUSTFS_SECRET_KEY=integration-password \
  go test -json -count=1 -tags='integration privateformatsclient' ./internal/app ./internal/repository \
  -run '^Test(PostgresRustFSPrivateFormat(Credentials|OfficialClients)|Private(Raw|Conan|Native|Format).*|PostgresAPT.*|PostgresCargoProxyCacheAcrossConnections|PostgresCargoGroupPreflightsKnownHostedCoordinates|PostgresArtifactIdentitiesExcludeUncachedProxyMetadata|PyPIYankPostgresContract)$' \
  >"$evidence/results.jsonl" 2>&1; then
  cat "$evidence/results.jsonl" >&2
  exit 1
fi
python3 - "$evidence/results.jsonl" "$evidence/results.json" <<'PYRESULT'
import hashlib, json, pathlib, sys
path, destination = map(pathlib.Path, sys.argv[1:])
events = [json.loads(line) for line in path.read_text().splitlines()]
required = {'TestPostgresRustFSPrivateFormatCredentials', 'TestPostgresRustFSPrivateFormatOfficialClients', 'TestPrivateRawProxyCredentials', 'TestPrivateConanProxyCredentials', 'TestPrivateNativeFormatCredentials', 'TestPrivateFormatRedirectCredentialBoundary', 'TestPrivateRawConanRejectRedirectWithoutCredentialLeak', 'TestPrivateFormatSourceFailureDoesNotFallThrough', 'TestPrivateFormatCargoToolchainIsolation'}
required |= {'TestPostgresAPTEmptyAssetPage','TestPostgresAPTMetadataIsMutableAndPoolIsImmutable',
             'TestPostgresAPTCapacityAndSearchProjectionIncludeZeroByteMetadata',
             'TestPostgresCargoProxyCacheAcrossConnections','TestPostgresCargoGroupPreflightsKnownHostedCoordinates',
             'TestPostgresArtifactIdentitiesExcludeUncachedProxyMetadata','TestPyPIYankPostgresContract'}
required |= {'TestPrivateFormatNPMTarballPublication', 'TestPrivateFormatNPMTarballPublicationPostgres'}
required |= {'TestPrivateFormatDefaultPortCredentials', 'TestPrivateFormatDefaultPortCredentialsPostgres'}
for parent in ('TestPostgresRustFSPrivateFormatCredentials','TestPostgresRustFSPrivateFormatOfficialClients'):
    for fmt in ('npm','pypi','cargo','raw','apt','conan'):
        for scheme in ('basic','bearer'):
            for group in ('false','true'):
                required.add(f'{parent}/{fmt}/{scheme}/group={group}')
source = json.loads((path.parent/'source.json').read_text())
changed = [name for name,digest in source['fileSHA256'].items() if not pathlib.Path(name).is_file() or hashlib.sha256(pathlib.Path(name).read_bytes()).hexdigest()!=digest]
if changed: raise SystemExit('source changed during acceptance: '+', '.join(changed))

run = {e.get('Test') for e in events if e.get('Action') == 'run'}
passed = {e.get('Test') for e in events if e.get('Action') == 'pass'}
failed = {e.get('Test') or e.get('Package') for e in events if e.get('Action') == 'fail'}
skipped = {e.get('Test') for e in events if e.get('Action') == 'skip'}
summary = {'required': sorted(required), 'missingRun': sorted(required - run),
           'missingPass': sorted(required - passed), 'failed': sorted(failed),
           'skipped': sorted(skipped), 'passedCount': len(passed), 'head':source['head'], 'tree':source['tree'], 'trackedClean':source['trackedClean'], 'sourceUnchanged':not changed,
           'logSHA256': hashlib.sha256(path.read_bytes()).hexdigest()}
destination.write_text(json.dumps(summary, indent=2) + '\n')
for event in events:
    if event.get('Output'): print(event['Output'], end='')
if required - run or required - passed or failed or skipped:
    raise SystemExit('mandatory private upstream fixture missing, failed or skipped; see results.json')
print('Verified all six private native formats, real PG/RustFS reopen and fixed official clients, cold/hot caches, zero skips.')
PYRESULT
