#!/usr/bin/env bash
set -euo pipefail
umask 077

# This gate uses synthetic read-only Nexus REST inventory and owned PostgreSQL /
# RustFS only. It never discovers, modifies, or migrates an operator's Nexus.
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
for binary in python3 docker go node curl tar; do
  command -v "$binary" >/dev/null || { printf 'npm migration E2E requires %s\n' "$binary" >&2; exit 1; }
done

workdir=$(mktemp -d "${TMPDIR:-/tmp}/artifact-gateway-npm-migration-e2e.XXXXXX")
project="artifact-gateway-npm-migration-$$-${RANDOM}"
evidence="$repo_root/output/nexus-npm-migration-e2e/$project"
mkdir -p "$evidence"
compose=(docker compose --env-file /dev/null --project-name "$project" -f "$repo_root/compose.integration.yml" -f "$workdir/compose.npm.yml")
started=0
cleanup() {
  local status=$?
  local cleanup_status=0
  local down_status=0
  local temporary_status=0
  trap - EXIT
  if [[ "$started" == 1 ]]; then
    if "${compose[@]}" down -v --remove-orphans >"$evidence/cleanup.log" 2>&1; then
      down_status=0
    else
      down_status=$?
      cleanup_status=1
    fi
  fi
  if rm -rf "$workdir"; then
    temporary_status=0
  else
    temporary_status=$?
    cleanup_status=1
  fi
  if ! python3 -B - "$project" "$evidence/cleanup.json" "$workdir" "$cleanup_status" "$down_status" "$temporary_status" "$status" <<'PYCLEANUP'
import json, pathlib, subprocess, sys
project, destination, temporary, initial, down, removal, run = sys.argv[1:]
code = int(initial)
commands = {"containers": ["docker", "ps", "-aq"],
            "volumes": ["docker", "volume", "ls", "-q"],
            "networks": ["docker", "network", "ls", "-q"]}
remaining, readback = {}, {}
for name, command in commands.items():
    try:
        result = subprocess.run(command + ["--filter", "label=com.docker.compose.project=" + project],
                                capture_output=True, text=True, check=False)
        readback[name] = result.returncode
        remaining[name] = result.stdout.splitlines() if result.returncode == 0 else ["readback_failed"]
    except OSError:
        readback[name], remaining[name] = 1, ["readback_failed"]
    if readback[name] or remaining[name]:
        code = 1
removed = not pathlib.Path(temporary).exists()
if not removed:
    code = 1
receipt = {"project": project, "exitCode": code, "runExitCode": int(run),
           "ownedTemporaryRemoved": removed, "composeDownExitCode": int(down),
           "temporaryRemovalExitCode": int(removal), "readbackExitCodes": readback,
           **remaining}
pathlib.Path(destination).write_text(json.dumps(receipt, indent=2) + "\n")
if code:
    raise SystemExit("owned npm migration fixture cleanup is incomplete")
PYCLEANUP
  then cleanup_status=1; fi
  if [[ "$cleanup_status" != 0 ]]; then status=1; fi
  if [[ "$status" != 0 ]]; then
    printf 'npm migration gate failed; retained evidence: %s\n' "$evidence" >&2
    if [[ -f "$evidence/results.jsonl" ]]; then tail -n 20 "$evidence/results.jsonl" >&2; fi
    if [[ -f "$evidence/setup.log" ]]; then tail -n 10 "$evidence/setup.log" >&2; fi
  else
    printf 'npm migration gate passed; all owned resources removed; evidence: %s\n' "$evidence"
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

python3 -B - "$workdir/compose.npm.yml" <<'PY'
import pathlib, socket, sys
sockets = [socket.socket(), socket.socket()]
try:
    for sock in sockets:
        sock.bind(("127.0.0.1", 0))
    postgres, rustfs = [sock.getsockname()[1] for sock in sockets]
    pathlib.Path(sys.argv[1]).write_text(f'''services:
  postgres:
    ports: !override ["127.0.0.1:{postgres}:5432"]
  rustfs:
    ports: !override ["127.0.0.1:{rustfs}:9000"]
''')
finally:
    for sock in sockets:
        sock.close()
PY
python3 -B - "$evidence/source.json" <<'PY'
import json, pathlib, subprocess, sys
def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()
pathlib.Path(sys.argv[1]).write_text(json.dumps({"head": git("rev-parse", "HEAD"),
    "tree": git("rev-parse", "HEAD^{tree}"), "dirty": bool(git("status", "--porcelain")),
    "source": "synthetic-read-only-nexus-rest", "npmVersion": "11.6.2", "repositoryPresets": "none"}, indent=2) + "\n")
PY

npm_client_version=11.6.2
npm_client_archive="$workdir/npm-client.tgz"
curl --disable --fail --silent --show-error --proto '=https' --tlsv1.2 \
  --retry 3 --connect-timeout 15 --max-time 120 \
  "https://registry.npmjs.org/npm/-/npm-${npm_client_version}.tgz" \
  --output "$npm_client_archive"
node - "$npm_client_archive" <<'NODE'
const fs = require('node:fs');
const { createHash } = require('node:crypto');
const expected = 'sha512-7iKzNfy8lWYs3zq4oFPa8EXZz5xt9gQNKJZau3B1ErLBb6bF7sBJ00x09485DOvRT2l5Gerbl3VlZNT57MxJVA==';
const actual = `sha512-${createHash('sha512').update(fs.readFileSync(process.argv[2])).digest('base64')}`;
if (actual !== expected) throw new Error('official npm client archive integrity mismatch');
NODE
mkdir "$workdir/npm-client"
tar -xzf "$npm_client_archive" -C "$workdir/npm-client"
npm() { node "$workdir/npm-client/package/bin/npm-cli.js" "$@"; }
if [[ "$(npm --version)" != "$npm_client_version" ]]; then
  printf 'Official npm client version mismatch\n' >&2
  exit 1
fi
export NPM_CLIENT_NODE="$(command -v node)"
export NPM_CLIENT_JS="$workdir/npm-client/package/bin/npm-cli.js"
export NPM_MIGRATION_REQUIRED=1
export GATEWAY_REPOSITORY_PRESETS=none
export NPM_MIGRATION_EVIDENCE="$evidence"
node --version >"$evidence/client.txt"
npm --version >>"$evidence/client.txt"
started=1
"${compose[@]}" up -d --wait --wait-timeout 60 postgres rustfs >>"$evidence/setup.log" 2>&1
"${compose[@]}" run --rm --no-deps rustfs-ready >>"$evidence/setup.log" 2>&1
"${compose[@]}" run --rm --no-deps migrate >>"$evidence/setup.log" 2>&1
"${compose[@]}" run --rm --no-deps migrate >>"$evidence/setup.log" 2>&1
postgres_address=$("${compose[@]}" port postgres 5432)
rustfs_address=$("${compose[@]}" port rustfs 9000)
export TEST_DATABASE_URL="postgres://gateway:integration-password@${postgres_address}/gateway_test?sslmode=disable"
export TEST_RUSTFS_ENDPOINT="http://${rustfs_address}"
export TEST_RUSTFS_ACCESS_KEY=integration-rustfs
export TEST_RUSTFS_SECRET_KEY=integration-password
test_status=0
go test -json -count=1 -p 1 -timeout 6m -tags=integration ./internal/app \
  -run '^Test(PostgresRustFSNexusNPMMigration|PostgresNexusNPMMigration|PostgresMigrationTargetOnlyNPMClient)' \
  >"$evidence/results.jsonl" 2>&1 || test_status=$?
python3 -B - "$evidence/results.jsonl" "$evidence/results.json" "$test_status" <<'PYRESULT'
import hashlib, json, pathlib, sys
path,destination=map(pathlib.Path,sys.argv[1:3])
exit_code=int(sys.argv[3])
required=set(['TestPostgresRustFSNexusNPMMigrationCompleteClosureAndRestart', 'TestPostgresRustFSNexusNPMMigrationRecoversUploadVerifyAndCommitAckLoss', 'TestPostgresNexusNPMMigrationRejectsMissingWrongIntegrityIdentityAndSourceDrift', 'TestPostgresNexusNPMMigrationLeaseAndSourceVersionFence', 'TestPostgresNexusNPMMigrationNativeTransactionLossCannotPublish', 'TestPostgresNexusNPMMigrationPreservesTargetTagsAndDeprecation', 'TestPostgresNexusNPMMigrationQuarantineCannotBecomeVerifiedSkip', 'TestPostgresMigrationTargetOnlyNPMClient', 'TestPostgresNexusNPMMigrationAtomicTargetConflictAndQuota'])
events=[json.loads(line)for line in path.read_text().splitlines()]
run={e.get('Test')for e in events if e.get('Action')=='run' and e.get('Test')}
passed={e.get('Test')for e in events if e.get('Action')=='pass' and e.get('Test')}
failed={e.get('Test')or e.get('Package')for e in events if e.get('Action')=='fail'}
skipped={e.get('Test')for e in events if e.get('Action')=='skip'}
packages={e.get('Package')for e in events if e.get('Action')=='pass'and not e.get('Test')}
summary={'exitCode':exit_code,'packages':sorted(packages),'required':sorted(required),'requiredCount':len(required),'caseCount':len(run),'passedCaseCount':len(passed),'missingRun':sorted(required-run),'missingPass':sorted(required-passed),'failed':sorted(failed),'skipped':sorted(skipped),'logSHA256':hashlib.sha256(path.read_bytes()).hexdigest()}
destination.write_text(json.dumps(summary,indent=2)+'\n')
for e in events:
    if e.get('Output'):print(e['Output'],end='')
if exit_code or required-run or required-passed or failed or skipped or packages!={"github.com/ccsert/artifact-gateway/internal/app"}:raise SystemExit('mandatory npm migration proof missing, skipped or failed')
print('All nine required npm migration fixtures passed: complete byte/SRI/identity closure, actual transaction fence, restart/recovery, target governance and pinned official target-only npm.')
PYRESULT
