#!/usr/bin/env sh
set -eu

compatibility_doc="docs/protocol-compatibility.md"
nexus_doc="docs/nexus-gap-analysis.md"
readiness_doc="docs/release-readiness.md"

for format in OCI Maven Raw Conan npm PyPI Go; do
  if ! rg -qi --fixed-strings "$format" "$compatibility_doc"; then
    printf 'missing format %s from %s\n' "$format" "$compatibility_doc" >&2
    exit 1
  fi
done

for required in \
  'repositories:intelligence' \
  'versioned admission policies' \
  'automatic scanner execution'; do
  if ! rg -qi --fixed-strings "$required" "$nexus_doc"; then
    printf 'missing capability statement %s from %s\n' "$required" "$nexus_doc" >&2
    exit 1
  fi
done

if ! rg -qi --fixed-strings 'npm, and PyPI Hosted lifecycle paths' "$readiness_doc"; then
  printf 'release readiness document has stale hosted format coverage\n' >&2
  exit 1
fi

printf '%s\n' 'documentation capability checks passed'
