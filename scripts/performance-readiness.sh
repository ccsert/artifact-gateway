#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

environment_file=${GATEWAY_ENV_FILE:-.env}

if [[ ! -f "$environment_file" || ! -f .gitea-fixture/connection.env ]]; then
  printf '%s\n' 'Run requires a configured environment file and the seeded Gitea fixture.' >&2
  exit 1
fi

# shellcheck disable=SC1091
source "$environment_file"
# shellcheck disable=SC1091
source .gitea-fixture/connection.env

requests=${GATEWAY_PERFORMANCE_REQUESTS:-50}
concurrency=${GATEWAY_PERFORMANCE_CONCURRENCY:-10}
p95_limit_ms=${GATEWAY_PERFORMANCE_P95_MS:-1000}
max_error_percent=${GATEWAY_PERFORMANCE_MAX_ERROR_PERCENT:-0}

for name in GATEWAY_HTTP_PORT GATEWAY_RESOLVER_TOKEN GITEA_FIXTURE_ORG; do
  if [[ -z "${!name:-}" ]]; then
    printf 'Missing required %s\n' "$name" >&2
    exit 1
  fi
done

[[ "$requests" =~ ^[1-9][0-9]*$ ]] || { printf '%s\n' 'GATEWAY_PERFORMANCE_REQUESTS must be a positive integer.' >&2; exit 1; }
[[ "$concurrency" =~ ^[1-9][0-9]*$ ]] || { printf '%s\n' 'GATEWAY_PERFORMANCE_CONCURRENCY must be a positive integer.' >&2; exit 1; }
[[ "$p95_limit_ms" =~ ^[1-9][0-9]*$ ]] || { printf '%s\n' 'GATEWAY_PERFORMANCE_P95_MS must be a positive integer.' >&2; exit 1; }
[[ "$max_error_percent" =~ ^[0-9]+([.][0-9]+)?$ ]] || { printf '%s\n' 'GATEWAY_PERFORMANCE_MAX_ERROR_PERCENT must be numeric.' >&2; exit 1; }

gateway_url="http://localhost:${GATEWAY_HTTP_PORT}"
token=$(curl --silent --show-error --fail --user "performance-readiness:${GATEWAY_RESOLVER_TOKEN}" \
  "$gateway_url/auth/token" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
test -n "$token" || { printf '%s\n' 'Performance bearer token is empty.' >&2; exit 1; }

target="${GATEWAY_PERFORMANCE_TARGET:-$gateway_url/v2/$GITEA_FIXTURE_ORG/gateway-fixture/manifests/1.0.0}"
result_dir=$(mktemp -d)
cleanup() { rm -rf "$result_dir"; }
trap cleanup EXIT

for ((worker = 1; worker <= concurrency; worker++)); do
  (
    for ((request = worker; request <= requests; request += concurrency)); do
      curl --silent --show-error --output /dev/null --write-out '%{http_code} %{time_total}\n' \
        -H "Authorization: Bearer $token" "$target" >>"$result_dir/$worker"
    done
  ) &
done
wait

cat "$result_dir"/* >"$result_dir/results"
completed=$(wc -l <"$result_dir/results" | tr -d ' ')
[[ "$completed" -eq "$requests" ]] || { printf 'Expected %s benchmark responses, got %s.\n' "$requests" "$completed" >&2; exit 1; }

failures=$(awk '$1 != 200 { failures++ } END { print failures + 0 }' "$result_dir/results")
error_percent=$(awk -v failures="$failures" -v completed="$completed" 'BEGIN { printf "%.3f", failures * 100 / completed }')
sort -n -k2 "$result_dir/results" >"$result_dir/sorted"
p95_rank=$(( (completed * 95 + 99) / 100 ))
p95_seconds=$(awk -v rank="$p95_rank" 'NR == rank { print $2 }' "$result_dir/sorted")
p95_ms=$(awk -v seconds="$p95_seconds" 'BEGIN { printf "%.3f", seconds * 1000 }')

awk -v actual="$error_percent" -v maximum="$max_error_percent" 'BEGIN { exit !(actual <= maximum) }' || {
  printf 'OCI performance error rate %s%% exceeded %s%%.\n' "$error_percent" "$max_error_percent" >&2
  exit 1
}
awk -v actual="$p95_ms" -v maximum="$p95_limit_ms" 'BEGIN { exit !(actual <= maximum) }' || {
  printf 'OCI performance p95 %sms exceeded %sms.\n' "$p95_ms" "$p95_limit_ms" >&2
  exit 1
}

printf 'OCI performance gate passed: requests=%s concurrency=%s p95_ms=%s error_percent=%s\n' \
  "$requests" "$concurrency" "$p95_ms" "$error_percent"
