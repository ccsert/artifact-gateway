#!/usr/bin/env bash
set -euo pipefail
umask 077

# This gate uses synthetic read-only Nexus REST inventory and owned PostgreSQL /
# RustFS only. It never discovers, modifies, or migrates an operator's Nexus.
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
for binary in python3 docker go curl; do
  command -v "$binary" >/dev/null || { printf 'pypi migration E2E requires %s\n' "$binary" >&2; exit 1; }
done

workdir=$(mktemp -d "${TMPDIR:-/tmp}/artifact-gateway-pypi-migration-e2e.XXXXXX")
project="artifact-gateway-pypi-migration-$$-${RANDOM}"
evidence="$repo_root/output/nexus-pypi-migration-e2e/$project"
mkdir -p "$evidence"
compose=(docker compose --env-file /dev/null --project-name "$project" -f "$repo_root/compose.integration.yml" -f "$workdir/compose.pypi.yml")
started=0

bounded() {
  local seconds=$1
  shift
  python3 - "$seconds" "$@" <<'PYBOUND'
import os, signal, subprocess, sys
seconds, command = float(sys.argv[1]), sys.argv[2:]
process = subprocess.Popen(command, start_new_session=True)
def stop(signum, frame):
    os.killpg(process.pid, signal.SIGTERM)
    try: process.wait(timeout=2)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait(timeout=2)
    raise SystemExit(128 + signum)
signal.signal(signal.SIGINT, stop)
signal.signal(signal.SIGTERM, stop)
try:
    code = process.wait(timeout=seconds)
except subprocess.TimeoutExpired:
    stop(signal.SIGTERM, None)
raise SystemExit(code)
PYBOUND
}

cleanup() {
  local status=$?
  local cleanup_status=0
  local down_status=0
  local temporary_status=0
  trap - EXIT
  if [[ "$started" == 1 ]]; then
    if bounded 45 "${compose[@]}" down -v --remove-orphans >"$evidence/cleanup.log" 2>&1; then
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
                                capture_output=True, text=True, check=False, timeout=30)
        readback[name] = result.returncode
        remaining[name] = result.stdout.splitlines() if result.returncode == 0 else ["readback_failed"]
    except (OSError, subprocess.TimeoutExpired):
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
    raise SystemExit("owned pypi migration fixture cleanup is incomplete")
PYCLEANUP
  then cleanup_status=1; fi
  if [[ "$cleanup_status" != 0 ]]; then status=1; fi
  if [[ "$status" != 0 ]]; then
    printf 'pypi migration gate failed; retained evidence: %s\n' "$evidence" >&2
    if [[ -f "$evidence/results.jsonl" ]]; then tail -n 20 "$evidence/results.jsonl" >&2; fi
    if [[ -f "$evidence/setup.log" ]]; then tail -n 10 "$evidence/setup.log" >&2; fi
  else
    printf 'pypi migration gate passed; all owned resources removed; evidence: %s\n' "$evidence"
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

python3 -B - "$workdir/compose.pypi.yml" <<'PY'
import ipaddress, os, pathlib, socket, sys
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
    subnet = os.environ.get("PYPI_MIGRATION_SUBNET", "")
    if subnet:
        network = ipaddress.ip_network(subnet, strict=True)
        if network.version != 4 or not network.is_private or network.prefixlen < 24:
            raise ValueError("owned PyPI fixture subnet must be private IPv4 /24 or smaller")
        with pathlib.Path(sys.argv[1]).open("a") as output:
            output.write("networks:\n  default:\n    ipam:\n      config:\n        - subnet: " + str(network) + "\n")
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
    "source": "synthetic-read-only-nexus-rest", "pipVersion": "26.2", "repositoryPresets": "none"}, indent=2) + "\n")
PY

python3 -m venv "$workdir/venv"
pip_archive="$workdir/pip-26.2-py3-none-any.whl"
pip_url='https://files.pythonhosted.org/packages/62/36/a3aed958d60531cb442b7ab4596cda7b3621cfb916f8ae1d6769795c7dc1/pip-26.2-py3-none-any.whl'
curl --disable --fail --silent --show-error --proto '=https' --tlsv1.2 \
  --connect-timeout 10 --max-time 40 --retry 0 "$pip_url" --output "$pip_archive" \
  >"$evidence/client-download.stdout" 2>"$evidence/client-download.stderr"
python3 -B - "$pip_archive" "$evidence/official-pip.json" "$pip_url" <<'PYPIP'
import hashlib, json, pathlib, sys
archive, receipt = map(pathlib.Path, sys.argv[1:3])
body = archive.read_bytes()
expected = "931c303696af6fa3417112103b1cad26890e5a07eccb5b99783700e33f2b8aad"
actual = hashlib.sha256(body).hexdigest()
verified = actual == expected and len(body) == 1816475
receipt.write_text(json.dumps({"version":"26.2", "sourceURL":sys.argv[3],
    "metadataSource":"https://pypi.org/pypi/pip/26.2/json", "sha256":actual,
    "expectedSHA256":expected,"size":len(body),"verified":verified}, indent=2)+"\n")
if not verified:
    raise SystemExit("official pip wheel checksum/size mismatch")
PYPIP
bounded 60 "$workdir/venv/bin/python" -m pip --isolated install --disable-pip-version-check --no-cache-dir --no-index --no-deps "$pip_archive" >"$evidence/client-setup.log" 2>&1
export PYPI_MIGRATION_PYTHON="$workdir/venv/bin/python"
export PYPI_MIGRATION_REQUIRED=1
export PYPI_MIGRATION_EVIDENCE="$evidence"
export GATEWAY_REPOSITORY_PRESETS=none
"$PYPI_MIGRATION_PYTHON" -c 'import importlib.metadata; assert importlib.metadata.version("pip") == "26.2"; print("Official pip 26.2")' >"$evidence/client.txt"
started=1
bounded 90 "${compose[@]}" up -d --wait --wait-timeout 60 postgres rustfs >>"$evidence/setup.log" 2>&1
bounded 90 "${compose[@]}" run --rm --no-deps rustfs-ready >>"$evidence/setup.log" 2>&1
bounded 90 "${compose[@]}" run --rm --no-deps migrate >>"$evidence/setup.log" 2>&1
bounded 90 "${compose[@]}" run --rm --no-deps migrate >>"$evidence/setup.log" 2>&1
postgres_address=$("${compose[@]}" port postgres 5432)
rustfs_address=$("${compose[@]}" port rustfs 9000)
export TEST_DATABASE_URL="postgres://gateway:integration-password@${postgres_address}/gateway_test?sslmode=disable"
export TEST_RUSTFS_ENDPOINT="http://${rustfs_address}"
export TEST_RUSTFS_ACCESS_KEY=integration-rustfs
export TEST_RUSTFS_SECRET_KEY=integration-password
test_status=0
go test -json -count=1 -p 1 -timeout 6m -tags=integration ./internal/app \
  -run '^Test(PostgresRustFSNexusPyPIMigration|PostgresNexusPyPIMigration|PostgresMigrationTargetOnlyPyPIClient)' \
  >"$evidence/results.jsonl" 2>&1 || test_status=$?
python3 -B - "$evidence/results.jsonl" "$evidence/results.json" "$test_status" <<'PYRESULT'
import hashlib, json, pathlib, sys
path,destination=map(pathlib.Path,sys.argv[1:3])
exit_code=int(sys.argv[3])
required=set(['TestPostgresRustFSNexusPyPIMigrationClosureAndRestart', 'TestPostgresRustFSNexusPyPIMigrationRecovery', 'TestPostgresNexusPyPIMigrationRejectsIncompleteAndDrift', 'TestPostgresNexusPyPIMigrationCurrentAdministrator', 'TestPostgresNexusPyPIMigrationLeaseAndSourceFence', 'TestPostgresNexusPyPIMigrationAtomicConflictQuotaAudit', 'TestPostgresMigrationTargetOnlyPyPIClient', 'TestPostgresNexusPyPIMigrationNativeTransactionLossCannotPublish','TestPostgresNexusPyPIMigrationRejectsAuthenticationKindCollisions','TestPostgresNexusPyPIMigrationScheduledOwnerAuthority','TestPostgresNexusPyPIMigrationDurableOwnerBindingAndPoolOne'])
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
if exit_code or required-run or required-passed or failed or skipped or packages!={"github.com/ccsert/artifact-gateway/internal/app"}:raise SystemExit('mandatory pypi migration proof missing, skipped or failed')
print('All eleven required PyPI migration fixtures passed: complete bytes/selection/identity, native transaction fence and progress, current authority, restart/recovery, governance and pinned target-only pip.')
PYRESULT
