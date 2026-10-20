#!/usr/bin/env bash
set -euo pipefail
umask 077

# This gate uses synthetic read-only Nexus REST inventory and owned PostgreSQL /
# RustFS only. It never discovers, modifies, or migrates an operator's Nexus.
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
for binary in python3 docker go; do
  command -v "$binary" >/dev/null || { printf 'Conan migration E2E requires %s\n' "$binary" >&2; exit 1; }
done

workdir=$(mktemp -d "${TMPDIR:-/tmp}/artifact-gateway-conan-migration-e2e.XXXXXX")
project="artifact-gateway-conan-migration-$$-${RANDOM}"
evidence="$repo_root/output/nexus-conan-migration-e2e/$project"
mkdir -p "$evidence"
compose=(docker compose --env-file /dev/null --project-name "$project" -f "$repo_root/compose.integration.yml" -f "$workdir/compose.conan.yml")
started=0
cleanup() {
  local status=$?
  trap - EXIT
  if [[ "$started" == 1 ]]; then
    if ! "${compose[@]}" down -v --remove-orphans >"$evidence/cleanup.log" 2>&1; then status=1; fi
  fi
  if ! python3 - "$project" "$evidence/cleanup.json" <<'PY'
import json, pathlib, subprocess, sys
project, destination = sys.argv[1:]
commands = {"containers": ["docker", "ps", "-aq"],
            "volumes": ["docker", "volume", "ls", "-q"],
            "networks": ["docker", "network", "ls", "-q"]}
remaining = {name: subprocess.check_output(command + ["--filter", "label=com.docker.compose.project=" + project], text=True).splitlines()
             for name, command in commands.items()}
pathlib.Path(destination).write_text(json.dumps({"project": project, **remaining}, indent=2) + "\n")
if any(remaining.values()):
    raise SystemExit("owned Conan migration fixture cleanup is incomplete")
PY
  then status=1; fi
  rm -rf "$workdir"
  if [[ "$status" != 0 ]]; then
    printf 'Conan migration gate failed; retained evidence: %s\n' "$evidence" >&2
    if [[ -f "$evidence/results.jsonl" ]]; then tail -n 20 "$evidence/results.jsonl" >&2; fi
    if [[ -f "$evidence/setup.log" ]]; then tail -n 10 "$evidence/setup.log" >&2; fi
  else
    printf 'Conan migration gate passed; all owned resources removed; evidence: %s\n' "$evidence"
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

python3 - "$workdir/compose.conan.yml" <<'PY'
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
python3 - "$evidence/source.json" <<'PY'
import json, pathlib, subprocess, sys
def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()
pathlib.Path(sys.argv[1]).write_text(json.dumps({"head": git("rev-parse", "HEAD"),
    "tree": git("rev-parse", "HEAD^{tree}"), "dirty": bool(git("status", "--porcelain")),
    "source": "synthetic-read-only-nexus-rest", "conanVersion": "2.21.0"}, indent=2) + "\n")
PY

# Explicitly select an owned home before every Conan command, even --version.
# No inherited Conan home, credentials, or caller-selected backend is used.
export CONAN_HOME="$workdir/conan-home"
mkdir -p "$CONAN_HOME"
python3 - "$workdir" "$CONAN_HOME" <<'PY'
import pathlib, sys
owned, home = map(lambda value: pathlib.Path(value).resolve(), sys.argv[1:])
if not owned.is_dir() or not home.is_dir() or home == owned or not home.is_relative_to(owned):
    raise SystemExit("refuse a Conan home outside the owned fixture directory")
PY
python3 -m venv "$workdir/client" >>"$evidence/setup.log" 2>&1
PIP_CACHE_DIR="$workdir/pip-cache" "$workdir/client/bin/python" -m pip install \
  --disable-pip-version-check --no-input 'conan==2.21.0' >>"$evidence/setup.log" 2>&1
export CONAN_BINARY="$workdir/client/bin/conan"
[[ "$(CONAN_HOME="$CONAN_HOME" "$CONAN_BINARY" --version)" == 'Conan version 2.21.0' ]]
"$workdir/client/bin/python" -m pip freeze >"$evidence/client.txt"

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
export TEST_CONAN_COMPOSE_PROJECT="$project"
export TEST_CONAN_COMPOSE_FILE="$repo_root/compose.integration.yml"
export TEST_CONAN_COMPOSE_OVERRIDE="$workdir/compose.conan.yml"
export CONAN_MIGRATION_REQUIRED=1
export TEST_CONAN_TOKEN_SECRET=synthetic-conan-migration-signing-key-425-only

go test -json -count=1 -p 1 -timeout 6m -tags=integration ./internal/app \
  -run '^Test(PostgresRustFSNexusConanMigration|PostgresNexusConanMigration|PostgresMigrationTargetOnlyConan2Client)' \
  >"$evidence/results.jsonl" 2>&1
python3 - "$evidence/results.jsonl" "$evidence/results.json" <<'PY'
import hashlib, json, pathlib, sys
path, destination = map(pathlib.Path, sys.argv[1:])
required = {"TestPostgresMigrationTargetOnlyConan2Client",
            "TestPostgresNexusConanMigrationFinalVerifyCannotOutliveOwner",
            "TestPostgresNexusConanMigrationNativeTransactionLossCannotPublish",
            "TestPostgresRustFSNexusConanMigrationCompleteRevisionReopen",
            "TestPostgresRustFSNexusConanMigrationExpiredSessionUsesFreshObjects",
            "TestPostgresNexusConanMigrationGuardPreventsVisibilityAndAudit",
            "TestPostgresRustFSNexusConanMigrationStagedBytesRemainImmutable",
            "TestPostgresRustFSNexusConanMigrationRecoversUploadAndCommitAckLoss",
            "TestPostgresNexusConanMigrationRejectsSameSizeSourceDrift",
            "TestPostgresNexusConanMigrationQuarantineCannotBecomeVerifiedSkip"}
events = [json.loads(line) for line in path.read_text().splitlines()]
run = {event.get("Test") for event in events if event.get("Action") == "run"}
passed = {event.get("Test") for event in events if event.get("Action") == "pass"}
failed = {event.get("Test") or event.get("Package") for event in events if event.get("Action") == "fail"}
skipped = {event.get("Test") for event in events if event.get("Action") == "skip"}
packages = {event.get("Package") for event in events if event.get("Action") == "pass" and not event.get("Test")}
summary = {"required": sorted(required), "passedRequired": sorted(required & passed),
           "missingRun": sorted(required - run), "missingPass": sorted(required - passed),
           "failed": sorted(failed), "skipped": sorted(skipped), "packages": sorted(packages),
           "logSHA256": hashlib.sha256(path.read_bytes()).hexdigest()}
destination.write_text(json.dumps(summary, indent=2) + "\n")
for event in events:
    if event.get("Output"):
        print(event["Output"], end="")
if required - run or required - passed or failed or skipped or packages != {"github.com/ccsert/artifact-gateway/internal/app"}:
    raise SystemExit("mandatory Conan migration proof is missing, skipped or failed; see results.json")
print("Verified all 10 required Conan migration fixtures: complete revision bytes, actual transaction fencing, restart/retry, quarantine, and pinned target-only official client.")
PY
