# Changelog

All user-visible changes to Artifact Gateway are recorded in this file.

The project has not published a stable release. Changes are collected under
`Unreleased` using the fixed categories below; a release moves those entries to
a dated version heading without rewriting their meaning.

## Unreleased

### Added

- Role-based `api`, `scheduler`, and `worker` deployments with format- and
  job-specific worker filters.
- Server-side aggregate repository management and cross-repository search APIs.
- Per-repository outbound proxy configuration and connectivity checks.

### Changed

- Reduced Console repository request fan-out and split large vendor bundles for
  faster navigation and initial loading.
- Compacted repository detail navigation and added restrained interface motion.
- Streamed artifact uploads and bounded database, HTTP, and background-worker
  resource usage.
- Added package-level Go coverage floors and Console lint, formatting,
  accessibility, component-test, and coverage gates.

### Fixed

- Resolved public artifact deep links even when the target coordinate is beyond
  the first browse page.
- Stabilized generated OpenAPI output, dependency installation, integration
  readiness, and lifecycle ordering in CI.

### Security

- Updated the Go toolchain and dependency pins used by release-readiness checks.
