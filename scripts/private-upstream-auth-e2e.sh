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
  printf '{"project":"%s","exitCode":%s,"remainingContainers":"%s","remainingVolumes":"%s","remainingNetworks":"%s"}\n' "$project" "$result" "$remaining" "$volumes" "$networks" >"$evidence/cleanup.json"
  rm -rf "$workdir"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
printf 'Private upstream owned project=%s docker-context=%s\n' "$project" "$context"
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
export PATH="$workdir/apache-maven-3.9.16/bin:$workdir/bin:$PATH"
printf 'Pinned clients: Maven 3.9.16 (SHA-512), ORAS 1.3.3 (SHA-256)\n'
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
  go test -json -count=1 -tags='integration mavenclient ociclient' ./internal/app \
  -run '^Test(PostgresRustFSPrivateProxy(Credentials|OfficialClients)|Private.*)$' \
  >"$evidence/results.jsonl" 2>&1; then
  cat "$evidence/results.jsonl" >&2
  exit 1
fi
python3 - "$evidence/results.jsonl" "$evidence/results.json" <<'PYRESULT'
import hashlib, json, pathlib, sys
path, destination = map(pathlib.Path, sys.argv[1:])
events = [json.loads(line) for line in path.read_text().splitlines()]
required = {'TestPostgresRustFSPrivateProxyCredentials', 'TestPostgresRustFSPrivateProxyOfficialClients',
            'TestPrivateProxyUpstreamCredentials', 'TestPrivateOCIIssuerCredentials',
            'TestPrivateProxyAPIAdmitsOnlyImplementedFormats', 'TestPrivateProxyInvalidCiphertextFailsBeforeNetwork',
            'TestPrivateProxyCredentialRedirectBoundary', 'TestPrivateOCIIssuerRedirectNeverReattachesCredential',
            'TestPrivateProxyUnauthorizedDoesNotContactAnonymousGroupFallback', 'TestPrivateOCICacheRechecksEgressAndRequiresAudit',
            'TestPrivateProxyCredentialRebinding', 'TestPrivateProxyCredentialRebindingPostgres',
            'TestPrivateProxyCircuitDoesNotFallThrough', 'TestPrivateProxyCircuitDoesNotFallThroughPostgres'}
run = {e.get('Test') for e in events if e.get('Action') == 'run'}
passed = {e.get('Test') for e in events if e.get('Action') == 'pass'}
failed = {e.get('Test') or e.get('Package') for e in events if e.get('Action') == 'fail'}
skipped = {e.get('Test') for e in events if e.get('Action') == 'skip'}
summary = {'required': sorted(required), 'missingRun': sorted(required - run),
           'missingPass': sorted(required - passed), 'failed': sorted(failed),
           'skipped': sorted(skipped), 'passedCount': len(passed),
           'logSHA256': hashlib.sha256(path.read_bytes()).hexdigest()}
destination.write_text(json.dumps(summary, indent=2) + '\n')
for event in events:
    if event.get('Output'): print(event['Output'], end='')
if required - run or required - passed or failed or skipped:
    raise SystemExit('mandatory private upstream fixture missing, failed or skipped; see results.json')
print('Verified private Maven/OCI proxy and group authentication with pinned official clients, cold and hot caches, zero skipped tests.')
PYRESULT
