#!/usr/bin/env bash

# Source this helper from an isolated rehearsal. A registry manifest digest or
# local image ID identifies the exact image; moving tags are not evidence.
readiness_validate_image_ref() {
  [[ "$1" =~ ^sha256:[0-9a-f]{64}$ || "$1" =~ ^[^[:space:]@]+@sha256:[0-9a-f]{64}$ ]] || {
    printf '%s\n' 'Readiness images require an immutable registry digest or local sha256 image ID.' >&2
    return 1
  }
}

readiness_load_image() {
  local image=$1 expected_revision=$2 expected_version=$3 local_tag=$4 revision version identity
  readiness_validate_image_ref "$image" || return 1
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    [[ "$image" != sha256:* ]] || { printf '%s\n' 'The pinned local image is not present.' >&2; return 1; }
    docker pull "$image" || return 1
  fi
  revision=$(docker image inspect "$image" --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}') || return 1
  version=$(docker image inspect "$image" --format '{{ index .Config.Labels "org.opencontainers.image.version" }}') || return 1
  [[ "$revision" == "$expected_revision" && "$version" == "$expected_version" ]] || {
    printf 'Readiness image identity mismatch: revision=%s version=%s; expected revision=%s version=%s.\n' \
      "$revision" "$version" "$expected_revision" "$expected_version" >&2
    return 1
  }
  identity=$(docker run --rm "$image" version) || return 1
  [[ "$identity" == "artifact-gateway $expected_version (revision $expected_revision,"* ]] || {
    printf 'Readiness image binary identity mismatch: %s\n' "$identity" >&2
    return 1
  }
  docker image tag "$image" "$local_tag" || return 1
  printf 'Verified readiness image: %s; %s\n' "$image" "$identity"
}
