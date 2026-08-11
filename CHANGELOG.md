# Changelog

All user-visible changes to Artifact Gateway are recorded in this file.

The project has not published a stable release. Changes are collected under
`Unreleased` using the fixed categories below; a release moves those entries to
a dated version heading without rewriting their meaning.

## Unreleased

### Added

- Administrator-only sanitized system diagnostics covering build identity,
  runtime roles, dependency reachability, node health, and repository job
  queues, with a bilingual Console view and copyable support JSON.
- Role-based `api`, `scheduler`, and `worker` deployments with format- and
  job-specific worker filters.
- PostgreSQL-backed runtime node heartbeats and an administrator inventory of
  node roles and Worker capabilities.
- Server-side aggregate repository management and cross-repository search APIs.
- Per-repository outbound proxy configuration and connectivity checks.
- npm Proxy repositories with verified read-through metadata and tarball
  caching, stale-if-error reads, negative caching, and offline installs.
- npm Group registries that merge Hosted and Proxy package versions with
  Hosted-first conflict resolution, anonymous/grant filtering, and Group-local
  tarball URLs.
- PyPI Hosted, Proxy, and Group repositories with native `pip` upload and
  install flows, normalized project browsing, anonymous/grant filtering, and
  read-through package caching.
- npm and PyPI artifact lifecycle operations covering retention, tombstones,
  restore, object collection, promotion, and replication.
- Go Module Proxy and Group repositories with standard `GOPROXY` endpoints,
  verified read-through caching, offline resolution, anonymous/grant filtering,
  search, capacity accounting, deep-linked Console browsing, and a real Go CLI
  acceptance gate.
- APT Proxy and ordered Group repositories with byte-preserving `dists/` and
  `pool/` reads, conditional metadata revalidation, stale-if-error and range
  responses, anonymous/grant filtering, search, capacity accounting, and
  deep-linked Console browsing.
- Configurable external artifact scanning with durable, idempotent management
  jobs; isolated workers; native Raw, Maven, OCI, npm, PyPI, Go, and Conan asset
  resolution; and optimistic security-intelligence merging.
- Per-repository scan-on-publication policies for Maven, OCI, Raw, npm, PyPI,
  and Conan Hosted repositories, with idempotent lifecycle jobs, audit records,
  and capability-aware Console controls.
- Per-artifact scan status, manual rescan controls, and bounded reconciliation
  for publications that missed automatic scanning or need a failed scan retried.
- Bounded per-vulnerability scanner findings with severity-count consistency,
  immutable evidence persistence, and searchable bilingual Console details.
- Local-user governance with profile metadata, case-insensitive identities,
  failed-sign-in lockout, last-sign-in and password-change timestamps,
  mandatory password changes, administrator password resets, revocable
  versioned sessions, and last-active-administrator protection.

### Changed

- Unified scan and promotion-intelligence lifecycle execution around shared
  claim, lease, metrics, terminal-state, polling, and PostgreSQL notification
  semantics, so queued work starts promptly without sacrificing polling-based
  recovery.
- Reduced Console repository request fan-out and split large vendor bundles for
  faster navigation and initial loading.
- Compacted repository detail navigation and added restrained interface motion.
- Streamed artifact uploads and bounded database, HTTP, and background-worker
  resource usage.
- Added package-level Go coverage floors and Console lint, formatting,
  accessibility, component-test, and coverage gates.
- Added session-aware distributed node inventory, graceful offline state,
  retention cleanup, and cluster capability health summaries.
- Reworked Console user management around server-side search, filtering, and
  pagination plus a focused account drawer for profile and security actions.

### Fixed

- Resolved public artifact deep links even when the target coordinate is beyond
  the first browse page.
- Fixed PostgreSQL OCI upload expiry scans so scheduled reclaim jobs are
  created and completed reliably across worker instances.
- Stabilized generated OpenAPI output, dependency installation, integration
  readiness, and lifecycle ordering in CI.
- Included PyPI and Go object usage in aggregate repository capacity views.
- Corrected user-management audits so the actor is the administrator performing
  the action and self-service password changes use their own audit resource.

### Security

- Updated the Go toolchain and dependency pins used by release-readiness checks.
