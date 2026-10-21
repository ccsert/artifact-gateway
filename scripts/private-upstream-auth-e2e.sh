#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
for binary in docker go curl tar java javac python3; do
  command -v "$binary" >/dev/null || { printf 'Private upstream acceptance requires %s\n' "$binary" >&2; exit 1; }
done
context=$(docker context show)
endpoint=$(docker context inspect "$context" --format '{{(index .Endpoints "docker").Host}}')
case "$endpoint" in
  unix://*) ;;
  *) printf 'Private upstream acceptance requires a local Docker Unix socket.\n' >&2; exit 1 ;;
esac
project="artifact-gateway-private-auth-${BASHPID}-${RANDOM}"
workdir=$(mktemp -d)
evidence="$root/output/private-upstream-auth-e2e/$project"
mkdir -p "$evidence" "$workdir/bin"
started=0
compose=(docker --context "$context" compose --env-file /dev/null --project-name "$project" -f compose.integration.yml)
cleanup() {
  local result=$?
  trap - EXIT
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
  rm -rf "$workdir"
  if [[ -e "$workdir" ]]; then result=1; fi
  printf '{"project":"%s","exitCode":%s,"remainingContainers":"%s","remainingVolumes":"%s","remainingNetworks":"%s","temporaryDirectory":"%s","temporaryDirectoryRemoved":%s}\n' "$project" "$result" "$remaining" "$volumes" "$networks" "$workdir" "$([[ ! -e "$workdir" ]] && printf true || printf false)" >"$evidence/cleanup.json"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
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
# Pin official clients and verify release digests before running them. These
# executables, settings and caches stay inside this fixture's private directory.
curl_args=(--disable --proto '=https' --tlsv1.2 --fail --silent --show-error --location --connect-timeout 15 --max-time 120 --retry 2)
curl "${curl_args[@]}" https://dlcdn.apache.org/maven/maven-3/3.9.16/binaries/apache-maven-3.9.16-bin.tar.gz --output "$workdir/maven.tar.gz"
python3 - "$workdir/maven.tar.gz" <<'PYHASH'
import hashlib, pathlib, sys
expected = "831a8591fe20c8243b1dbe7d71e3244f31d1665b0804b2e825e38cbbe5ce0cafb8338851f90780735568773e0a6cd07bbec107cda0b896b008b861075358b6f6"
if hashlib.sha512(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest() != expected:
    raise SystemExit("official Maven 3.9.16 archive SHA-512 mismatch")
PYHASH
tar -xzf "$workdir/maven.tar.gz" -C "$workdir"
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) oras_platform=linux_amd64; oras_sha=9ce999f8d2de03fc03968b29d743077a58783e545e5eaa53917ca177352d0e59 ;;
  Linux/aarch64|Linux/arm64) oras_platform=linux_arm64; oras_sha=ac7156f93a21e903f7ad606c792f3560f17e0cd0e36365634701b1e7cc4e4eca ;;
  Darwin/x86_64) oras_platform=darwin_amd64; oras_sha=aeb684d8c24c18dce28fd1f7326636e4782b573108e244a93d4b1c4a5ec50f48 ;;
  Darwin/arm64) oras_platform=darwin_arm64; oras_sha=f33fc12753c54172b0d0d19eaa0318d3f90fe9b094d96e8b259c881713c92e1c ;;
  *) printf 'Unsupported official ORAS platform\n' >&2; exit 1 ;;
esac
curl "${curl_args[@]}" "https://github.com/oras-project/oras/releases/download/v1.3.3/oras_1.3.3_${oras_platform}.tar.gz" --output "$workdir/oras.tar.gz"
python3 - "$workdir/oras.tar.gz" "$oras_sha" <<'PYHASH'
import hashlib, pathlib, sys
if hashlib.sha256(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest() != sys.argv[2]:
    raise SystemExit("official ORAS 1.3.3 archive SHA-256 mismatch")
PYHASH
tar -xzf "$workdir/oras.tar.gz" -C "$workdir/bin" oras
curl "${curl_args[@]}" https://services.gradle.org/distributions/gradle-9.3.1-bin.zip --output "$workdir/gradle.zip"
python3 - "$workdir" "$evidence/clients.json" "$oras_platform" <<'PYGRADLE'
import hashlib, json, os, pathlib, subprocess, sys, zipfile
work, destination = map(pathlib.Path, sys.argv[1:3])
archive = work/'gradle.zip'
expected = 'b266d5ff6b90eada6dc3b20cb090e3731302e553a27c5d3e4df1f0d76beaff06'
if hashlib.sha256(archive.read_bytes()).hexdigest() != expected:
    raise SystemExit('official Gradle 9.3.1 distribution SHA-256 mismatch')
with zipfile.ZipFile(archive) as source:
    for entry in source.infolist():
        if not (work/entry.filename).resolve().is_relative_to(work.resolve()):
            raise SystemExit('Gradle distribution member escapes the owned directory')
    source.extractall(work)
binary = work/'gradle-9.3.1/bin/gradle'
binary.chmod(0o700)
home, cache, temporary = work/'gradle-bootstrap-home', work/'gradle-bootstrap-cache', work/'gradle-bootstrap-tmp'
for directory in (home, cache, temporary):
    directory.mkdir(mode=0o700)
environment = {'PATH':os.environ['PATH'], 'HOME':str(home), 'GRADLE_USER_HOME':str(cache),
               'TMPDIR':str(temporary), 'LANG':'C', 'GRADLE_OPTS':'-Duser.home='+str(home)}
version = subprocess.run([str(binary), '--version'], cwd=work, env=environment, capture_output=True, text=True, timeout=60)
if version.returncode or '\nGradle 9.3.1\n' not in version.stdout:
    raise SystemExit('pinned Gradle prerequisite failed: '+version.stdout+version.stderr)
destination.write_text(json.dumps({'maven':{'version':'3.9.16', 'mode':'owned-official-distribution',
    'archiveSHA512':hashlib.sha512((work/'maven.tar.gz').read_bytes()).hexdigest()},
    'oras':{'version':'1.3.3', 'platform':sys.argv[3], 'mode':'owned-official-release',
    'archiveSHA256':hashlib.sha256((work/'oras.tar.gz').read_bytes()).hexdigest()},
    'gradle':{'version':'9.3.1', 'mode':'owned-official-distribution',
    'url':'https://services.gradle.org/distributions/gradle-9.3.1-bin.zip',
    'checksumSource':'https://gradle.org/release-checksums/#9.3.1', 'archiveSHA256':expected,
    'binarySHA256':hashlib.sha256(binary.read_bytes()).hexdigest(), 'versionOutput':version.stdout,
    'ownedBootstrapHome':True}}, indent=2)+'\n')
PYGRADLE
export PRIVATE_AUTH_GRADLE="$workdir/gradle-9.3.1/bin/gradle"
export PATH="$workdir/apache-maven-3.9.16/bin:$workdir/bin:$PATH"
printf 'Pinned clients: Maven 3.9.16 (SHA-512), ORAS 1.3.3 and Gradle 9.3.1 (SHA-256)\n'
started=1
"${compose[@]}" up -d --wait postgres rustfs
"${compose[@]}" run --rm --no-deps rustfs-ready
"${compose[@]}" run --rm --no-deps migrate
"${compose[@]}" run --rm --no-deps migrate
postgres_address=$("${compose[@]}" port postgres 5432)
rustfs_address=$("${compose[@]}" port rustfs 9000)
test_exit=0
if ! TEST_DATABASE_URL="postgres://gateway:integration-password@${postgres_address}/gateway_test?sslmode=disable" \
TEST_RUSTFS_ENDPOINT="http://${rustfs_address}" \
TEST_RUSTFS_ACCESS_KEY=integration-rustfs \
TEST_RUSTFS_SECRET_KEY=integration-password \
  go test -json -count=1 -tags='integration mavenclient ociclient gradleclient' ./internal/app \
  -run '^Test(PostgresRustFSPrivateProxy(Credentials|OfficialClients|GradleOfficialClient)|Private.*)$' \
  >"$evidence/results.jsonl" 2>&1; then
  test_exit=1
fi
python3 - "$evidence/results.jsonl" "$evidence/results.json" "$test_exit" <<'PYRESULT'
import collections, hashlib, json, pathlib, sys
path, destination = map(pathlib.Path, sys.argv[1:3])
test_exit = int(sys.argv[3])
events = [json.loads(line) for line in path.read_text().splitlines()]
required = {'TestPostgresRustFSPrivateProxyCredentials', 'TestPostgresRustFSPrivateProxyOfficialClients',
            'TestPrivateProxyUpstreamCredentials', 'TestPrivateOCIIssuerCredentials',
            'TestPrivateProxyAPIAdmitsOnlyImplementedFormats', 'TestPrivateProxyInvalidCiphertextFailsBeforeNetwork',
            'TestPrivateProxyCredentialRedirectBoundary', 'TestPrivateOCIIssuerRedirectNeverReattachesCredential',
            'TestPrivateProxyUnauthorizedDoesNotContactAnonymousGroupFallback', 'TestPrivateOCICacheRechecksEgressAndRequiresAudit',
            'TestPrivateProxyCredentialRebinding', 'TestPrivateProxyCredentialRebindingPostgres',
            'TestPrivateProxyCircuitDoesNotFallThrough', 'TestPrivateProxyCircuitDoesNotFallThroughPostgres'}
required.add('TestPostgresRustFSPrivateProxyGradleOfficialClient')
for scheme in ('basic','bearer'):
    for route in ('proxy','group'):
        parent = f'TestPostgresRustFSPrivateProxyGradleOfficialClient/{scheme}/{route}'
        required.add(parent)
        for stage in ('missing','cold','restarted-cache','wrong','rotated','expired-source-401','expired-source-403','cleared','restored','disconnected-cache'):
            required.add(parent+'/'+stage)
source = json.loads((path.parent/'source.json').read_text())
changed = [name for name,digest in source['fileSHA256'].items() if not pathlib.Path(name).is_file() or hashlib.sha256(pathlib.Path(name).read_bytes()).hexdigest()!=digest]
clients = json.loads((path.parent/'clients.json').read_text())
outputs = collections.defaultdict(str)
for event in events:
    if event.get('Test'):
        outputs[event['Test']] += event.get('Output', '')
# test2json can split a single long t.Log line into multiple Output events.
# Reassemble by test before parsing the complete JSON evidence line.
def records(marker):
    return [json.loads(line.split(marker,1)[1]) for output in outputs.values() for line in output.splitlines() if marker in line]
gradle = records('PRIVATE_GRADLE_CLIENT ')
rejections = records('PRIVATE_GRADLE_REJECTION ')
clients['gradle']['consumptions'] = gradle
clients['gradle']['rejections'] = rejections
expected = {(scheme,route,stage) for scheme in ('basic','bearer') for route in ('proxy','group') for stage in ('missing','cold','restarted-cache','wrong','rotated','cleared','restored','disconnected-cache')}
actual = {(r['scheme'],r['route'],r['round']) for r in gradle}
expected_rejections = {(scheme,route,stage) for scheme in ('basic','bearer') for route in ('proxy','group') for stage in ('missing','wrong','cleared')}
actual_rejections = {(r['scheme'],r['route'],r['round']) for r in rejections}
valid_clients = expected == actual and len(gradle) == len(expected) and expected_rejections == actual_rejections and len(rejections) == len(expected_rejections)
valid_clients &= all(r['sourceRequests'] > 0 and r['sourceStatus'] == 401 and r['gatewayStatus'] == 502 and r['anonymousMemberRequests'] == 0 and 'status code 502' in r['output'] for r in rejections)
for record in gradle:
    success = record['round'] not in ('missing','wrong','cleared')
    valid_clients &= record['expectedSuccess'] == success and record['fullPayloadCheck'] == success and record['version'] == '9.3.1' and record['emptyClientCache'] and record['targetOnly']
    if success:
        valid_clients &= len(record['objects']) == 6 and all(o['size'] > 0 and len(o['sha256']) == 64 for o in record['objects'])
clients['gradle']['validMatrix'] = bool(valid_clients)
(path.parent/'clients.json').write_text(json.dumps(clients,indent=2)+'\n')
run = {e['Test'] for e in events if e.get('Action') == 'run' and e.get('Test')}
passed = {e['Test'] for e in events if e.get('Action') == 'pass' and e.get('Test')}
failed = {e.get('Test') or e.get('Package') for e in events if e.get('Action') == 'fail'}
skipped = {e.get('Test') for e in events if e.get('Action') == 'skip'}
summary = {'required': sorted(required), 'missingRun': sorted(required - run),
           'missingPass': sorted(required - passed), 'failed': sorted(failed),
           'skipped': sorted(skipped), 'passedCount': len(passed), 'testExitCode':test_exit,
           'head':source['head'], 'tree':source['tree'], 'trackedClean':source['trackedClean'], 'sourceUnchanged':not changed,
           'gradleClientMatrixValid':bool(valid_clients), 'gradleConsumptionCount':len(gradle),
           'logSHA256': hashlib.sha256(path.read_bytes()).hexdigest()}
destination.write_text(json.dumps(summary, indent=2) + '\n')
for event in events:
    if event.get('Output'): print(event['Output'], end='')
if test_exit or required - run or required - passed or failed or skipped or changed or not valid_clients:
    raise SystemExit('mandatory private upstream fixture missing, failed or skipped; see results.json')
print('Verified private Maven/OCI/Gradle proxy and group authentication with pinned official clients, cold and hot caches, zero skipped tests.')
PYRESULT
