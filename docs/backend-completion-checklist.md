# Backend Completion Checklist

This checklist is the working backlog for the **Full Artifact Repository V1**
goal. It intentionally excludes frontend work.

## Immediate Rule

- Do not introduce a new `/conan/v3` route family. Conan Hosted work should
  extend the existing Conan backend surface with clear Hosted-vs-Group
  resolution, while preserving current Conan read-through behavior.

## Lifecycle Foundation

- [x] Shared artifact state model and lifecycle job store.
- [x] OCI manifest tombstones and OCI reclaim worker.
- [x] Maven tombstones/retention, recoverable restore, and Maven reclaim worker.
- [x] Raw reclaim worker.
- [x] Conan native recipe/package revision state model.
- [x] Conan HTTP Hosted publication, resolution, deletion, and reclaim worker.

## Protocol Completion

- [x] OCI catalog endpoint and pagination.
- [x] OCI referrers endpoint.
- [x] OCI repository browse/search projection.
- [x] Maven browse/search projection.
- [x] Maven publication companion hardening and black-box fixture coverage.
- [x] Raw object listing.
- [x] Raw checksum metadata and checksum sidecar behavior.
- [x] Raw resumable upload support.
- [x] Conan Hosted publish/session flow.
- [x] Conan Hosted metadata/file read flow.
- [x] Conan Hosted logical delete and restore.
- [x] Conan Hosted search/index projection.

## Management API

- [x] Repository capability endpoint per format/type.
- [x] Cross-format artifact browse/search API.
- [x] Tombstone inspection API.
- [x] Lifecycle job status API.
- [x] Maven retention execution API and dry-run reporting.
- [x] Restore API for supported tombstoned artifacts.

## Distribution

- [x] Maven immutable promotion API and worker, including HTTP retry and PostgreSQL/RustFS evidence.
- [x] OCI, Raw, and Conan immutable promotion API and worker, including
  authorization, idempotency, audit records, retry behavior, HTTP black-box,
  and PostgreSQL/RustFS evidence.
- [x] Replication planning model.
- [x] Checkpointed replication worker with persisted checkpoints, retry, resume,
  and SHA-256 integrity checks.
- [x] Promotion/replication authorization and audit events.

## Operations

- [x] Repository quota accounting across OCI, Maven, Raw, and Conan Hosted
  Repositories.
- [x] Per-repository concurrency limits for lifecycle jobs.
- [x] Metrics for lifecycle jobs, tombstones, promotion, and replication.
- [x] Durable artifact scan jobs with immutable identity status lookup,
  publication idempotency, and repository-level missing-scan reconciliation.
- [x] Sanitized scanner health and Gateway-enforced vulnerability database
  freshness in administrator diagnostics.
- [x] Transactional operational events and durable HMAC-signed Webhook
  delivery for Artifact quarantine and release, including lease recovery,
  bounded retry, dead-letter replay, and administrator visibility.
- [x] Backup/restore coverage for promotion and replication state.
- [x] Release preflight and evidence coverage for lifecycle operations.
- [x] Black-box protocol tests for publish, resolve, delete, retain, and
  restore across OCI, Maven, Raw, and Conan.

## Current Next Slice

1. Begin APT H3 without widening the public format profile: define production
   signer/key custody, rotation overlap, backup/restore verification, metrics,
   alerts, and an operator-visible signing state.
2. Start Cargo C0 after the APT H3 production-signing gate: freeze a bounded
   `.crate` byte/identity contract and canonical sparse-index rules before
   adding the format enum. Cargo is the next candidate ecosystem; see
   [Cargo repository research](cargo-repository-research.md).
3. Add the optional scanner workload to the Kubernetes overlay only when its
   network policy, persistent SBOM storage, resource limits, and real scan
   smoke test are part of the same slice. Keep the local APT reference signer
   confined to its loopback sidecar and dedicated key volume until H3 replaces
   it with production key custody.
4. Keep NuGet deferred. Preserve and test the existing bounded parser, but do
   not begin publication persistence until Cargo C0-C1 or customer demand
   changes the priority.
5. Run the release gates and retain their output in the release record before
   any public capability expansion.
