#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

for binary in ab awk cmp curl dd docker go gzip jq openssl python3 rg sort wc; do
  if ! command -v "$binary" >/dev/null 2>&1; then
    printf 'Performance baseline requires %s.\n' "$binary" >&2
    exit 1
  fi
done

if ! docker compose version >/dev/null 2>&1; then
  printf '%s\n' 'Performance baseline requires Docker Compose v2.' >&2
  exit 1
fi

read -r -a generated_ports <<<"$(python3 - <<'PY'
import socket

sockets = []
try:
    for _ in range(4):
        sock = socket.socket()
        sock.bind(("127.0.0.1", 0))
        sockets.append(sock)
    print(" ".join(str(sock.getsockname()[1]) for sock in sockets))
finally:
    for sock in sockets:
        sock.close()
PY
)"
if [[ ${#generated_ports[@]} != 4 ]]; then
  printf '%s\n' 'Could not allocate four loopback ports for the performance stack.' >&2
  exit 1
fi

export GATEWAY_HTTP_PORT=${GATEWAY_PERF_GATEWAY_PORT:-${generated_ports[0]}}
export GATEWAY_POSTGRES_PORT=${GATEWAY_PERF_POSTGRES_PORT:-${generated_ports[1]}}
export RUSTFS_API_PORT=${GATEWAY_PERF_RUSTFS_PORT:-${generated_ports[2]}}
export RUSTFS_CONSOLE_PORT=${GATEWAY_PERF_RUSTFS_CONSOLE_PORT:-${generated_ports[3]}}

ports=("$GATEWAY_HTTP_PORT" "$GATEWAY_POSTGRES_PORT" "$RUSTFS_API_PORT" "$RUSTFS_CONSOLE_PORT")
if [[ $(printf '%s\n' "${ports[@]}" | sort -u | wc -l | tr -d ' ') != 4 ]]; then
  printf '%s\n' 'Performance baseline ports must be distinct.' >&2
  exit 1
fi

project=${GATEWAY_PERF_PROJECT:-artifact-gateway-perf-$(date -u +%Y%m%d%H%M%S)-$$}
if [[ ! "$project" =~ ^artifact-gateway-perf-[a-z0-9-]+$ ]]; then
  printf 'GATEWAY_PERF_PROJECT must start with artifact-gateway-perf-: %s\n' "$project" >&2
  exit 1
fi

if [[ -n ${GATEWAY_PERF_OUTPUT_DIR:-} ]]; then
  output_dir=$GATEWAY_PERF_OUTPUT_DIR
  mkdir -p "$output_dir"
else
  output_dir=$(mktemp -d "${TMPDIR:-/tmp}/artifact-gateway-performance.XXXXXX")
fi
output_dir=$(cd "$output_dir" && pwd)
private_dir="$output_dir/.private"
mkdir -p "$private_dir"
chmod 700 "$private_dir"
env_file="$private_dir/performance.env"
stack_started=0

compose=(docker compose -p "$project" --env-file "$env_file" -f compose.yml)

cleanup() {
  local exit_code=$?
  set +e
  if [[ $stack_started == 1 ]]; then
    "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1
  fi
  case "$private_dir" in
    "$output_dir"/.private) rm -rf "$private_dir" ;;
  esac
  trap - EXIT
  if [[ $exit_code == 0 ]]; then
    printf 'Performance baseline results: %s\n' "$output_dir"
  else
    printf 'Performance baseline failed; partial results: %s\n' "$output_dir" >&2
  fi
  exit "$exit_code"
}
trap cleanup EXIT

random_hex() {
  openssl rand -hex "$1"
}

admin_token="perf-admin-$(random_hex 24)"
umask 077
{
  printf 'GATEWAY_POSTGRES_PASSWORD=%s\n' "$(random_hex 24)"
  printf 'GATEWAY_ADMIN_TOKEN=%s\n' "$admin_token"
  printf 'GATEWAY_RESOLVER_TOKEN=%s\n' "perf-resolver-$(random_hex 24)"
  printf 'RUSTFS_ACCESS_KEY=%s\n' "perfaccess$(random_hex 8)"
  printf 'RUSTFS_SECRET_KEY=%s\n' "$(random_hex 24)"
  printf 'RUSTFS_RPC_SECRET=%s\n' "$(random_hex 24)"
} >"$env_file"

metadata_low_requests=${GATEWAY_PERF_METADATA_LOW_REQUESTS:-5000}
metadata_mid_requests=${GATEWAY_PERF_METADATA_MID_REQUESTS:-30000}
metadata_high_requests=${GATEWAY_PERF_METADATA_HIGH_REQUESTS:-300000}
raw_low_requests=${GATEWAY_PERF_RAW_LOW_REQUESTS:-2000}
raw_mid_requests=${GATEWAY_PERF_RAW_MID_REQUESTS:-20000}
raw_high_requests=${GATEWAY_PERF_RAW_HIGH_REQUESTS:-40000}
low_concurrency=${GATEWAY_PERF_LOW_CONCURRENCY:-1}
mid_concurrency=${GATEWAY_PERF_MID_CONCURRENCY:-32}
high_concurrency=${GATEWAY_PERF_HIGH_CONCURRENCY:-128}
idle_samples=${GATEWAY_PERF_IDLE_SAMPLES:-10}
sample_interval=${GATEWAY_PERF_SAMPLE_INTERVAL_SECONDS:-1}

for count in \
  "$metadata_low_requests" "$metadata_mid_requests" "$metadata_high_requests" \
  "$raw_low_requests" "$raw_mid_requests" "$raw_high_requests" \
  "$low_concurrency" "$mid_concurrency" "$high_concurrency" "$idle_samples"; do
  if [[ ! "$count" =~ ^[1-9][0-9]*$ ]]; then
    printf 'Performance counts must be positive integers: %s\n' "$count" >&2
    exit 1
  fi
done
if (( metadata_low_requests < low_concurrency || metadata_mid_requests < mid_concurrency || metadata_high_requests < high_concurrency || raw_low_requests < low_concurrency || raw_mid_requests < mid_concurrency || raw_high_requests < high_concurrency )); then
  printf '%s\n' 'Each request count must be greater than or equal to its concurrency.' >&2
  exit 1
fi

bytes_to_mib() {
  awk -v bytes="$1" 'BEGIN { printf "%.2f", bytes / 1048576 }'
}

memory_to_mib() {
  awk -v raw="$1" 'BEGIN {
    value = raw + 0
    if (raw ~ /GiB$/) value *= 1024
    else if (raw ~ /KiB$/) value /= 1024
    else if (raw ~ /GB$/) value *= 953.674316
    else if (raw ~ /MB$/) value *= 0.953674316
    else if (raw ~ /kB$/) value *= 0.000953674316
    else if (raw ~ /B$/ && raw !~ /iB$/) value /= 1048576
    printf "%.2f", value
  }'
}

expect_http() {
  local expected=$1 method=$2 url=$3 output=$4
  shift 4
  local http_code
  http_code=$(curl --noproxy '*' --silent --show-error --output "$output" --write-out '%{http_code}' \
    --request "$method" "$@" "$url")
  if [[ "$http_code" != "$expected" ]]; then
    printf '%s %s returned HTTP %s; expected %s.\n' "$method" "$url" "$http_code" "$expected" >&2
    sed -n '1,20p' "$output" >&2
    exit 1
  fi
}

printf '%s\n' 'Building and starting the isolated Gateway/PostgreSQL/RustFS core stack...'
stack_started=1
"${compose[@]}" up -d --build --wait --remove-orphans

gateway_id=$("${compose[@]}" ps -q gateway)
postgres_id=$("${compose[@]}" ps -q postgres)
rustfs_id=$("${compose[@]}" ps -q rustfs)
for container_id in "$gateway_id" "$postgres_id" "$rustfs_id"; do
  if [[ -z "$container_id" ]]; then
    printf '%s\n' 'Core stack did not expose all expected containers.' >&2
    exit 1
  fi
done

gateway_url="http://127.0.0.1:${GATEWAY_HTTP_PORT}"
expect_http 204 GET "$gateway_url/livez" "$private_dir/livez.out"
expect_http 204 GET "$gateway_url/readyz" "$private_dir/readyz.out"

{
  printf 'captured_at_utc=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'git_commit=%s\n' "$(git rev-parse HEAD)"
  printf 'host_uname=%s\n' "$(uname -a)"
  printf 'go=%s\n' "$(go version)"
  printf 'docker=%s\n' "$(docker version --format 'client={{.Client.Version}} server={{.Server.Version}}')"
  docker info --format 'docker_server_arch={{.Architecture}} docker_server_cpus={{.NCPU}} docker_server_memory_bytes={{.MemTotal}}'
  printf 'compose_project=%s\n' "$project"
  printf 'core_services=gateway,postgres,rustfs\n'
} >"$output_dir/environment.txt"

printf '%s\n' 'Measuring stripped Linux binaries and the runtime image...'
printf 'component,os,arch,form,bytes,mib\n' >"$output_dir/artifact-sizes.csv"
for arch in arm64 amd64; do
  for component in gateway healthcheck; do
    package=./cmd/gateway
    [[ "$component" == healthcheck ]] && package=./cmd/healthcheck
    binary_file="$private_dir/${component}-linux-${arch}"
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags='-s -w' -o "$binary_file" "$package"
    binary_bytes=$(wc -c <"$binary_file" | tr -d ' ')
    gzip -9 -c "$binary_file" >"${binary_file}.gz"
    gzip_bytes=$(wc -c <"${binary_file}.gz" | tr -d ' ')
    printf '%s,linux,%s,stripped-static-binary,%s,%s\n' "$component" "$arch" "$binary_bytes" "$(bytes_to_mib "$binary_bytes")" >>"$output_dir/artifact-sizes.csv"
    printf '%s,linux,%s,gzip-9,%s,%s\n' "$component" "$arch" "$gzip_bytes" "$(bytes_to_mib "$gzip_bytes")" >>"$output_dir/artifact-sizes.csv"
  done
done

gateway_image="${project}-gateway"
image_arch=$(docker image inspect --format '{{.Architecture}}' "$gateway_image")
image_bytes=$(docker image inspect --format '{{.Size}}' "$gateway_image")
image_archive="$private_dir/gateway-image.tar.gz"
docker save "$gateway_image" | gzip -1 >"$image_archive"
archive_bytes=$(wc -c <"$image_archive" | tr -d ' ')
printf 'gateway-runtime-image,linux,%s,uncompressed-content,%s,%s\n' "$image_arch" "$image_bytes" "$(bytes_to_mib "$image_bytes")" >>"$output_dir/artifact-sizes.csv"
printf 'gateway-runtime-image,linux,%s,docker-save-gzip-1,%s,%s\n' "$image_arch" "$archive_bytes" "$(bytes_to_mib "$archive_bytes")" >>"$output_dir/artifact-sizes.csv"

capture_snapshot() {
  local output=$1 sample=$2 service container_id line usage cpu memory
  for service in gateway postgres rustfs; do
    case "$service" in
      gateway) container_id=$gateway_id ;;
      postgres) container_id=$postgres_id ;;
      rustfs) container_id=$rustfs_id ;;
    esac
    line=$(docker stats --no-stream --format '{{.MemUsage}}|{{.CPUPerc}}' "$container_id")
    usage=${line%%|*}
    cpu=${line#*|}
    memory=${usage%% / *}
    printf '%s,%s,%s,%s\n' "$sample" "$service" "$(memory_to_mib "$memory")" "${cpu%%%}" >>"$output"
  done
}

sample_fixed() {
  local output=$1 samples=$2 sample
  printf 'sample,service,memory_mib,cpu_percent\n' >"$output"
  for ((sample = 1; sample <= samples; sample++)); do
    capture_snapshot "$output" "$sample"
    if (( sample < samples )); then
      sleep "$sample_interval"
    fi
  done
}

printf '%s\n' 'Allowing the healthy stack to settle before idle sampling...'
sleep 15
sample_fixed "$output_dir/idle-stats.csv" "$idle_samples"

repository_response="$private_dir/repository.json"
expect_http 201 POST "$gateway_url/api/v2/repositories" "$repository_response" \
  -H "Authorization: Bearer $admin_token" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: performance-baseline-raw-repository' \
  --data '{"name":"perf-raw","format":"raw","type":"hosted"}'
jq -e '.id | strings | length > 0' "$repository_response" >/dev/null

payload="$private_dir/payload-64k.bin"
dd if=/dev/urandom of="$payload" bs=65536 count=1 status=none
raw_url="$gateway_url/raw/perf-raw/performance/payload-64k.bin"
expect_http 201 PUT "$raw_url" "$private_dir/upload.out" \
  -H "Authorization: Bearer $admin_token" \
  -H 'Content-Type: application/octet-stream' \
  --data-binary "@$payload"
metadata_url="$gateway_url/api/v2/repositories"
expect_http 200 GET "$metadata_url" "$private_dir/metadata-warmup.json" -H "Authorization: Bearer $admin_token"
expect_http 200 GET "$raw_url" "$private_dir/raw-warmup.bin" -H "Authorization: Bearer $admin_token"
cmp "$payload" "$private_dir/raw-warmup.bin"

printf 'workload,concurrency,requests,completed,failed,rps,p50_ms,p95_ms,p99_ms,duration_s,transfer_kib_s\n' >"$output_dir/benchmark-results.csv"

ab_value() {
  local label=$1 file=$2
  awk -v label="$label" 'index($0, label) == 1 { value=$0; sub("^[^:]*:[[:space:]]*", "", value); sub(/[[:space:]].*$/, "", value); print value; exit }' "$file"
}

ab_percentile() {
  local percentile=$1 file=$2
  awk -v percentile="${percentile}%" '$1 == percentile { print $2; exit }' "$file"
}

run_ab_case() {
  local workload=$1 concurrency=$2 requests=$3 url=$4 stats_output=${5:-}
  local slug=${workload//[^a-zA-Z0-9]/-}
  local ab_output="$private_dir/ab-${slug}-c${concurrency}.txt"
  local ab_pid sample=1 complete failed non_2xx rps p50 p95 p99 duration transfer

  printf 'Running %-10s concurrency=%-3s requests=%s...\n' "$workload" "$concurrency" "$requests"
  if [[ -n "$stats_output" ]]; then
    printf 'sample,service,memory_mib,cpu_percent\n' >"$stats_output"
    ab -q -k -n "$requests" -c "$concurrency" -H "Authorization: Bearer $admin_token" "$url" >"$ab_output" 2>&1 &
    ab_pid=$!
    while kill -0 "$ab_pid" 2>/dev/null; do
      capture_snapshot "$stats_output" "$sample"
      sample=$((sample + 1))
      sleep "$sample_interval"
    done
    if ! wait "$ab_pid"; then
      sed -n '1,160p' "$ab_output" >&2
      exit 1
    fi
  else
    if ! ab -q -k -n "$requests" -c "$concurrency" -H "Authorization: Bearer $admin_token" "$url" >"$ab_output" 2>&1; then
      sed -n '1,160p' "$ab_output" >&2
      exit 1
    fi
  fi

  complete=$(ab_value 'Complete requests:' "$ab_output")
  failed=$(ab_value 'Failed requests:' "$ab_output")
  non_2xx=$(ab_value 'Non-2xx responses:' "$ab_output")
  non_2xx=${non_2xx:-0}
  if [[ "$complete" != "$requests" || "$failed" != 0 || "$non_2xx" != 0 ]]; then
    printf '%s c=%s completed=%s failed=%s non_2xx=%s.\n' "$workload" "$concurrency" "$complete" "$failed" "$non_2xx" >&2
    exit 1
  fi
  rps=$(ab_value 'Requests per second:' "$ab_output")
  duration=$(ab_value 'Time taken for tests:' "$ab_output")
  transfer=$(ab_value 'Transfer rate:' "$ab_output")
  p50=$(ab_percentile 50 "$ab_output")
  p95=$(ab_percentile 95 "$ab_output")
  p99=$(ab_percentile 99 "$ab_output")
  printf '%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
    "$workload" "$concurrency" "$requests" "$complete" "$failed" "$rps" "$p50" "$p95" "$p99" "$duration" "$transfer" \
    >>"$output_dir/benchmark-results.csv"
}

run_ab_case metadata "$low_concurrency" "$metadata_low_requests" "$metadata_url"
run_ab_case metadata "$mid_concurrency" "$metadata_mid_requests" "$metadata_url"
run_ab_case metadata "$high_concurrency" "$metadata_high_requests" "$metadata_url" "$output_dir/load-stats-metadata-c${high_concurrency}.csv"
run_ab_case raw-64k "$low_concurrency" "$raw_low_requests" "$raw_url"
run_ab_case raw-64k "$mid_concurrency" "$raw_mid_requests" "$raw_url"
run_ab_case raw-64k "$high_concurrency" "$raw_high_requests" "$raw_url" "$output_dir/load-stats-raw-64k-c${high_concurrency}.csv"

printf 'phase,service,min_memory_mib,avg_memory_mib,max_memory_mib,max_cpu_percent\n' >"$output_dir/resource-summary.csv"
summarize_service() {
  local phase=$1 file=$2 service=$3
  awk -F, -v phase="$phase" -v service="$service" '
    NR > 1 && $2 == service {
      count++
      memory = $3 + 0
      cpu = $4 + 0
      sum += memory
      if (count == 1 || memory < min) min = memory
      if (count == 1 || memory > max) max = memory
      if (count == 1 || cpu > max_cpu) max_cpu = cpu
    }
    END {
      if (count > 0) printf "%s,%s,%.2f,%.2f,%.2f,%.2f\n", phase, service, min, sum / count, max, max_cpu
    }
  ' "$file" >>"$output_dir/resource-summary.csv"
}

for service in gateway postgres rustfs; do
  summarize_service idle "$output_dir/idle-stats.csv" "$service"
  summarize_service "metadata-c${high_concurrency}" "$output_dir/load-stats-metadata-c${high_concurrency}.csv" "$service"
  summarize_service "raw-64k-c${high_concurrency}" "$output_dir/load-stats-raw-64k-c${high_concurrency}.csv" "$service"
done

expect_http 204 GET "$gateway_url/livez" "$private_dir/livez-after-load.out"
expect_http 204 GET "$gateway_url/readyz" "$private_dir/readyz-after-load.out"
gateway_logs="$private_dir/gateway.log"
"${compose[@]}" logs --no-color gateway >"$gateway_logs"
if rg -i 'panic|fatal' "$gateway_logs" >/dev/null 2>&1; then
  printf '%s\n' 'Gateway logs contain panic or fatal output after the benchmark.' >&2
  rg -in 'panic|fatal' "$gateway_logs" >&2
  exit 1
fi

printf '%s\n' 'Benchmark summary:'
awk -F, 'NR == 1 { next } { printf "  %-10s c=%-3s rps=%-10s p95=%sms failed=%s\n", $1, $2, $6, $8, $5 }' "$output_dir/benchmark-results.csv"
