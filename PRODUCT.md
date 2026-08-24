# Product

[简体中文](PRODUCT.zh-CN.md) · [Documentation index](docs/README.md)

<!-- impeccable:product-schema 1 -->

> This record is derived from the repository's domain contracts, operational
> documentation, release gates, and Console implementation. It makes no market,
> customer, or production-support promises that are not supported by those sources.

## Platform

Web.

## Users

- **Platform and repository administrators:** manage Repositories, Groups,
  identities, grants, retention, lifecycle jobs, audits, and runtime health.
- **Developers and release engineers:** publish or resolve artifacts through
  native OCI, Raw, Maven, Conan 2, npm, PyPI, Go Module, and APT clients.
- **Security and governance operators:** inspect immutable digests, scans,
  Quarantine, Promotion, Replication, anonymous access, and audit evidence.
- **CI systems and external applications:** use stable Service Account subjects
  with overlapping, revocable, expiring credentials and least-privilege grants.

## Product Purpose

Artifact Gateway is a multi-format artifact repository and governance control
plane. It stores, resolves, governs, and distributes software through native
ecosystem protocols. PostgreSQL owns lifecycle metadata, authorization, audit,
and background work; S3-compatible storage owns verified content-addressed bytes.

Success means native clients can publish or read through explicit capability
contracts, while operators can understand source, identity, scan, quarantine,
promotion, replication, retention, and recovery state from one source of truth.
High-risk operations remain auditable, retryable, idempotent, and recoverable.

## Positioning

The immutable governance identity is `Repository + format + canonical
coordinate + SHA-256 digest`. Artifact Gateway centers native protocol traffic,
explicit Hosted/Proxy/Group models, and database-backed lifecycle workflows. It
is not a transparent rewriting proxy, generic object browser, or vulnerability
scanner; external scanners contribute intelligence only through versioned contracts.

## Operating Context

- Human operators use the React/Vite Console and the management API generated
  from the same OpenAPI contract.
- Docker Compose and local Kubernetes support development and acceptance;
  production can split the same image into API, scheduler, and worker roles.
- Native clients include Docker/ORAS, Maven/Gradle, Conan 2, npm, pip/twine,
  Go, and Debian APT.
- CI, release automation, scanners, and integrations use Service Accounts.
  Production human identity uses HTTPS RS256 OIDC; static tokens are limited
  to local or break-glass use.
- Release decisions require protocol, persistence, Console, upgrade, and
  backup/restore evidence from a clean checkout. A healthy local page is not
  release approval.

## Capabilities and Constraints

- OCI, Raw, Maven, Conan 2, npm, and PyPI have complete Hosted lifecycle paths.
  Go has canonical single-ZIP Hosted publication, recoverable deletion,
  retention, reclaim, promotion, replication, and Proxy/Group reads. Its
  quarantine-read gate, authenticated upstream Proxy, and checksum database
  mirroring remain unavailable.
- APT has Proxy/Group reads and a management-only signed Hosted snapshot
  preview. The preview is not a stable compatibility commitment, and its
  bundled signer is not production key custody.
- A Repository owns format, policy, authorization, and Hosted bytes or one
  allowlisted upstream. A Group is an ordered resolution view and owns no bytes.
- Artifact lifecycle states are `staged`, `visible`, and `tombstoned`.
  Quarantine is a versioned Repository-local governance decision, not deletion
  or a fourth lifecycle state.
- Anonymous reads are disabled by default and require global and local policy.
  Anonymous writes and management operations are unsupported; reads are audited.
- Service Accounts have no global role. Grants bind stable subjects while
  multiple credentials may overlap during rotation; plaintext is returned once.
- PostgreSQL is the metadata and coordination source of truth. S3-compatible
  storage contains only verified bytes. Recovery pairs database and object-store evidence.
- Version 0.1.0 is an early packaged release. The project remains in active
  development and does not imply a stable public release or production support
  commitment.

## Brand Commitments

- The product name is **Artifact Gateway**.
- UI and documentation use the domain language from `CONTEXT.md`: Repository,
  Hosted Repository, Proxy Repository, Group, Artifact, Asset, Service Account,
  Publication, Tombstone, Quarantine, Promotion, and Replication.
- Communication is precise and operator-oriented. It never invents capacity,
  health, scan, or release data or presents a candidate as a released product.

## Evidence on Hand

- `CONTEXT.md` defines domain language and ownership.
- `README.md` and `ARCHITECTURE.md` define runtime and deployment boundaries.
- Lifecycle and Native Hosted contracts define visibility and object rules.
- Protocol compatibility records implemented behavior and explicit limits.
- Service Account and Console documents define machine identity and task models.
- Release readiness defines protocol, Console, upgrade, and recovery gates.
- `api/openapi/` and generated clients are the machine-readable management contract.
- `console/e2e/` provides browser evidence for responsive, identity, and artifact flows.

There are no verified customer stories, external benchmarks, pricing, SLA, or
stable-release commitments available for product claims.

## Product Principles

1. **Protocol truth first.** The UI must not alter native-client or Group semantics.
2. **Immutable identity throughout.** Scans, quarantine, promotion, replication,
   and recovery remain traceable to canonical coordinates and digests.
3. **Explicit governance, convergent defaults.** Anonymous access, upstreams,
   grants, and high-risk transitions require explicit policy and fail closed.
4. **Stable subjects, rotatable credentials.** Rotation does not rebuild grants
   or break audit continuity.
5. **Release conclusions come from an evidence matrix.** Code, protocol,
   persistence, Console, upgrade, and recovery gates decide release eligibility.

## Accessibility and Inclusion

- The Console and documentation support English and Simplified Chinese with
  consistent domain meaning.
- Management tasks support keyboard use, visible focus, semantic status, and
  `prefers-reduced-motion`.
- Responsive layouts cover narrow screens and zoom without hiding core governance.
