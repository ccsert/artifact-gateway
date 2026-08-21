#!/usr/bin/env sh
set -eu

compatibility_doc="docs/protocol-compatibility.md"
nexus_doc="docs/nexus-gap-analysis.md"
readiness_doc="docs/release-readiness.md"

contains_fixed_string() {
  needle=$1
  file=$2

  if command -v rg >/dev/null 2>&1; then
    rg -qi --fixed-strings "$needle" "$file"
    return
  fi

  grep -Fqi -- "$needle" "$file"
}

for file in \
  README.md \
  README.zh-CN.md \
  docs/README.md \
  docs/README.zh-CN.md \
  docs/getting-started.md \
  docs/getting-started.zh-CN.md \
  docs/architecture-diagrams.md \
  docs/architecture-diagrams.zh-CN.md \
  docs/postgresql-capabilities.md \
  docs/postgresql-capabilities.en.md \
  docs/performance-baseline.md \
  docs/performance-baseline.zh-CN.md \
  docs/project-quality-assessment.md \
  docs/project-quality-assessment.zh-CN.md \
  docs/assets/artifact-gateway-hero.png \
  docs/assets/artifact-gateway-system-architecture.png \
  docs/assets/artifact-gateway-deployment-topology.png; do
  if [ ! -s "$file" ]; then
    printf 'missing required documentation entry %s\n' "$file" >&2
    exit 1
  fi
done

for pair in \
  'README.md|README.zh-CN.md' \
  'docs/README.md|README.zh-CN.md' \
  'docs/getting-started.md|getting-started.zh-CN.md' \
  'docs/architecture-diagrams.md|architecture-diagrams.zh-CN.md' \
  'docs/postgresql-capabilities.en.md|postgresql-capabilities.md' \
  'docs/performance-baseline.md|performance-baseline.zh-CN.md' \
  'docs/project-quality-assessment.md|project-quality-assessment.zh-CN.md'; do
  source_file=${pair%%|*}
  target_name=${pair#*|}
  if ! contains_fixed_string "$target_name" "$source_file"; then
    printf 'missing reciprocal language link %s from %s\n' "$target_name" "$source_file" >&2
    exit 1
  fi
done

for pair in \
  'README.zh-CN.md|README.md' \
  'docs/README.zh-CN.md|README.md' \
  'docs/getting-started.zh-CN.md|getting-started.md' \
  'docs/architecture-diagrams.zh-CN.md|architecture-diagrams.md' \
  'docs/postgresql-capabilities.md|postgresql-capabilities.en.md' \
  'docs/performance-baseline.zh-CN.md|performance-baseline.md' \
  'docs/project-quality-assessment.zh-CN.md|project-quality-assessment.md'; do
  source_file=${pair%%|*}
  target_name=${pair#*|}
  if ! contains_fixed_string "$target_name" "$source_file"; then
    printf 'missing reciprocal language link %s from %s\n' "$target_name" "$source_file" >&2
    exit 1
  fi
done

if ! contains_fixed_string 'PostgreSQL is the only coordination and database dependency' README.md ||
  ! contains_fixed_string 'S3-compatible object-storage' README.md ||
  ! contains_fixed_string 'PostgreSQL 是唯一的协调与数据库依赖' README.zh-CN.md ||
  ! contains_fixed_string 'S3 兼容对象存储' README.zh-CN.md; then
  printf 'README lightweight storage boundary is missing or misleading\n' >&2
  exit 1
fi

for file in README.md README.zh-CN.md docs/architecture-diagrams.md docs/architecture-diagrams.zh-CN.md; do
  if ! contains_fixed_string 'artifact-gateway-system-architecture.png' "$file"; then
    printf 'missing generated system architecture from %s\n' "$file" >&2
    exit 1
  fi
done

for file in docs/architecture-diagrams.md docs/architecture-diagrams.zh-CN.md; do
  if ! contains_fixed_string 'artifact-gateway-deployment-topology.png' "$file"; then
    printf 'missing generated deployment topology from %s\n' "$file" >&2
    exit 1
  fi
done

for file in README.md README.zh-CN.md docs/getting-started.md docs/getting-started.zh-CN.md; do
  if ! contains_fixed_string 'make dev-bootstrap' "$file"; then
    printf 'missing executable bootstrap path from %s\n' "$file" >&2
    exit 1
  fi
done

node --test scripts/docs-link-check.test.mjs
node scripts/docs-link-check.mjs

for format in OCI Maven Raw Conan npm PyPI Go; do
  if ! contains_fixed_string "$format" "$compatibility_doc"; then
    printf 'missing format %s from %s\n' "$format" "$compatibility_doc" >&2
    exit 1
  fi
done

for required in \
  'repositories:intelligence' \
  'versioned admission policies' \
  'automatic scanner execution'; do
  if ! contains_fixed_string "$required" "$nexus_doc"; then
    printf 'missing capability statement %s from %s\n' "$required" "$nexus_doc" >&2
    exit 1
  fi
done

if ! contains_fixed_string 'npm, PyPI, and Go Hosted lifecycle paths' "$readiness_doc"; then
  printf 'release readiness document has stale hosted format coverage\n' >&2
  exit 1
fi

printf '%s\n' 'documentation capability checks passed'
