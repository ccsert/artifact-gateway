#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd); cd "$repo_root"
for binary in curl go python3; do command -v "$binary" >/dev/null || { printf 'Native Raw E2E requires %s\n' "$binary" >&2; exit 1; }; done
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
expect 204 "${auth[@]}" -X DELETE "$path"
expect 404 "${auth[@]}" "$path"
printf 'Native Raw Hosted E2E passed through %s\n' "$base"
