#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

for binary in curl go python3 sha256sum; do command -v "$binary" >/dev/null || { printf 'OCI performance E2E requires %s\n' "$binary" >&2; exit 1; }; done
requests=${GATEWAY_PERFORMANCE_REQUESTS:-50}
concurrency=${GATEWAY_PERFORMANCE_CONCURRENCY:-10}
p95_limit_ms=${GATEWAY_PERFORMANCE_P95_MS:-1000}
max_error_percent=${GATEWAY_PERFORMANCE_MAX_ERROR_PERCENT:-0}

port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
gateway_url="http://127.0.0.1:${port}"
workdir=$(mktemp -d)
fixture_pid=""
cleanup() { local status=$?; [[ -z "$fixture_pid" ]] || { kill "$fixture_pid" 2>/dev/null || true; wait "$fixture_pid" 2>/dev/null || true; }; rm -rf "$workdir"; trap - EXIT; exit "$status"; }
trap cleanup EXIT

go build -o "$workdir/native-oci-fixture" ./cmd/native-oci-fixture
LISTEN_ADDR="127.0.0.1:${port}" "$workdir/native-oci-fixture" >"$workdir/gateway.log" 2>&1 &
fixture_pid=$!
until curl --silent --show-error --fail "$gateway_url/livez" >/dev/null; do kill -0 "$fixture_pid" 2>/dev/null || { cat "$workdir/gateway.log" >&2; exit 1; }; sleep 0.1; done

status() { curl --silent --show-error --output "$workdir/response" --write-out '%{http_code}' "$@"; }
expect() { local want=$1; shift; local got; got=$(status "$@"); [[ "$got" == "$want" ]] || { printf 'Expected HTTP %s, got %s: %s\n' "$want" "$got" "$*" >&2; cat "$workdir/response" >&2; exit 1; }; }
expect 200 --user fixture:fixture-secret "$gateway_url/auth/token"
token=$(sed -n 's/.*"token":"\([^"]*\)".*/\1/p' "$workdir/response")
test -n "$token"
auth=(-H "Authorization: Bearer $token")
headers="$workdir/headers"
manifest='{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}'
expect 201 "${auth[@]}" -D "$headers" -H 'Content-Type: application/vnd.oci.image.manifest.v1+json' -X PUT --data-binary "$manifest" "$gateway_url/v2/team/perf/manifests/latest"
expect 200 "${auth[@]}" "$gateway_url/v2/team/perf/manifests/latest"

python3 - "$gateway_url/v2/team/perf/manifests/latest" "$token" "$requests" "$concurrency" "$p95_limit_ms" "$max_error_percent" <<'PY'
import concurrent.futures, math, sys, time, urllib.error, urllib.request
url, token = sys.argv[1], sys.argv[2]
requests_count, concurrency = int(sys.argv[3]), int(sys.argv[4])
p95_limit_ms, max_error_percent = float(sys.argv[5]), float(sys.argv[6])

def one(_):
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    start = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=5) as response:
            body = response.read()
            ok = response.status == 200 and b'"schemaVersion":2' in body
    except (urllib.error.URLError, TimeoutError):
        ok = False
    return ok, (time.perf_counter() - start) * 1000

with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
    results = list(pool.map(one, range(requests_count)))

errors = sum(1 for ok, _ in results if not ok)
durations = sorted(ms for _, ms in results)
rank = max(0, math.ceil(0.95 * len(durations)) - 1)
p95 = durations[rank]
error_percent = errors * 100 / requests_count
print(f"OCI performance: requests={requests_count} concurrency={concurrency} errors={errors} error_percent={error_percent:.2f} p95_ms={p95:.2f}")
if error_percent > max_error_percent:
    raise SystemExit(f"error percent {error_percent:.2f} exceeds {max_error_percent:.2f}")
if p95 > p95_limit_ms:
    raise SystemExit(f"p95 {p95:.2f}ms exceeds {p95_limit_ms:.2f}ms")
PY

printf '%s\n' 'OCI performance E2E passed: cached manifest reads met error and p95 gates.'
