# Artifact Gateway documentation

[简体中文](README.zh-CN.md) · [Project README](../README.md)

Artifact Gateway is a lightweight, protocol-native artifact repository. Its
control plane relies on PostgreSQL as its only coordination and database
dependency; immutable bytes live in S3-compatible object storage.

This index follows the framework-neutral navigation contract in
[`site-map.json`](site-map.json). Capability claims are current only when they
agree with executable tests and the
[protocol compatibility baseline](protocol-compatibility.md).

## Start here

- [Project overview](../README.md) — positioning, capabilities, quick start, and lightweight deployment boundary.
- [Documentation site guide](documentation-site-guide.md) — locale, route, navigation, and future site-generator contract.
- [Getting started](getting-started.md) — complete local stack and first Repository.
- [Product direction](../PRODUCT.md) — users, use cases, product boundary, and priorities.
- [Contributing](../CONTRIBUTING.md) — change workflow, documentation conventions, and required checks.
- [Format extension guide](format-extension-guide.md) — admission requirements for a new package ecosystem.

## Architecture and design

- [Architecture](../ARCHITECTURE.md) — runtime, storage, consistency, and code ownership.
- [Architecture diagrams](architecture-diagrams.md) — system, deployment, publication, and Worker flows.
- [Design system](../DESIGN.md) — Console visual language, components, layout, and interaction rules.
- [Domain language](../CONTEXT.md) — canonical Repository, Artifact, lifecycle, and distribution terminology.
- [PostgreSQL capabilities](postgresql-capabilities.md) — locks, leases, queues, notifications, JSONB, search, and observability.
- [Native Hosted contract](native-hosted-contract.md) — metadata authority, object lifecycle, transactions, and idempotency.
- [V2 contract](v2-contract.md) — historical Raw, Conan 2, anonymous-policy, and migration context.
- [Artifact lifecycle contract](artifact-lifecycle-contract.md) — visibility, Tombstone, restore, collection, promotion, and replication.
- [Repository deletion contract](repository-deletion-contract.md) — safe logical deletion and recovery semantics.
- [Distributed deployment](distributed-deployment.md) — API, Scheduler, Worker, PostgreSQL, and object-store topology.
- [ADR 0001: full artifact repository](adr/0001-full-artifact-repository.md)
- [ADR 0002: promotion snapshots](adr/0002-promotion-snapshots.md)
- [ADR 0003: protocol-only formats](adr/0003-protocol-only-formats.md)
- [ADR 0004: Go Hosted publication](adr/0004-go-hosted-publication.md)
- [ADR 0005: Console semantic theme system](adr/0005-console-semantic-theme-system.md)

## Protocols and formats

- [Protocol compatibility](protocol-compatibility.md) — implemented behavior and explicit limits by format.
- [Maven Hosted publication](maven-hosted-publication.md) — Nexus-compatible direct mode and strict opt-in commit.
- [APT Proxy and Group](apt-proxy.md) — byte-preserving reads, caching, authorization, and limits.
- [APT Hosted signing](apt-hosted-signing.md) — H2 preview, external signer, H3 rotation, and production boundary.
- [APT Hosted roadmap](apt-hosted-roadmap.md) — ordered H1-H4 acceptance gates.
- [Cargo repository research](cargo-repository-research.md) — sparse registry recommendation and C0-C4 roadmap.
- [NuGet roadmap](nuget-roadmap.md) — deferred protocol and lifecycle plan.
- [Legacy Group migration](legacy-group-migration.md) — compatibility behavior and migration guidance.
- [Gitea backend reference](gitea-backend-reference.md) — preflight and evidence-loop design reference.

## Operations and security

- [Kubernetes deployment](kubernetes-deployment.md) — local executable baseline and production requirements.
- [Recovery runbook](recovery-runbook.md) — backup, restore, RPO/RTO evidence, and rollback.
- [Anonymous access operations](anonymous-access-operations.md) — default-deny global, Group, and Repository gates.
- [Local user governance](user-governance.md) — accounts, sessions, lockout, OIDC linkage, and audit.
- [OIDC browser SSO](oidc-sso.md) — Code + PKCE, runtime configuration, role mapping, and session behavior.
- [Keycloak on Kubernetes](oidc-keycloak-k8s.md) — real browser callback acceptance.
- [Repository Grant authorization](repository-grant-authorization-plan.md) — scoped runtime decisions and rollout.
- [Service Account operations](service-account-operations.md) — stable machine principals and zero-downtime rotation.
- [Security admission policy](security-admission-policy.md) — promotion evidence, Quarantine, and optional read enforcement.
- [Artifact scanner contract](artifact-scanner-contract.md) — bounded external scanning, health, evidence, and reconciliation.
- [Proxy egress design](proxy-egress-design.md) — direct, environment, HTTP, and SOCKS5 per-Repository routing.
- [Webhook delivery contract](webhook-delivery-contract.md) — transactional events, HMAC, retries, and replay.
- [Security policy](../SECURITY.md) — private reporting and deployment baseline.

## Quality, performance, and release

- [Performance baseline](performance-baseline.md) — binary/image size, quiet memory, concurrency, and large-object evidence.
- [Project quality assessment](project-quality-assessment.md) — strengths, risks, and improvement priorities.
- [Backend completion checklist](backend-completion-checklist.md) — V1 implementation status and next slices.
- [Release readiness](release-readiness.md) — executable protocol, integration, upgrade, and recovery gates.
- [Release record template](release-record-template.md) — evidence, approval, and rollback fields.
- [Preparation record: 2026-08-11](release-records/2026-08-11-d738d4ed.md) — historical local candidate evidence, not production approval.
- [Changelog](../CHANGELOG.md) — user-visible work collected under `Unreleased`.

## Strategy and reference

- [Full repository goal](full-artifact-repository-goal.md) — V1 definition of done.
- [Full repository roadmap](full-artifact-repository-roadmap.md) — architecture sequence and current status.
- [Nexus gap analysis](nexus-gap-analysis.md) — current cross-product capability and experience comparison.
- [Nexus gap review, 2026-08](nexus-gap-review-2026-08.md) — historical point-in-time review.
- [Nexus Maven publication research](nexus-maven-publication-research.md) — primary-source direct versus staging behavior.
- [Repository Console roadmap](repository-console-experience-roadmap.md) — browse, capacity, Proxy operations, and policy experience.
- [OpenAPI governance](openapi-governance-plan.md) — source, generation, runtime, and review boundaries.

## Documentation rules

- English uses unsuffixed `.md`; Simplified Chinese uses `.zh-CN.md`.
- Every page pair links both ways and appears once in `site-map.json` with localized titles.
- Both locales preserve commands, routes, status codes, compatibility limits, security boundaries, and delivery status.
- Research, preview, historical evidence, and roadmap work stay explicitly labeled.
- Run `make docs-check` before submitting documentation changes.
