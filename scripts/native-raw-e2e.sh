#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd); cd "$repo_root"
for binary in curl go python3; do command -v "$binary" >/dev/null || { printf 'Native Raw E2E requires %s\n' "$binary" >&2; exit 1; }; done
curl --version | head -1
python3 - "$(command -v curl)" <<'PY'
import hashlib, pathlib, sys
print('curl executable SHA-256:', hashlib.sha256(pathlib.Path(sys.argv[1]).read_bytes()).hexdigest())
PY
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
base="http://127.0.0.1:${port}"; workdir=$(mktemp -d); pid=""
cleanup() { local status=$?; [[ -z "$pid" ]] || { kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; }; rm -rf "$workdir"; trap - EXIT; exit "$status"; }; trap cleanup EXIT
go build -o "$workdir/native-raw-fixture" ./cmd/native-raw-fixture
LISTEN_ADDR="127.0.0.1:${port}" "$workdir/native-raw-fixture" >"$workdir/gateway.log" 2>&1 & pid=$!
until curl --silent --show-error --fail "$base/livez" >/dev/null; do kill -0 "$pid" 2>/dev/null || { cat "$workdir/gateway.log" >&2; exit 1; }; sleep 0.1; done
token=$(curl --silent --show-error --fail --user fixture:fixture-secret "$base/auth/token" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'); test -n "$token"
auth=(-H "Authorization: Bearer $token")
status() { curl --silent --show-error --output "$workdir/response" --write-out '%{http_code}' "$@"; }
expect() { local want=$1; shift; local got; got=$(status "$@"); [[ "$got" == "$want" ]] || { printf 'Expected HTTP %s, got %s\n' "$want" "$got" >&2; cat "$workdir/response" >&2; exit 1; }; }
path="$base/raw/downloads/releases/app.txt"
expect 201 "${auth[@]}" -H 'Content-Type: text/plain' -X PUT --data-binary 'native raw artifact' "$path"
expect 206 "${auth[@]}" -H 'Range: bytes=7-9' "$path"; [[ $(cat "$workdir/response") == raw ]] || exit 1
expect 200 "${auth[@]}" -I "$path"
# A large fixed-byte fixture exercises bounded multipart responses with the
# real curl executable. No client cache or upstream is involved.
python3 - "$workdir/large.bin" <<'PY'
import pathlib, sys
with pathlib.Path(sys.argv[1]).open('wb') as f:
    for _ in range(512):
        f.write(bytes(range(256)) * 256)
PY
large="$base/raw/downloads/releases/large.bin"
expect 201 "${auth[@]}" -H 'Content-Type: application/octet-stream' -X PUT --data-binary "@$workdir/large.bin" "$large"
expect 200 "${auth[@]}" "$large"
cmp "$workdir/large.bin" "$workdir/response"
curl --silent --show-error --fail --head "${auth[@]}" "$large" >"$workdir/large.headers"
validator=$(tr -d '\r' <"$workdir/large.headers" | sed -n 's/^[Ee][Tt][Aa][Gg]: //p'); test -n "$validator"
expect 206 "${auth[@]}" -H 'Range: bytes=0-65535,16777216-16842751,-65536' -H "If-Range: $validator" --dump-header "$workdir/ranges.headers" "$large"
python3 - "$workdir" <<'PY'
import base64, email.parser, email.policy, hashlib, pathlib, sys
root = pathlib.Path(sys.argv[1])
headers = root.joinpath('ranges.headers').read_bytes().split(b'\r\n', 1)[1]
body = root.joinpath('response').read_bytes()
message = email.parser.BytesParser(policy=email.policy.default).parsebytes(headers + body)
source = root.joinpath('large.bin').read_bytes()
assert len(source) == 32 * 1024 * 1024
assert message.get_content_type() == 'multipart/byteranges'
assert message['Content-Range'] is None
assert int(message['Content-Length']) == len(body)
assert message['Digest'] == 'sha-256=' + base64.b64encode(hashlib.sha256(source).digest()).decode()
parts = list(message.iter_parts())
intervals = [(0, 65535), (16777216, 16842751), (len(source)-65536, len(source)-1)]
assert len(parts) == len(intervals)
for part, (start, end) in zip(parts, intervals):
    assert part['Content-Range'] == f'bytes {start}-{end}/{len(source)}'
    data = part.get_payload(decode=True)
    assert data == source[start:end+1]
    assert hashlib.sha256(data).digest() == hashlib.sha256(source[start:end+1]).digest()
print('Verified 32 MiB Raw object and all three multipart byte identities')
PY
expect 200 "${auth[@]}" -H 'Range: bytes=0-1,8-9' -H 'If-Range: "stale"' "$large"
cmp "$workdir/large.bin" "$workdir/response"
expect 200 "${auth[@]}" -I -H 'Range: bytes=0-1,8-9' "$large"
expect 416 "${auth[@]}" -H 'Range: bytes=9223372036854775808-' "$large"
expect 401 -H 'Range: bytes=0-1,8-9' "$large"
# Conditional writes: create-only, If-Match on the read ETag, one winner among
# writers holding the same validator, and a guarded resumable completion.
etag() { curl --silent --show-error --fail --head "${auth[@]}" "$1" | tr -d '\r' | sed -n 's/^[Ee][Tt][Aa][Gg]: //p'; }
created="$base/raw/downloads/releases/create-only.txt"
expect 201 "${auth[@]}" -H 'If-None-Match: *' -X PUT --data-binary 'first' "$created"
expect 412 "${auth[@]}" -H 'If-None-Match: *' -X PUT --data-binary 'second' "$created"
expect 200 "${auth[@]}" "$created"; [[ $(cat "$workdir/response") == first ]] || exit 1
validator=$(etag "$path"); test -n "$validator"
expect 412 "${auth[@]}" -H 'If-Match: "sha256-stale"' -X PUT --data-binary 'stale' "$path"
writers=()
for writer in a b; do
  curl --silent --output /dev/null --write-out '%{http_code}\n' "${auth[@]}" -H "If-Match: $validator" -X PUT --data-binary "writer-$writer" "$path" >"$workdir/writer-$writer" & writers+=($!)
done
wait "${writers[@]}"
codes=$(sort "$workdir"/writer-* | tr '\n' ' ')
[[ "$codes" == "201 412 " ]] || { printf 'Expected one 201 and one 412 from concurrent writers, got %s\n' "$codes" >&2; exit 1; }
current=$(etag "$path")
location=$(curl --silent --show-error --fail --dump-header - --output /dev/null "${auth[@]}" -X POST "$path?resumable=1" | tr -d '\r' | sed -n 's/^[Ll]ocation: //p'); test -n "$location"
expect 204 "${auth[@]}" -H 'Upload-Offset: 0' -X PATCH --data-binary 'resumed' "$base$location"
digest="sha-256=$(printf resumed | python3 -c 'import base64,hashlib,sys; print(base64.b64encode(hashlib.sha256(sys.stdin.buffer.read()).digest()).decode())')"
expect 412 "${auth[@]}" -H "Digest: $digest" -H "If-Match: $validator" -X PUT "$base$location&complete=1"
expect 201 "${auth[@]}" -H "Digest: $digest" -H "If-Match: $current" -X PUT "$base$location&complete=1"
expect 200 "${auth[@]}" "$path"; [[ $(cat "$workdir/response") == resumed ]] || exit 1
expect 204 "${auth[@]}" -X DELETE "$path"
expect 404 "${auth[@]}" "$path"
printf 'Native Raw Hosted E2E passed through native Raw repository root at %s\n' "$base"
