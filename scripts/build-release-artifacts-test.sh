#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

version=0.1.0
revision=0123456789abcdef0123456789abcdef01234567
goos=$(go env GOOS)
goarch=$(go env GOARCH)

if bash "$root/scripts/build-release-artifacts.sh" \
  --version "v$version" \
  --revision "$revision" \
  --output "$workdir/invalid" \
  --target "$goos/$goarch" \
  --skip-console >/dev/null 2>&1; then
  printf '%s\n' 'release builder accepted a version with a v prefix' >&2
  exit 1
fi

(
  cd "$workdir"
  bash "$root/scripts/build-release-artifacts.sh" \
    --version "$version" \
    --revision "$revision" \
    --output dist \
    --target "$goos/$goarch" \
    --skip-console
)

archive="$workdir/dist/artifact-gateway_${version}_${goos}_${goarch}.tar.gz"
if [[ "$goos" == "windows" ]]; then
  archive="$workdir/dist/artifact-gateway_${version}_${goos}_${goarch}.zip"
fi
test -f "$archive"
test -f "$workdir/dist/SHA256SUMS"

mkdir -p "$workdir/checksum-fixtures"
cp "$archive" "$workdir/checksum-fixtures/${archive##*/}"
cp "$archive" "$workdir/checksum-fixtures/artifact-gateway-console_${version}_web.tar.gz"
cp "$archive" "$workdir/checksum-fixtures/artifact-gateway-openapi_${version}.tar.gz"
printf '%s\n' 'not a release archive' > "$workdir/checksum-fixtures/ignored.txt"
bash "$root/scripts/write-release-checksums.sh" "$workdir/checksum-fixtures"
test "$(wc -l < "$workdir/checksum-fixtures/SHA256SUMS" | tr -d '[:space:]')" = 3
grep -F "artifact-gateway_${version}_${goos}_${goarch}" "$workdir/checksum-fixtures/SHA256SUMS" >/dev/null
grep -F "artifact-gateway-console_${version}_web.tar.gz" "$workdir/checksum-fixtures/SHA256SUMS" >/dev/null
grep -F "artifact-gateway-openapi_${version}.tar.gz" "$workdir/checksum-fixtures/SHA256SUMS" >/dev/null
if grep -F 'ignored.txt' "$workdir/checksum-fixtures/SHA256SUMS" >/dev/null; then
  printf '%s\n' 'release checksum manifest included a non-archive file' >&2
  exit 1
fi

mkdir -p "$workdir/unpacked"
if [[ "$archive" == *.zip ]]; then
  unzip -q "$archive" -d "$workdir/unpacked"
  gateway="$workdir/unpacked/gateway.exe"
  healthcheck="$workdir/unpacked/gateway-healthcheck.exe"
else
  tar -xzf "$archive" -C "$workdir/unpacked"
  gateway="$workdir/unpacked/gateway"
  healthcheck="$workdir/unpacked/gateway-healthcheck"
fi
test -x "$gateway"
test -x "$healthcheck"
test -x "$workdir/unpacked/run-migrations.sh"
test -f "$workdir/unpacked/migrations/000001_initial.sql"
test -f "$workdir/unpacked/artifact-gateway.env.example"
test -f "$workdir/unpacked/INSTALL.txt"

version_output=$("$gateway" version)
printf '%s\n' "$version_output" | grep -F "artifact-gateway $version" >/dev/null
printf '%s\n' "$version_output" | grep -F "revision $revision" >/dev/null
test "$("$gateway" --version)" = "$version_output"
healthcheck_version_output=$("$healthcheck" version)
printf '%s\n' "$healthcheck_version_output" | grep -F "artifact-gateway-healthcheck $version" >/dev/null
printf '%s\n' "$healthcheck_version_output" | grep -F "revision $revision" >/dev/null
test "$("$healthcheck" --version)" = "$healthcheck_version_output"

(
  cd "$workdir/dist"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c SHA256SUMS
  else
    shasum -a 256 -c SHA256SUMS
  fi
)

(
  cd "$workdir"
  bash "$root/scripts/build-release-artifacts.sh" \
    --version "$version" \
    --revision "$revision" \
    --output dist-repeat \
    --target "$goos/$goarch" \
    --skip-console
)
repeat_archive="$workdir/dist-repeat/${archive##*/}"
cmp "$archive" "$repeat_archive"
cmp "$workdir/dist/SHA256SUMS" "$workdir/dist-repeat/SHA256SUMS"

printf 'Release artifact test passed for %s/%s.\n' "$goos" "$goarch"
