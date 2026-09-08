#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=readiness-image.sh
source "$root/scripts/readiness-image.sh"

workspace=$(mktemp -d)
trap 'rm -rf "$workspace"' EXIT
calls="$workspace/docker-calls"
image="example.invalid/gateway@sha256:$(printf 'a%.0s' {1..64})"
expected_revision=$(printf 'b%.0s' {1..40})
label_revision=$expected_revision
label_version=0.1.0
binary_revision=$expected_revision
binary_version=0.1.0
docker() {
  printf '%s\n' "$*" >> "$calls"
  case "$1 $2" in
    'image inspect')
      case "${5:-}" in
        *revision*) printf '%s\n' "$label_revision" ;;
        *version*) printf '%s\n' "$label_version" ;;
      esac ;;
    'run --rm') printf 'artifact-gateway %s (revision %s, go test)\n' "$binary_version" "$binary_revision" ;;
    'image tag') ;;
    *) printf 'Unexpected Docker call: %s\n' "$*" >&2; return 1 ;;
  esac
}
assert_rejected() {
  : > "$calls"
  if readiness_load_image "$1" "$expected_revision" 0.1.0 fixture:local > "$workspace/result" 2>&1; then
    printf 'Accepted an invalid readiness image: %s\n' "$1" >&2
    exit 1
  fi
  if grep -q '^image tag ' "$calls"; then
    printf '%s\n' 'Rejected image was installed into the rehearsal.' >&2
    exit 1
  fi
}
assert_rejected example.invalid/gateway:latest
test ! -s "$calls"
label_revision=wrong
assert_rejected "$image"
label_revision=$expected_revision
label_version=wrong
assert_rejected "$image"
label_version=0.1.0
binary_revision=wrong
assert_rejected "$image"
binary_revision=$expected_revision
binary_version=wrong
assert_rejected "$image"
binary_version=0.1.0
for pinned in "$image" "sha256:$(printf 'c%.0s' {1..64})"; do
  : > "$calls"
  readiness_load_image "$pinned" "$expected_revision" 0.1.0 fixture:local
  grep -q '^image tag ' "$calls"
done
printf '%s\n' 'Readiness image identity tests passed.'
