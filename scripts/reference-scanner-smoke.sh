#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
compose=(docker compose --env-file "$root/.env" -f "$root/compose.yml" --profile scanner)

cleanup() {
  "${compose[@]}" rm -sf reference-scanner >/dev/null 2>&1 || true
}
trap cleanup EXIT

"${compose[@]}" config --quiet
gateway_id=$("${compose[@]}" ps -q gateway)
[[ -n "$gateway_id" && $(docker inspect --format '{{.State.Health.Status}}' "$gateway_id") == healthy ]] || {
  printf 'Start the base development stack with make dev before this smoke check.\n' >&2
  exit 1
}
"${compose[@]}" up -d --build --no-deps --wait reference-scanner
container=$("${compose[@]}" ps -q reference-scanner)

"${compose[@]}" exec -T reference-scanner /bin/sh -ec '
  test "$(id -u)" = 65532
  test -w /var/cache/trivy
  test -w /var/lib/reference-scanner/sboms
  mkdir -p /tmp/reference-input
  printf "module example.com/reference\n\ngo 1.26\n" > /tmp/reference-input/go.mod
  trivy filesystem --quiet --no-progress --scanners vuln,license --format json -- /tmp/reference-input > /tmp/report.json
  trivy convert --quiet --format cyclonedx -- /tmp/report.json > /tmp/sbom.json
  test -s /tmp/report.json
  test -s /tmp/sbom.json
  trivy version --format json >/dev/null
'

[[ $(docker inspect --format '{{.Config.User}}' "$container") == 65532:65532 ]]
[[ $(docker inspect --format '{{.HostConfig.ReadonlyRootfs}}' "$container") == true ]]
[[ $(docker inspect --format '{{.HostConfig.Memory}}' "$container") == 4294967296 ]]
[[ $(docker inspect --format '{{.HostConfig.NanoCpus}}' "$container") == 2000000000 ]]
[[ $(docker inspect --format '{{.HostConfig.PidsLimit}}' "$container") == 256 ]]
tmpfs=$(docker inspect --format '{{index .HostConfig.Tmpfs "/tmp"}}' "$container")
[[ "$tmpfs" == *size=1610612736* && "$tmpfs" == *mode=1777* ]]
[[ $(docker inspect --format '{{.HostConfig.NetworkMode}}' "$container") == container:* ]]
[[ $(docker inspect --format '{{.State.Health.Status}}' "$container") == healthy ]]
"${compose[@]}" exec -T reference-scanner /usr/local/bin/reference-scanner-healthcheck

printf 'Reference scanner image and runtime smoke checks passed.\n'
