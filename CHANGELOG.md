# Changelog

All user-visible changes to Artifact Gateway are recorded in this file.

The project has not published a stable release. Changes are collected under
`Unreleased` using the fixed categories below; a release moves those entries to
a dated version heading without rewriting their meaning.

## Unreleased

- Added each OCI manifest's immutable creation timestamp to repository browse
  responses so consumers can select the newest publication without inferring
  order from tags or digest text.
- Switched the runtime, local Compose, Kubernetes, integration tests, and
  configuration contract fully to RustFS using the official AWS SDK for Go v2;
  removed MinIO services, SDK dependencies, migration tooling, and cutover
  bypasses while retaining fail-closed detection of legacy resources.
- Added the first APT H3 signing hardening slice: remote signers require a
  public-only OpenPGP keyring matching one or two pinned fingerprints, and
  Gateway cryptographically verifies both signatures before visibility. This
  allows a controlled rotation overlap, derives signer identity and algorithm
  from verified keys, validates the complete keyring before preflight can pass,
  and records structured immutable signing evidence without changing
  authorization-reason semantics; the
  OpenPGP dependency chain now includes the CIRCL secp384r1 fix from v1.6.3.
  Repository administrators can now compare the configured old/next trust
  window with the latest visible snapshot in a generated API and bilingual
  Console view, while bounded outcome and latency metrics support operational
  alerting without high-cardinality signer or repository labels. During a
  rolling upgrade, a Console connected to an older Gateway now identifies the
  missing signing-state endpoint as an unavailable feature instead of
  incorrectly reporting that the repository does not exist.
  A dedicated external-signer gate now provisions signer-owned keys, mounts
  them read-only for serving, uses a signer-specific TLS CA, and proves old,
  overlap, new, rejection, and retirement behavior with clean Debian clients.
- Prioritized Cargo sparse-registry planning after APT H3 and deferred NuGet
  repository implementation while retaining its tested parser foundation.
- Kept access evaluation and repository grant editing focused on usable
  authorization principals by hiding disabled users and revoked or expired API
  keys from both principal pickers.

### Added

- A bounded Cargo C0 parser foundation that validates official publish framing
  and complete `.crate` gzip/tar archives, derives collision-safe immutable
  crate/version identity from normalized `Cargo.toml`, and translates current
  publish metadata into checksum-owned sparse-index rows. Official
  `cargo package` and `cargo publish` tests exercise the byte boundary without
  admitting Cargo to the public format catalog.
- A pinned, non-root Traefik Ingress for the Docker Desktop Kubernetes overlay,
  exposing the complete same-origin Console, API, and artifact surface at
  `artifact-gateway.localhost` with bounded resources and least-privilege RBAC.
- A bounded NuGet `.nupkg`/`.nuspec` parser with normalized, case-insensitive
  immutable package identity, plus an explicit staged roadmap that keeps the
  format undiscoverable until its executable protocol gates are complete.
- Idempotent local RustFS credential bootstrapping with retained rollback copies
  and a fail-closed guard for unsupported legacy object-store resources.
- A hardened Kustomize base and one-command local Kubernetes deployment with
  Gateway, Console, PostgreSQL, RustFS, idempotent migrations, persistent local
  volumes, health checks, manifest validation, and same-origin protocol routes.
- A staged APT Hosted roadmap covering native publication, atomic signed
  repository snapshots, external signing, lifecycle, scanning, quarantine,
  promotion, replication, and real APT client acceptance gates.
- Streaming Debian binary metadata parsing for gzip, xz, zstd, and uncompressed
  control archives, with server-derived package/version/architecture identity.
- The completed APT Hosted H1 pre-visibility foundation: idempotent quota-
  reserving publication sessions, explicit management-only Hosted provisioning,
  repository-scoped management and binary-safe generated
  OpenAPI clients, streaming `.deb` staging, explicit package/suite/component/
  architecture records, transactionally durable audit evidence, reference-
  checked RustFS orphan collection with heartbeat-fenced lifecycle-job retries
  and cross-instance integration coverage, immutable repository-snapshot records,
  and a private-key-free signer port. Installable Hosted publication remains
  gated on atomic signed snapshots.
- The completed APT Hosted H2 operator preview: deterministic `Packages` and gzip
  indices, Release checksum closure, Acquire-By-Hash objects, signed
  `InRelease`/`Release.gpg` assets, repository-global immutable pool paths,
  audited PostgreSQL visibility switching, Hosted GET/HEAD/range reads, and
  durable reference-checked cleanup of interrupted snapshot objects, a
  generated idempotent snapshot-publish API, loopback reference signer with an
  isolated persistent private key, Console/search/capacity projection, and a
  real signed Debian update/install gate. The gate now also proves exact
  PostgreSQL/RustFS recovery by publishing a later mutation, restoring the
  original signing evidence and every signed/index/package byte, and installing
  with the signer offline. Hosted remains unadvertised until H3 production key
  custody and rotation are complete.
- A pinned RustFS-only object-store baseline for Compose, integration tests,
  and Kubernetes, including streaming, metadata, Range, lifecycle, backup, and
  recovery contract coverage.
- One-command local development lifecycle targets for starting the complete
  stack, checking the Console and Gateway paths, and stopping only the
  checkout-managed Console.
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
- An optional non-root Trivy reference-scanner Compose profile with loopback-
  only transport, immutable asset verification, persistent CycloneDX reports,
  license evidence, vulnerability findings, health metadata, and a persistent
  vulnerability database cache.
- Per-repository scan-on-publication policies for Maven, OCI, Raw, npm, PyPI,
  and Conan Hosted repositories, with idempotent lifecycle jobs, audit records,
  and capability-aware Console controls.
- Per-artifact scan status, manual rescan controls, and bounded reconciliation
  for publications that missed automatic scanning or need a failed scan retried.
- Bounded per-vulnerability scanner findings with severity-count consistency,
  immutable evidence persistence, and searchable bilingual Console details.
- Versioned per-artifact quarantine and release controls with optimistic
  concurrency, audit evidence, admission-time enforcement, and worker-time
  protection against queued promotion or replication publication.
- Versioned Hosted quarantine-read policies, disabled by default, with
  protocol-level GET/HEAD denial, aggregate npm/PyPI and Conan closure
  semantics, metadata filtering, Group anti-bypass behavior, PostgreSQL
  persistence, OpenAPI clients, and Console controls.
- Durable administrator-managed Webhook subscriptions for Artifact quarantine
  and release events, with transactional outbox persistence, encrypted HMAC
  secrets, SSRF-safe HTTPS delivery, bounded retry/dead-letter replay, cluster
  leases, OpenAPI clients, audits, and Console operations visibility.
- Local-user governance with profile metadata, case-insensitive identities,
  failed-sign-in lockout, last-sign-in and password-change timestamps,
  mandatory password changes, administrator password resets, revocable
  versioned sessions, and last-active-administrator protection.

### Changed

- Routed APT requests through both the Vite development proxy and the
  production Console container so direct package-client paths cannot fall
  through to the SPA.
- Replaced default coordinate-and-digest entry in repository scanning,
  promotion, and replication workflows with a shared searchable immutable-
  artifact picker backed by protocol-owned canonical identity queries, including
  historical npm/PyPI versions, locally cached Proxy assets, and Conan revisions,
  while retaining advanced exact-identity input as a recovery path.
- Added a discoverable repository Scanning workspace for manual immutable-
  artifact scans, capability and configuration guidance, historical backfill,
  and recent job status.
- Updated retention controls to use Maven, OCI, Conan, Raw, npm, and PyPI
  cleanup-unit terminology instead of Maven fallback copy.
- Reorganized the repository security tab into separate quarantine-read and
  promotion-admission guardrails with format-aware scope, explicit saved and
  unsaved states, and contextual scanner availability.
- Corrected the repository scanning and security layouts with frameless tab
  surfaces, desktop alert and guardrail grids, and overflow-free single-column
  behavior on narrower viewports.
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
- Isolated artifact publication locks in a bounded, observable PostgreSQL pool
  and made multi-file object and coordinate locks share one backend session.

### Fixed

- Replaced expired native Maven staging sessions on the next authenticated PUT
  so interrupted publishes can retry without remaining permanently blocked by
  the expired coordinate lock.
- Resolved npm Proxy and Group tarballs directly from a cold `package-lock.json`
  URL before any packument request, accepted canonical single-root manifest
  layouts used by legacy scoped packages, retained online-to-offline `npm ci`
  caching, and emitted one member-owned terminal audit for metadata failures.
- Honored repeated OCI `Accept` request headers when selecting manifest media
  types, matching Docker clients that send one header field per supported type.
- Prevented OpenAPI contract checks from reinstalling dependencies underneath
  a running Vite Console, and replaced the default lazy-route exception page
  with a bilingual recovery screen.

- Resolved public artifact deep links even when the target coordinate is beyond
  the first browse page.
- Fixed PostgreSQL OCI upload expiry scans so scheduled reclaim jobs are
  created and completed reliably across worker instances.
- Stabilized generated OpenAPI output, dependency installation, integration
  readiness, and lifecycle ordering in CI.
- Included PyPI and Go object usage in aggregate repository capacity views.
- Prevented PyPI promotion, replication, and restore from publishing or
  restoring a partial version when its file membership changes mid-operation;
  parked replication plans now refresh checkpoints on exact replay.
- Corrected user-management audits so the actor is the administrator performing
  the action and self-service password changes use their own audit resource.

### Security

- Updated the Go toolchain and dependency pins used by release-readiness checks.
