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

upstream_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
proxy_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
upstream_url="http://127.0.0.1:${upstream_port}"
proxy_url="http://127.0.0.1:${proxy_port}"
registry_url="${upstream_url}/repository/packages/"
group_registry_url="${proxy_url}/repository/all-packages/"
private_registry_url="${proxy_url}/repository/private/"
workdir=$(mktemp -d)
upstream_pid=""
proxy_pid=""

cleanup() {
  local status=$?
  for pid in "$upstream_pid" "$proxy_pid"; do
    if [[ -n "$pid" ]]; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  rm -rf "$workdir"
  trap - EXIT
  exit "$status"
}
trap cleanup EXIT

go build -o "$workdir/native-npm-fixture" ./cmd/native-npm-fixture
LISTEN_ADDR="127.0.0.1:${upstream_port}" "$workdir/native-npm-fixture" >"$workdir/upstream.log" 2>&1 &
upstream_pid=$!
until curl --silent --show-error --fail "$upstream_url/livez" >/dev/null; do
  if ! kill -0 "$upstream_pid" 2>/dev/null; then
    sed -n '1,200p' "$workdir/upstream.log" >&2
    exit 1
  fi
  sleep 0.1
done

auth_config="$workdir/auth.npmrc"
anonymous_config="$workdir/anonymous.npmrc"
touch "$anonymous_config"
npm_config_userconfig="$auth_config" npm config set "//127.0.0.1:${upstream_port}/repository/packages/:_authToken" fixture-secret

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

# DefinitelyTyped tarballs such as @types/json-schema@7.0.15 use a legacy
# single root (`json-schema/package.json`) instead of npm pack's usual
# `package/package.json`. Publish that exact archive shape so the lockfile gate
# covers the real scoped-package compatibility boundary without public-network
# access during CI.
legacy_scoped_document="$workdir/legacy-scoped-publication.json"
python3 - "$legacy_scoped_document" <<'PY'
import base64
import io
import json
import sys
import tarfile

name = "@types/json-schema"
version = "7.0.15"
manifest = json.dumps({"name": name, "version": version, "license": "MIT"}, separators=(",", ":")).encode()
archive = io.BytesIO()
with tarfile.open(fileobj=archive, mode="w:gz") as tar:
    root = tarfile.TarInfo("json-schema/")
    root.type = tarfile.DIRTYPE
    root.mode = 0o755
    tar.addfile(root)
    package_json = tarfile.TarInfo("json-schema/package.json")
    package_json.mode = 0o644
    package_json.size = len(manifest)
    tar.addfile(package_json, io.BytesIO(manifest))
    declaration = b"export interface JSONSchema {}\n"
    index = tarfile.TarInfo("json-schema/index.d.ts")
    index.mode = 0o644
    index.size = len(declaration)
    tar.addfile(index, io.BytesIO(declaration))
tarball = archive.getvalue()
tarball_name = "json-schema-7.0.15.tgz"
document = {
    "_id": name,
    "name": name,
    "dist-tags": {"latest": version},
    "versions": {version: {
        "name": name,
        "version": version,
        "dist": {"tarball": "https://registry.invalid/" + tarball_name},
    }},
    "_attachments": {tarball_name: {
        "content_type": "application/octet-stream",
        "length": len(tarball),
        "data": base64.b64encode(tarball).decode(),
    }},
}
with open(sys.argv[1], "w", encoding="utf-8") as output:
    json.dump(document, output, separators=(",", ":"))
PY
curl --fail --silent --show-error \
  --request PUT \
  --header 'Authorization: Bearer fixture-secret' \
  --header 'Content-Type: application/json' \
  --data-binary "@$legacy_scoped_document" \
  "${registry_url}@types%2Fjson-schema" >/dev/null

tags=$(npm_config_userconfig="$anonymous_config" npm view ag-npm-fixture dist-tags --json --registry="$registry_url")
node -e 'const tags=JSON.parse(process.argv[1]); if(tags.latest!=="1.0.0" || tags.next!=="1.1.0") process.exit(1)' "$tags"

if (
  cd "$unscoped"
  npm_config_userconfig="$auth_config" npm publish --registry="$registry_url" --tag=next >"$workdir/duplicate.log" 2>&1
); then
  printf 'npm allowed an immutable version to be overwritten\n' >&2
  exit 1
fi
grep -q 'previously published versions' "$workdir/duplicate.log"

LISTEN_ADDR="127.0.0.1:${proxy_port}" NPM_PROXY_ENDPOINT="$registry_url" "$workdir/native-npm-fixture" >"$workdir/proxy.log" 2>&1 &
proxy_pid=$!
until curl --silent --show-error --fail "$proxy_url/livez" >/dev/null; do
  if ! kill -0 "$proxy_pid" 2>/dev/null; then
    sed -n '1,200p' "$workdir/proxy.log" >&2
    exit 1
  fi
  sleep 0.1
done

npm_config_userconfig="$auth_config" npm config set "//127.0.0.1:${proxy_port}/repository/private/:_authToken" fixture-secret

# Corepack resolves pinned package-manager releases through npm's package
# version endpoint (`/<package>/<version>`), not through the full packument.
corepack_metadata=$(curl --fail --silent --show-error "${group_registry_url}ag-npm-fixture/1.0.0")
node -e '
  const metadata=JSON.parse(process.argv[1]);
  const registry=process.argv[2];
  if (metadata.name !== "ag-npm-fixture" || metadata.version !== "1.0.0") process.exit(1);
  if (!metadata.dist?.tarball?.startsWith(registry)) process.exit(1);
' "$corepack_metadata" "$group_registry_url"

lockfile_install="$workdir/lockfile-install"
mkdir "$lockfile_install"
(
  cd "$lockfile_install"
  npm init -y >/dev/null
  npm pkg set \
    dependencies.ag-npm-fixture='1.0.0' \
    'dependencies.@artifact-gateway/npm-fixture'='2.0.0' \
    'dependencies.@types/json-schema'='7.0.15'
  npm_config_cache="$workdir/npm-cache-lock" npm_config_userconfig="$anonymous_config" npm install \
    --package-lock-only --registry="$group_registry_url" --ignore-scripts --no-audit --no-fund
  node -e '
    const lock=require("./package-lock.json");
    const registry=process.argv[1];
    for (const name of ["ag-npm-fixture", "@artifact-gateway/npm-fixture", "@types/json-schema"]) {
      const resolved=lock.packages[`node_modules/${name}`]?.resolved;
      if (!resolved?.startsWith(registry)) throw new Error(`${name} did not lock to Group: ${resolved}`);
    }
  ' "$group_registry_url"
)

# package-lock-only has resolved metadata through the Group. Restarting the
# in-memory Gateway now proves npm ci can lead with the locked tarball URL when
# the Proxy has neither packument nor tarball cache state.
kill "$proxy_pid"
wait "$proxy_pid" 2>/dev/null || true
proxy_pid=""
LISTEN_ADDR="127.0.0.1:${proxy_port}" NPM_PROXY_ENDPOINT="$registry_url" "$workdir/native-npm-fixture" >"$workdir/proxy-cold.log" 2>&1 &
proxy_pid=$!
until curl --silent --show-error --fail "$proxy_url/livez" >/dev/null; do
  if ! kill -0 "$proxy_pid" 2>/dev/null; then
    sed -n '1,200p' "$workdir/proxy-cold.log" >&2
    exit 1
  fi
  sleep 0.1
done
(
  cd "$lockfile_install"
  npm_config_cache="$workdir/npm-cache-ci-online" npm_config_userconfig="$anonymous_config" npm ci \
    --registry="$group_registry_url" --ignore-scripts --no-audit --no-fund
  node -e 'for (const [name,version] of Object.entries({"ag-npm-fixture":"1.0.0","@artifact-gateway/npm-fixture":"2.0.0","@types/json-schema":"7.0.15"})) { const actual=require(`./node_modules/${name}/package.json`).version; if(actual!==version) process.exit(1) }'
)

private_package="$workdir/private-package"
mkdir "$private_package"
(
  cd "$private_package"
  npm init -y >/dev/null
  npm pkg set name='ag-npm-private-fixture' version='3.0.0' description='Artifact Gateway Group hosted fixture' license='MIT'
  npm_config_userconfig="$auth_config" npm publish --registry="$private_registry_url" --tag=latest
)

install_dir="$workdir/install"
mkdir "$install_dir"
(
  cd "$install_dir"
  npm init -y >/dev/null
  npm_config_cache="$workdir/npm-cache-online" npm_config_userconfig="$anonymous_config" npm install 'ag-npm-private-fixture@3.0.0' 'ag-npm-fixture@1.0.0' '@artifact-gateway/npm-fixture@2.0.0' --registry="$group_registry_url" --ignore-scripts
  node -e 'for (const [name,version] of Object.entries({"ag-npm-private-fixture":"3.0.0","ag-npm-fixture":"1.0.0","@artifact-gateway/npm-fixture":"2.0.0"})) { const actual=require(`./node_modules/${name}/package.json`).version; if(actual!==version) process.exit(1) }'
)

kill "$upstream_pid"
wait "$upstream_pid" 2>/dev/null || true
upstream_pid=""

rm -rf "$lockfile_install/node_modules"
(
  cd "$lockfile_install"
  npm_config_cache="$workdir/npm-cache-ci-offline" npm_config_userconfig="$anonymous_config" npm ci \
    --registry="$group_registry_url" --ignore-scripts --no-audit --no-fund
  node -e 'for (const [name,version] of Object.entries({"ag-npm-fixture":"1.0.0","@artifact-gateway/npm-fixture":"2.0.0","@types/json-schema":"7.0.15"})) { const actual=require(`./node_modules/${name}/package.json`).version; if(actual!==version) process.exit(1) }'
)

offline_install="$workdir/offline-install"
mkdir "$offline_install"
(
  cd "$offline_install"
  npm init -y >/dev/null
  npm_config_cache="$workdir/npm-cache-offline" npm_config_userconfig="$anonymous_config" npm install 'ag-npm-private-fixture@3.0.0' 'ag-npm-fixture@1.0.0' '@artifact-gateway/npm-fixture@2.0.0' --registry="$group_registry_url" --ignore-scripts
  node -e 'for (const [name,version] of Object.entries({"ag-npm-private-fixture":"3.0.0","ag-npm-fixture":"1.0.0","@artifact-gateway/npm-fixture":"2.0.0"})) { const actual=require(`./node_modules/${name}/package.json`).version; if(actual!==version) process.exit(1) }'
)

printf 'Native npm Hosted, Proxy, Group, and cold package-lock online/offline npm ci E2E passed through Nexus-compatible repository roots at %s\n' "$proxy_url"
