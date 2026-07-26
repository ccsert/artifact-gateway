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
- [x] Maven tombstones/retention and Maven reclaim worker.
- [x] Raw reclaim worker.
- [x] Conan native recipe/package revision state model.
- [ ] Conan HTTP Hosted publication, resolution, deletion, and reclaim worker (publication, resolution, and deletion complete; PostgreSQL/MinIO reclaim evidence pending).

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

- [ ] Repository capability endpoint per format/type.
- [ ] Cross-format artifact browse/search API.
- [ ] Tombstone inspection API.
- [ ] Lifecycle job status API.
- [ ] Retention execution API and dry-run reporting.
- [ ] Restore API for supported tombstoned artifacts.

## Distribution

- [ ] Promotion API and worker for immutable artifacts.
- [ ] Replication planning model.
- [ ] Checkpointed replication worker with retry and integrity checks.
- [ ] Promotion/replication authorization and audit events.

## Operations

- [ ] Repository quota accounting across Hosted formats.
- [ ] Per-repository concurrency limits for publish/delete/reclaim jobs.
- [ ] Metrics for lifecycle jobs, tombstones, promotion, and replication.
- [ ] Backup/restore coverage for all lifecycle states.
- [ ] Release preflight and evidence coverage for new lifecycle operations.
- [ ] Black-box protocol tests for publish, resolve, delete, retain, and
  restore across OCI, Maven, Raw, and Conan.

## Current Next Slice

Add PostgreSQL/MinIO reclaim-worker evidence for Conan, then start the
cross-format management API (capabilities, unified browse/search, tombstones,
lifecycle jobs, retention dry-run, and restore).
