# Full Artifact Repository V1 Goal

[简体中文](full-artifact-repository-goal.zh-CN.md) | [Documentation index](README.md)

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

The V1 backend work packages are complete: lifecycle, format behavior,
management APIs, distribution, and operational controls are tracked as complete
in the [Backend Completion Checklist](backend-completion-checklist.md). The
remaining release decision is evidence, not feature scope: execute the full
[release readiness](release-readiness.md) gate set from a clean checkout and
record the results before approving a deployment.

## Constraints

- No generic upload abstraction replaces protocol-native publication flows.
- No physical deletion bypasses a Tombstone, grace period, and reference check.
- No new package ecosystem is admitted before its lifecycle and protocol
  contract has an owner, migration plan, and black-box client test plan. The
  executable admission checklist is maintained in
  [Artifact Format Extension Guide](format-extension-guide.md).
- No compatibility break is introduced in `/api/v1`, `/api/v2`, or existing
  protocol response semantics; incompatible management behavior uses a new
  versioned surface.

## Next Planned Format: APT Hosted

APT Proxy and ordered Group reads have passed format admission for their current
protocol-only scope. The next format expansion is APT Hosted, delivered through
four explicit milestones rather than by widening the format enum prematurely:

1. publication contract, Debian package identity, persistence, and signing
   boundary;
2. atomic `Packages` and `Release` snapshot generation with real APT client
   tests;
3. trusted signing, key rotation, backup, and operational recovery;
4. lifecycle, `.deb` scanning, quarantine, promotion, and replication parity.

The format capability API continues to advertise APT as Proxy-only until the
minimum Hosted and production-signing gates are complete. See the
[APT Hosted Roadmap](apt-hosted-roadmap.md) for the ordered contract and
acceptance criteria.

## Subsequent Planned Format: Cargo

After the APT H3 production-signing milestone, Cargo is the next candidate
ecosystem. Its first slice now validates official publish frames and bounded
`.crate` archives and freezes the canonical sparse-index identity/checksum
contract. Persisted collision conformance remains before C1, and Cargo is still
not admitted as a public format. See [Cargo repository research](cargo-repository-research.md)
for Hosted, Proxy, immutable Group ownership, lifecycle, and acceptance gates.
NuGet's existing bounded parser remains maintained but its repository roadmap
is deferred rather than a first-priority delivery stream.
