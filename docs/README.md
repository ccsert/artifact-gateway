# Artifact Gateway documentation

[简体中文](README.zh-CN.md) · [Project README](../README.md)

This index separates current contracts and runbooks from roadmap material.
Capability claims are current only when they agree with executable tests and
the [protocol compatibility baseline](protocol-compatibility.md).

## Start here

- [Getting started](getting-started.md) — complete local stack and first repository.
- [Architecture](../ARCHITECTURE.md) — runtime, storage, consistency, and code ownership.
- [Architecture diagrams](architecture-diagrams.md) — system, deployment, publication, and worker flows.
- [Protocol compatibility](protocol-compatibility.md) — implemented behavior and explicit limits by format.
- [Contributing](../CONTRIBUTING.md) — change workflow and required checks.
- [Changelog](../CHANGELOG.md) — user-visible changes under active development.
- [Project quality assessment](project-quality-assessment.md) — current strengths, risks, and improvement order.
- [Performance baseline](performance-baseline.md) — binary and image size, quiet memory, local concurrency, and limits.

## Core contracts

- [Native Hosted contract](native-hosted-contract.md)
- [Artifact lifecycle contract](artifact-lifecycle-contract.md)
- [Repository deletion contract](repository-deletion-contract.md)
- [V2 management contract](v2-contract.md)
- [PostgreSQL capabilities](postgresql-capabilities.en.md)
- [Format extension guide](format-extension-guide.md)
- [OpenAPI governance plan](openapi-governance-plan.md)
- [Full repository goal](full-artifact-repository-goal.md)
- [Full repository roadmap](full-artifact-repository-roadmap.md)

### Architecture decisions

- [ADR 0001: full artifact repository](adr/0001-full-artifact-repository.md)
- [ADR 0002: promotion snapshots](adr/0002-promotion-snapshots.md)
- [ADR 0003: protocol-only formats](adr/0003-protocol-only-formats.md)
- [ADR 0004: Go Hosted publication](adr/0004-go-hosted-publication.md)

## Formats and upstreams

- [APT Proxy](apt-proxy.md)
- [APT Hosted roadmap](apt-hosted-roadmap.md)
- [APT Hosted signing](apt-hosted-signing.md)
- [Cargo repository research](cargo-repository-research.md)
- [NuGet roadmap](nuget-roadmap.md)
- [Legacy Group migration](legacy-group-migration.md)
- [Gitea backend reference](gitea-backend-reference.md)

## Identity, policy, and integration

- [Anonymous access operations](anonymous-access-operations.md)
- [Local user governance](user-governance.md)
- [OIDC SSO](oidc-sso.md)
- [Keycloak on Kubernetes](oidc-keycloak-k8s.md)
- [Repository grant authorization plan](repository-grant-authorization-plan.md)
- [Service account operations](service-account-operations.md)
- [Security admission policy](security-admission-policy.md)
- [Artifact scanner contract](artifact-scanner-contract.md)
- [Proxy egress design](proxy-egress-design.md)
- [Webhook delivery contract](webhook-delivery-contract.md)

## Deployment and operations

- [Performance baseline](performance-baseline.md)
- [Distributed deployment](distributed-deployment.md)
- [Kubernetes deployment](kubernetes-deployment.md)
- [Recovery runbook](recovery-runbook.md)
- [Backend completion checklist](backend-completion-checklist.md)

## Product gap and preparation evidence

These documents describe internal preparation and roadmap evidence. They are
not a public release, formal distribution record, or support commitment.

- [Nexus gap analysis](nexus-gap-analysis.md)
- [Nexus gap review, 2026-08](nexus-gap-review-2026-08.md)
- [Repository Console experience roadmap](repository-console-experience-roadmap.md)
- [Release-readiness working checklist](release-readiness.md)
- [Release record template](release-record-template.md)
- [Internal readiness record, 2026-08-11](release-records/2026-08-11-d738d4ed.md)

## Documentation rules

- Keep the English and Simplified Chinese README, architecture, contributing,
  protocol-compatibility, recovery, and getting-started entry points reciprocal
  and behaviorally equivalent.
- Put precise protocol support in `protocol-compatibility.md`; avoid copying
  long format contracts into the project README.
- Mark previews, research, and roadmap work explicitly. Do not present them as
  shipped capabilities.
- Add or update a focused guide when a change introduces operator decisions,
  recovery steps, or a new public contract.
- Run `make docs-check` before submitting documentation changes.
