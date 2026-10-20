#!/usr/bin/env bash
set -euo pipefail
umask 077
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
for binary in python3 docker go; do
  command -v "$binary" >/dev/null || { printf 'Nexus inventory scan E2E requires %s\n' "$binary" >&2; exit 1; }
done
# The only source is an in-process read-only TLS fixture. This gate does not
# contact or migrate an existing Nexus installation.
project="artifact-gateway-inventory-$$-${RANDOM}"
evidence="$repo_root/output/nexus-inventory-scan-e2e/$project"
mkdir -p "$evidence"
compose=(docker compose --env-file /dev/null --project-name "$project" -f "$repo_root/compose.integration.yml")
started=0
cleanup() {
  local status=$?
  if [[ "$started" == 1 ]] && ! "${compose[@]}" down -v --remove-orphans; then status=1; fi
  local containers volumes networks
  containers=$(docker ps -aq --filter "label=com.docker.compose.project=$project")
  volumes=$(docker volume ls -q --filter "label=com.docker.compose.project=$project")
  networks=$(docker network ls -q --filter "label=com.docker.compose.project=$project")
  if [[ -n "$containers$volumes$networks" ]]; then status=1; fi
  printf '{"project":"%s","exitCode":%s,"remainingContainers":"%s","remainingVolumes":"%s","remainingNetworks":"%s"}\n' "$project" "$status" "$containers" "$volumes" "$networks" >"$evidence/cleanup.json"
  trap - EXIT
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
started=1
"${compose[@]}" up -d --wait --wait-timeout 60 postgres rustfs
"${compose[@]}" run --rm --no-deps rustfs-ready
"${compose[@]}" run --rm --no-deps migrate >"$evidence/migrate-first.log" 2>&1
"${compose[@]}" run --rm --no-deps migrate >"$evidence/migrate-second.log" 2>&1
postgres_address=$("${compose[@]}" port postgres 5432)
rustfs_address=$("${compose[@]}" port rustfs 9000)
export TEST_DATABASE_URL="postgres://gateway:integration-password@${postgres_address}/gateway_test?sslmode=disable"
export TEST_RUSTFS_ENDPOINT="http://${rustfs_address}"
export TEST_RUSTFS_ACCESS_KEY=integration-rustfs
export TEST_RUSTFS_SECRET_KEY=integration-password
result=0
go test -json -race -count=1 -p 1 -timeout 8m -tags=integration ./internal/nexusmigration/pgstore \
  -run '^TestPostgresMigrationInventory' >"$evidence/results.jsonl" 2>&1 || result=$?
python3 - "$evidence/results.jsonl" "$evidence/results.json" "$result" <<'PY'
import hashlib, json, pathlib, sys
path, destination = map(pathlib.Path, sys.argv[1:3])
required = {"TestPostgresMigrationInventoryLargeResumeMatchesUninterruptedManifest",
            "TestPostgresMigrationInventoryCheckpointAckWindowsAndVerificationResume",
            "TestPostgresMigrationInventoryPauseCancelFencePageAndSeal",
            "TestPostgresMigrationInventoryLeaseTakeoverAndStaleWriter",
            "TestPostgresMigrationInventoryRejectsDriftDuplicateCycleAndIncompletePages",
            "TestPostgresMigrationInventoryCheckpointBindsSourceShapeMappingAndPolicy",
            "TestPostgresMigrationInventoryBackendLossRollsBackWholePage",
            "TestPostgresMigrationInventoryLeaseExpiryInsideTransactionRollsBackPage",
            "TestPostgresMigrationInventoryPageProgressPreservesPublicControlVersion"}
events = [json.loads(line) for line in path.read_text().splitlines()]
run = {event.get("Test") for event in events if event.get("Action") == "run"}
passed = {event.get("Test") for event in events if event.get("Action") == "pass"}
failed = {event.get("Test") or event.get("Package") for event in events if event.get("Action") == "fail"}
skipped = {event.get("Test") for event in events if event.get("Action") == "skip"}
packages = {event.get("Package") for event in events if event.get("Action") == "pass" and not event.get("Test")}
summary = {"required": sorted(required), "passedRequired": sorted(required & passed),
           "missingRun": sorted(required - run), "missingPass": sorted(required - passed),
           "failed": sorted(failed), "skipped": sorted(skipped), "packages": sorted(packages),
           "exitCode": int(sys.argv[3]), "logSHA256": hashlib.sha256(path.read_bytes()).hexdigest()}
destination.write_text(json.dumps(summary, indent=2) + "\n")
for event in events:
    if event.get("Output"):
        print(event["Output"], end="")
if int(sys.argv[3]) or required - run or required - passed or failed or skipped or packages != {"github.com/ccsert/artifact-gateway/internal/nexusmigration/pgstore"}:
    raise SystemExit("mandatory inventory scan proof is missing, skipped or failed; see results.json")
print("Verified all 9 required inventory scan fixtures: 101 pages / 10002 assets, PG recovery, receipts and fencing; no existing source migration.")
PY
