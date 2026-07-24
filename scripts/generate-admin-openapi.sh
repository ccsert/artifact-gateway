#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

redocly="tools/openapi/node_modules/.bin/redocly"
[[ -x "$redocly" ]] || { printf '%s\n' 'OpenAPI tools are missing; run make openapi-bundle first.' >&2; exit 1; }

workdir=$(mktemp -d)
cleanup() { rm -rf "$workdir"; }
trap cleanup EXIT

"$redocly" bundle api/openapi/management-runtime.yaml --output "$workdir/management-runtime.json" --ext json
go tool oapi-codegen -generate types,std-http-server,strict-server -package adminopenapi -o internal/admin/openapi/generated.go "$workdir/management-runtime.json"
