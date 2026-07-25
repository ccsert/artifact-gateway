# Full Artifact Repository V1 Goal

## Objective

Deliver Artifact Gateway V1 as a complete artifact repository for its four
supported formats: OCI, Maven, Raw, and Conan. A release is complete only when
every supported format can participate in Hosted, Proxy, and Group workflows,
has a safe artifact lifecycle, and can be operated and recovered as a repository
rather than as a read-through gateway.

This goal supersedes all earlier “read path complete” or “production readiness
only” goals. Preflight, evidence collection, and recovery drills remain required
release infrastructure for every phase.

## Definition Of Done

V1 is done when all of the following are true:

- **Lifecycle:** staged, visible, and tombstoned states are consistent across
  formats; publication is atomic; physical reclamation is delayed, reference
  checked, and performed through durable lifecycle jobs.
- **Format parity:** OCI, Maven, Raw, and Conan each support Hosted, Proxy, and
  Group use; native publication, resolution, browse/search, logical deletion,
  retention, and restore are covered by black-box protocol tests.
- **Management:** a versioned API exposes repository capabilities, artifact
  browse/search, tombstone inspection, retention execution, and lifecycle job
  status without changing existing V1/V2 meanings.
- **Distribution:** administrators can promote immutable artifacts between
  Hosted Repositories; replication is resumable, integrity checked, authorized,
  observable, and idempotent.
- **Operations:** repository quotas, capacity, concurrency limits, audits,
  metrics, backup/restore, upgrade/rollback, release preflight, and production
  evidence cover every lifecycle state and background operation.
- **Compatibility:** existing protocol reads and documented V1/V2 management
  behavior remain regression-tested through every migration.

## Ordered Work Packages

1. **Finish lifecycle foundation.** Add the reclaim worker to the completed
   tombstone and lifecycle-job store; migrate Raw and Maven deletion/reclaim
   onto the same contract; define Conan's native artifact state model.
2. **Complete native format behavior.** Finish OCI catalog/referrers and
   browsing; Raw listing, checksums, and resumable uploads; Maven browse/search
   and publication companion; Conan Hosted publication, deletion, metadata,
   search, and lifecycle.
3. **Ship repository experience APIs.** Introduce the versioned management
   surface for artifacts, tombstones, retention, capabilities, and job status.
4. **Ship distribution workflows.** Implement promotion first, then
   checkpointed replication with verification and retry.
5. **Close operational readiness.** Apply quotas and concurrency controls to
   repository operations, then extend backup/restore, observability, preflight,
   evidence, and release gates to all new state transitions.

## Current Checkpoint

The shared state model, OCI tombstone vertical slice, durable lifecycle-job
store, and OCI reclaim worker are complete. The active work package remains
**1: finish lifecycle foundation**: migrate Maven and Raw deletion/reclamation
onto the same repository-scoped lifecycle-job boundary, then define Conan's
native artifact state model.

## Constraints

- No generic upload abstraction replaces protocol-native publication flows.
- No physical deletion bypasses a Tombstone, grace period, and reference check.
- No new package ecosystem is admitted before its lifecycle and protocol
  contract has an owner, migration plan, and black-box client test plan.
- No compatibility break is introduced in `/api/v1`, `/api/v2`, or existing
  protocol response semantics; incompatible management behavior uses a new
  versioned surface.
