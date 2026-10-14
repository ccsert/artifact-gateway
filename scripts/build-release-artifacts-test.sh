#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

version=0.1.0
revision=0123456789abcdef0123456789abcdef01234567
goos=$(go env GOOS)
goarch=$(go env GOARCH)
release_targets=("$goos/$goarch")
if [[ "$goos" != linux ]]; then
  release_targets+=("linux/$goarch")
fi
targets=()
for release_target in "${release_targets[@]}"; do
  targets+=(--target "$release_target")
done

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
    "${targets[@]}" \
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

# The CLI client ships as its own archive per target with the same injected
# version, and it must stay a client-only payload: no server binaries,
# migrations, or environment template may leak into the client archive.
for release_target in "${release_targets[@]}"; do
  target_goos=${release_target%/*}
  target_goarch=${release_target#*/}
  target_extension=tar.gz
  target_suffix=
  if [[ "$target_goos" == "windows" ]]; then
    target_extension=zip
    target_suffix=.exe
  fi
  agctl_name="artifact-gateway-agctl_${version}_${target_goos}_${target_goarch}.${target_extension}"
  agctl_archive="$workdir/dist/$agctl_name"
  test -f "$agctl_archive"
  grep -F "$agctl_name" "$workdir/dist/SHA256SUMS" >/dev/null

  agctl_unpacked="$workdir/agctl-unpacked_${target_goos}_${target_goarch}"
  mkdir -p "$agctl_unpacked"
  if [[ "$target_extension" == "zip" ]]; then
    unzip -q "$agctl_archive" -d "$agctl_unpacked"
  else
    tar -xzf "$agctl_archive" -C "$agctl_unpacked"
  fi
  test -x "$agctl_unpacked/agctl$target_suffix"
  test -f "$agctl_unpacked/VERSION.txt"
  test -f "$agctl_unpacked/INSTALL.txt"
  for server_material in gateway gateway-healthcheck migrations run-migrations.sh artifact-gateway.env.example; do
    if [[ -e "$agctl_unpacked/$server_material" ]]; then
      printf 'CLI archive %s leaked server distribution material: %s\n' "$agctl_name" "$server_material" >&2
      exit 1
    fi
  done

  if [[ "$target_goos" == "$goos" && "$target_goarch" == "$goarch" ]]; then
    agctl_version_output=$("$agctl_unpacked/agctl$target_suffix" version)
    printf '%s\n' "$agctl_version_output" | grep -F "agctl $version (revision $revision," >/dev/null
  fi
done

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
    "${targets[@]}" \
    --skip-console
)
repeat_archive="$workdir/dist-repeat/${archive##*/}"
cmp "$archive" "$repeat_archive"
for release_target in "${release_targets[@]}"; do
  target_goos=${release_target%/*}
  target_goarch=${release_target#*/}
  target_extension=tar.gz
  if [[ "$target_goos" == "windows" ]]; then
    target_extension=zip
  fi
  repeat_agctl="artifact-gateway-agctl_${version}_${target_goos}_${target_goarch}.${target_extension}"
  cmp "$workdir/dist/$repeat_agctl" "$workdir/dist-repeat/$repeat_agctl"
done
cmp "$workdir/dist/SHA256SUMS" "$workdir/dist-repeat/SHA256SUMS"

printf 'Release artifact test passed for %s/%s.\n' "$goos" "$goarch"
