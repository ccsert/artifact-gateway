# Nexus Capability Review (2026-08)

[简体中文](nexus-gap-review-2026-08.zh-CN.md) | [Documentation index](README.md)

> Historical point-in-time review. The conclusions below reflect the August
> 2026 review and are no longer the authority for current capabilities. Use
> [Nexus gap analysis](nexus-gap-analysis.md) for delivered work and remaining gaps.

This review covered protocol backends, management APIs, lifecycle, storage,
authorization, and the main Ant Design 6 Console journeys. Artifact Gateway
already had a usable V1 across Hosted lifecycle, Proxy cache governance,
anonymous reads, audit retention, promotion/replication, and the four original
protocols. The remaining path to an enterprise experience was primarily
operational scale and security governance, not another collection of isolated
format pages.

## Confirmed strengths

- OCI, Maven, Conan, and Raw had explicit Hosted, Proxy, or Group boundaries.
- Publication, deletion, restore, retention, collection, promotion, and
  replication had idempotency, audit, or integrity controls; replication used
  checkpoints and SHA-256 verification.
- Anonymous policy, Repository Grants, local users, API-key roles, and OIDC
  validation existed with separate read, write, and management paths.
- The Ant Design 6 Console exposed global search, public browse, capacity
  trends, cache operations, access control, audit export, and login.

## Gaps recorded at the review

### P0: enterprise security and identity

The review called for richer IdP role mapping and session/logout behavior;
path-aware selectors, role templates, permission explanation, credential
expiry and last-use evidence; and observable baselines for password rotation,
JWKS/issuer configuration, CSRF, SSRF, upload limits, and archive expansion.

### P1: operational scale

Global search still fanned out from the Console and needed a permission-aware
server index and cursor pagination. Jobs needed scheduler controls, retry,
pause/cancel, queue depth, and worker health. Proxy needed configurable TTL,
negative caching, routing, and credential rotation. Backup and recovery needed
operator-visible policy, last-success, drill, and support-bundle evidence.

### P2: supply chain and product experience

Artifact detail needed checksum, signature, SBOM, provenance, license, and
vulnerability evidence plus copyable client commands. Publishing UI was thinner
outside Maven. Webhooks needed more event types and email; audit needed time
ranges, cursor pagination, saved queries, and server-side CSV export.

## Work delivered during the review

1. Repaired the Console lockfile for reproducible `npm ci` and pinned the
   generated-client formatting step to prevent OpenAPI drift.
2. Moved audit outcome, format, operation, and actor filters into OpenAPI,
   Memory/PostgreSQL stores, and the management API.
3. Added a global Console Jobs center for lifecycle and audit-retention work,
   with failure detail and state/kind/repository filters.
4. Extended retention from Maven to OCI, Conan, and Raw with format-aware
   candidates, cursor dry-runs, `If-Match`, and one worker path.
5. Made Raw deletion recoverable and prevented Proxy/Group from exposing
   invalid Hosted retention controls.

## Lifecycle follow-up recorded then

The next steps were durable retry/backoff and recovery of stuck running work;
run-now/cancel/retry/progress and job audit; policy-version handling for queued
jobs; explicit Conan recipe/package restore semantics; and richer retention
dry-run reason summaries and export.

## Recommended next phase at that time

The review prioritized API-key expiry/last-used, OIDC role mapping, and a
permission explainer; then server-side global search and real cursor paging;
then broader Webhook events, email, SBOM/scan display, and more task types.
It explicitly rejected unsafe hard deletion or unaudited background behavior
merely to imitate Nexus.
