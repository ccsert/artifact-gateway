#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
version=
revision=
output=
skip_console=0
targets=()

usage() {
  printf '%s\n' \
    'Usage: build-release-artifacts.sh --version VERSION --revision SHA --output DIR [--target GOOS/GOARCH] [--skip-console]'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      version=${2:-}
      shift 2
      ;;
    --revision)
      revision=${2:-}
      shift 2
      ;;
    --output)
      output=${2:-}
      shift 2
      ;;
    --target)
      targets+=("${2:-}")
      shift 2
      ;;
    --skip-console)
      skip_console=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'Unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z][0-9A-Za-z.-]*)?$ ]]; then
  printf 'Version must be SemVer without a v prefix: %s\n' "$version" >&2
  exit 2
fi
if [[ ! "$revision" =~ ^[0-9a-f]{7,64}$ ]]; then
  printf 'Revision must be a lowercase hexadecimal Git SHA: %s\n' "$revision" >&2
  exit 2
fi
if [[ -z "$output" ]]; then
  printf '%s\n' 'Output directory is required.' >&2
  exit 2
fi
if [[ -e "$output" && -n "$(find "$output" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  printf 'Output directory must be empty: %s\n' "$output" >&2
  exit 2
fi
mkdir -p "$output"
output=$(cd "$output" && pwd)
cd "$root"

normalize_stage() {
  find "$1" -exec touch -h -t 200001010000.00 {} +
}

create_tar_gz() {
  local source=$1
  local destination=$2
  normalize_stage "$source"
  (
    cd "$source"
    find . -print | LC_ALL=C sort | COPYFILE_DISABLE=1 tar \
      --no-recursion \
      --format ustar \
      --uid 0 \
      --gid 0 \
      --uname root \
      --gname root \
      -cf - \
      -T - | gzip -n > "$destination"
  )
}

create_zip() {
  local source=$1
  local destination=$2
  normalize_stage "$source"
  (
    cd "$source"
    find . -type f -print | LC_ALL=C sort | zip -q -X "$destination" -@
  )
}

if [[ ${#targets[@]} -eq 0 ]]; then
  targets=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64)
fi

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
ldflags="-s -w -X github.com/artifact-gateway/artifact-gateway/internal/buildinfo.injectedVersion=$version -X github.com/artifact-gateway/artifact-gateway/internal/buildinfo.injectedRevision=$revision"

for target in "${targets[@]}"; do
  if [[ ! "$target" =~ ^(linux|darwin|windows)/(amd64|arm64)$ ]]; then
    printf 'Unsupported release target: %s\n' "$target" >&2
    exit 2
  fi
  goos=${target%/*}
  goarch=${target#*/}
  stage="$workdir/artifact-gateway_${version}_${goos}_${goarch}"
  mkdir -p "$stage"
  suffix=
  if [[ "$goos" == "windows" ]]; then
    suffix=.exe
  fi
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$stage/gateway$suffix" ./cmd/gateway
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$stage/gateway-healthcheck$suffix" ./cmd/healthcheck
  cp -R "$root/migrations" "$stage/migrations"
  cp "$root/scripts/run-migrations.sh" "$stage/run-migrations.sh"
  cp "$root/.env.example" "$stage/artifact-gateway.env.example"
  chmod +x "$stage/run-migrations.sh"
  printf 'version=%s\nrevision=%s\ntarget=%s/%s\n' "$version" "$revision" "$goos" "$goarch" > "$stage/VERSION.txt"
  printf '%s\n' \
    'Artifact Gateway installation materials' \
    '' \
    '1. Verify SHA256SUMS before extracting this archive.' \
    '2. Configure PostgreSQL and S3-compatible object storage using artifact-gateway.env.example.' \
    '3. Install PostgreSQL psql, export PGHOST/PGUSER/PGDATABASE/PGPASSWORD, then run:' \
    '   MIGRATION_DIR=./migrations ./run-migrations.sh' \
    '4. Export the required GATEWAY_* variables and start ./gateway.' \
    '' \
    "Documentation: https://github.com/ccsert/artifact-gateway/blob/$revision/docs/getting-started.md" \
    > "$stage/INSTALL.txt"

  if [[ "$goos" == "windows" ]]; then
    archive="$output/artifact-gateway_${version}_${goos}_${goarch}.zip"
    create_zip "$stage" "$archive"
  else
    archive="$output/artifact-gateway_${version}_${goos}_${goarch}.tar.gz"
    create_tar_gz "$stage" "$archive"
  fi
done

if [[ "$skip_console" -eq 0 ]]; then
  npm --prefix "$root/console" ci --ignore-scripts --no-audit --no-fund
  npm --prefix "$root/console" run build
  console_stage="$workdir/artifact-gateway-console_${version}_web"
  mkdir -p "$console_stage"
  cp -R "$root/console/dist/." "$console_stage/"
  printf 'version=%s\nrevision=%s\n' "$version" "$revision" > "$console_stage/VERSION.txt"
  create_tar_gz "$console_stage" "$output/artifact-gateway-console_${version}_web.tar.gz"

  openapi_stage="$workdir/artifact-gateway-openapi_${version}"
  mkdir -p "$openapi_stage"
  cp "$root/api/openapi/native-hosted-v1.json" "$openapi_stage/"
  cp "$root/api/openapi/management-runtime-v1.json" "$openapi_stage/"
  printf 'version=%s\nrevision=%s\n' "$version" "$revision" > "$openapi_stage/VERSION.txt"
  create_tar_gz "$openapi_stage" "$output/artifact-gateway-openapi_${version}.tar.gz"
fi

(
  cd "$output"
  : > SHA256SUMS
  for artifact in artifact-gateway_*; do
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum "$artifact" >> SHA256SUMS
    else
      shasum -a 256 "$artifact" >> SHA256SUMS
    fi
  done
)

printf 'Built Artifact Gateway %s release artifacts in %s.\n' "$version" "$output"
