#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

for binary in curl go node npm python3; do
  command -v "$binary" >/dev/null || {
    printf 'Native npm E2E requires %s\n' "$binary" >&2
    exit 1
  }
done

port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
gateway_url="http://127.0.0.1:${port}"
registry_url="${gateway_url}/npm/packages/"
workdir=$(mktemp -d)
fixture_pid=""

cleanup() {
  local status=$?
  if [[ -n "$fixture_pid" ]]; then
    kill "$fixture_pid" 2>/dev/null || true
    wait "$fixture_pid" 2>/dev/null || true
  fi
  rm -rf "$workdir"
  trap - EXIT
  exit "$status"
}
trap cleanup EXIT

go build -o "$workdir/native-npm-fixture" ./cmd/native-npm-fixture
LISTEN_ADDR="127.0.0.1:${port}" "$workdir/native-npm-fixture" >"$workdir/gateway.log" 2>&1 &
fixture_pid=$!
until curl --silent --show-error --fail "$gateway_url/livez" >/dev/null; do
  if ! kill -0 "$fixture_pid" 2>/dev/null; then
    sed -n '1,200p' "$workdir/gateway.log" >&2
    exit 1
  fi
  sleep 0.1
done

auth_config="$workdir/auth.npmrc"
anonymous_config="$workdir/anonymous.npmrc"
touch "$anonymous_config"
npm_config_userconfig="$auth_config" npm config set "//127.0.0.1:${port}/npm/packages/:_authToken" fixture-secret

unscoped="$workdir/unscoped"
mkdir "$unscoped"
(
  cd "$unscoped"
  npm init -y >/dev/null
  npm pkg set name='ag-npm-fixture' version='1.0.0' description='Artifact Gateway npm fixture' license='MIT'
  npm_config_userconfig="$auth_config" npm publish --registry="$registry_url" --tag=latest
  npm version 1.1.0 --no-git-tag-version >/dev/null
  npm_config_userconfig="$auth_config" npm publish --registry="$registry_url" --tag=next
)

scoped="$workdir/scoped"
mkdir "$scoped"
(
  cd "$scoped"
  npm init -y >/dev/null
  npm pkg set name='@artifact-gateway/npm-fixture' version='2.0.0' description='Scoped Artifact Gateway npm fixture' license='Apache-2.0'
  npm_config_userconfig="$auth_config" npm publish --registry="$registry_url" --tag=latest --access=public
)

tags=$(npm_config_userconfig="$anonymous_config" npm view ag-npm-fixture dist-tags --json --registry="$registry_url")
node -e 'const tags=JSON.parse(process.argv[1]); if(tags.latest!=="1.0.0" || tags.next!=="1.1.0") process.exit(1)' "$tags"

install_dir="$workdir/install"
mkdir "$install_dir"
(
  cd "$install_dir"
  npm init -y >/dev/null
  npm_config_userconfig="$anonymous_config" npm install 'ag-npm-fixture@1.0.0' '@artifact-gateway/npm-fixture@2.0.0' --registry="$registry_url" --ignore-scripts
  node -e 'for (const [name,version] of Object.entries({"ag-npm-fixture":"1.0.0","@artifact-gateway/npm-fixture":"2.0.0"})) { const actual=require(`./node_modules/${name}/package.json`).version; if(actual!==version) process.exit(1) }'
)

if (
  cd "$unscoped"
  npm_config_userconfig="$auth_config" npm publish --registry="$registry_url" --tag=next >"$workdir/duplicate.log" 2>&1
); then
  printf 'npm allowed an immutable version to be overwritten\n' >&2
  exit 1
fi
grep -q 'previously published versions' "$workdir/duplicate.log"

printf 'Native npm publish/install E2E passed through %s\n' "$gateway_url"
