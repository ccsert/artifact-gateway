#!/usr/bin/env bash
set -euo pipefail
umask 077

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
for binary in docker go curl python3 tar uname; do
  command -v "$binary" >/dev/null || { printf 'Nexus OCI migration client gate requires %s\n' "$binary" >&2; exit 1; }
done

work=$(mktemp -d)
project="artifact-gateway-oci-migration-$$-$RANDOM"
evidence="$repo_root/output/nexus-oci-migration-e2e/$project"
mkdir -p "$evidence"
compose=(docker compose --env-file /dev/null --project-name "$project" -f compose.integration.yml -f "$work/compose.json")
started=0
cleanup() {
  local result=$?
  local containers="" volumes="" networks=""
  trap - EXIT
  if [[ "$started" == 1 ]]; then
    if ! "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1; then
      printf 'Owned OCI migration fixture cleanup failed: %s\n' "$project" >&2
      result=1
    fi
    for resource in container volume network; do
      if [[ "$resource" == container ]]; then
        remaining=$(docker ps -aq --filter "label=com.docker.compose.project=$project")
      else
        remaining=$(docker "$resource" ls -q --filter "label=com.docker.compose.project=$project")
      fi
      case "$resource" in
        container) containers="$remaining" ;;
        volume) volumes="$remaining" ;;
        network) networks="$remaining" ;;
      esac
      if [[ -n "$remaining" ]]; then
        printf 'Owned OCI migration %s resources remain: %s\n' "$resource" "$project" >&2
        result=1
      fi
    done
    if [[ "$result" == 0 ]]; then
      printf 'Owned OCI migration resources removed: containers=0 volumes=0 networks=0 project=%s\n' "$project"
    fi
  fi
  python3 - "$evidence/cleanup.json" "$project" "$result" "$containers" "$volumes" "$networks" <<'PYCLEAN'
import json, pathlib, sys
pathlib.Path(sys.argv[1]).write_text(json.dumps({'project': sys.argv[2], 'exitCode': int(sys.argv[3]),
    'remainingContainers': sys.argv[4].splitlines(), 'remainingVolumes': sys.argv[5].splitlines(),
    'remainingNetworks': sys.argv[6].splitlines()}, indent=2) + '\n')
PYCLEAN
  rm -rf "$work"
  exit "$result"
}
trap cleanup EXIT

# Pin the official release and its published SHA-256. No user client, Docker
# content store, registry credentials, config, proxy or layer cache is reused.
platform="$(uname -s)_$(uname -m)"
case "$platform" in
  Darwin_arm64) archive_name=go-containerregistry_Darwin_arm64.tar.gz; digest=2231fc8df8806d20d680ff1225db44e095a55dd6ac1ae8eced4faf4b278b78fb ;;
  Darwin_x86_64) archive_name=go-containerregistry_Darwin_x86_64.tar.gz; digest=6fedd06a648c11335f0e8b9547e4783002c30e9336047fec292942cd518bb799 ;;
  Linux_aarch64|Linux_arm64) archive_name=go-containerregistry_Linux_arm64.tar.gz; digest=898c0cff975f898a33e8c4580bdafb0e7c02c7faa33374e946762f97c4ab7110 ;;
  Linux_x86_64) archive_name=go-containerregistry_Linux_x86_64.tar.gz; digest=0ab7a1d6932a213aed964ce97666c3077fe691c8606413674a8b3e0b9ec4cda0 ;;
  *) printf 'Unsupported official crane client platform: %s\n' "$platform" >&2; exit 1 ;;
esac
curl --disable --proto '=https' --tlsv1.2 --fail --silent --show-error --location \
  --connect-timeout 15 --max-time 120 --retry 2 \
  "https://github.com/google/go-containerregistry/releases/download/v0.22.1/$archive_name" \
  --output "$work/crane.tar.gz"
python3 - "$work" "$digest" <<'PY'
import hashlib, json, pathlib, secrets, sys, tarfile
work = pathlib.Path(sys.argv[1])
archive = work / 'crane.tar.gz'
if hashlib.sha256(archive.read_bytes()).hexdigest() != sys.argv[2]:
    raise SystemExit('official crane v0.22.1 release SHA-256 mismatch')
with tarfile.open(archive) as source:
    member = source.getmember('crane')
    if not member.isfile():
        raise SystemExit('official release contains an invalid crane executable')
    (work / 'crane').write_bytes(source.extractfile(member).read())
(work / 'crane').chmod(0o700)
password, access, secret = secrets.token_hex(24), 'oci' + secrets.token_hex(10), secrets.token_hex(24)
(work / 'credentials.json').write_text(json.dumps({'password': password, 'access': access, 'secret': secret}))
(work / 'compose.json').write_text(json.dumps({'services': {
    'postgres': {'environment': {'POSTGRES_PASSWORD': password}},
    'migrate': {'environment': {'PGPASSWORD': password}},
    'rustfs': {'environment': {'RUSTFS_ACCESS_KEY': access, 'RUSTFS_SECRET_KEY': secret, 'RUSTFS_RPC_SECRET': secrets.token_hex(32)}}}}))
PY

started=1
"${compose[@]}" up -d --wait postgres rustfs
"${compose[@]}" run --rm --no-deps rustfs-ready
"${compose[@]}" run --rm --no-deps migrate
"${compose[@]}" run --rm --no-deps migrate
postgres_address=$("${compose[@]}" port postgres 5432)
rustfs_address=$("${compose[@]}" port rustfs 9000)
python3 - "$work" "$postgres_address" "$rustfs_address" "$evidence" <<'PY'
import hashlib, json, os, pathlib, subprocess, sys
work = pathlib.Path(sys.argv[1])
credentials = json.loads((work / 'credentials.json').read_text())
env = dict(os.environ)
env.update({
    'TEST_DATABASE_URL': 'postgres://gateway:' + credentials['password'] + '@' + sys.argv[2] + '/gateway_test?sslmode=disable',
    'TEST_RUSTFS_ENDPOINT': 'http://' + sys.argv[3],
    'TEST_RUSTFS_ACCESS_KEY': credentials['access'],
    'TEST_RUSTFS_SECRET_KEY': credentials['secret'],
    'MIGRATION_CRANE_BIN': str(work / 'crane'),
})
evidence = pathlib.Path(sys.argv[4])
log = evidence / 'client.jsonl'
with log.open('w') as output:
    result = subprocess.run(['go', 'test', '-json', '-count=1', '-tags=integration,ociclient',
        './internal/app', './internal/repository', './internal/nexusmigration/pgstore', '-run', '^Test(PostgresRustFSNexusOCI|PostgresOCIImport|PostgresNexusOCI)',
        '-timeout', '180s'], env=env, stdout=output, stderr=subprocess.STDOUT)
required = {
    'TestPostgresRustFSNexusOCICompletePublication',
    'TestPostgresRustFSNexusOCIReusesCurrentNativeObjectKeys',
    'TestPostgresRustFSNexusOCIGarbageCollectionAndImportSerialize',
    'TestPostgresRustFSNexusOCIPublicationFenceAuditAndRestart',
    'TestPostgresOCIImportClosureLocksShareOneConnection',
    'TestPostgresNexusOCIMigrationCompleteIncrementalAndRestart',
    'TestPostgresNexusOCIMigrationUploadCrashAndSourceUnauthorized',
    'TestPostgresNexusOCIMigrationTargetOnlyCraneClient',
}
passed, skipped, failed, cold = set(), [], [], set()
cold_required = {'TestPostgresNexusOCIMigrationTargetOnlyCraneClient/' + variant + '/' + platform
    for variant in ['oci-index', 'docker-list', 'nested-oci-index']
    for platform in ['all', 'linux/amd64', 'linux/arm64']}
for line in log.read_text().splitlines():
    try:
        event = json.loads(line)
    except ValueError:
        print(line)
        continue
    if event.get('Output'):
        print(event['Output'], end='')
    if event.get('Action') == 'pass' and event.get('Test') in required:
        passed.add(event['Test'])
    if event.get('Action') == 'pass' and event.get('Test') in cold_required:
        cold.add(event['Test'])
    if event.get('Action') == 'skip':
        skipped.append(event.get('Test'))
    if event.get('Action') == 'fail':
        failed.append(event.get('Test', event.get('Package')))
summary = {'schemaVersion': 1, 'testExitCode': result.returncode, 'requiredPassed': sorted(passed),
    'requiredMissing': sorted(required - passed), 'coldLayoutsPassed': sorted(cold),
    'coldLayoutsMissing': sorted(cold_required - cold), 'skipped': skipped, 'failed': failed,
    'logSHA256': hashlib.sha256(log.read_bytes()).hexdigest()}
(evidence / 'results.json').write_text(json.dumps(summary, indent=2) + '\n')
print(json.dumps(summary))
if result.returncode or passed != required or cold != cold_required or skipped or failed:
    raise SystemExit('mandatory OCI migration persistence/client gate failed, missing, or skipped')
print('Nexus OCI migration official client gate passed: crane v0.22.1; source stopped; fresh PostgreSQL owner and Gateway; independent all/amd64/arm64 empty OCI stores; OCI/Docker/nested original roots and complete byte objects. Loopback HTTP proof, not production cutover.')
PY
