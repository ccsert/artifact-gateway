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
  status through explicit contracts with synchronized server and consumer changes.
- **Distribution:** administrators can promote immutable artifacts between
  Hosted Repositories; replication is resumable, integrity checked, authorized,
  observable, and idempotent.
- **Operations:** repository quotas, capacity, concurrency limits, audits,
  metrics, backup/restore, upgrade/rollback, release preflight, and production
  evidence cover every lifecycle state and background operation.
- **Native contracts:** package protocols remain standards compliant and are
  regression-tested. Historical management APIs, Nexus URLs, authentication
  models and internal interfaces impose no compatibility requirement
  ([ADR 0009](adr/0009-native-architecture.md)).

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
checkpoint covers the initial four-format V1 scope. It does not close the
broader format, private-upstream or migration backlog. Use the current
[protocol matrix](protocol-compatibility.md) for executable capabilities and
limits, and record the [release readiness](release-readiness.md) gates for each
delivered slice.

## Constraints

- No generic upload abstraction replaces protocol-native publication flows.
- No physical deletion bypasses a Tombstone, grace period, and reference check.
- No new package ecosystem is admitted before its lifecycle and protocol
  contract has an owner, migration plan, and black-box client test plan. The
  executable admission checklist is maintained in
  [Artifact Format Extension Guide](format-extension-guide.md).
- Management changes may break historical interfaces. Change OpenAPI sources,
  server, Console and agctl together, describe the new contract, and remove
  superseded implementations. Standard package protocol correctness and
  byte-integrity guarantees still apply.

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

The format capability API advertises APT Proxy and Group reads until the
minimum Hosted and production-signing gates are complete. See the
[APT Hosted Roadmap](apt-hosted-roadmap.md) for the ordered contract and
acceptance criteria.

## Current Format Expansion

Cargo is now admitted with Hosted, Proxy and Group behavior; the current
[protocol matrix](protocol-compatibility.md) describes its supported scope and
limits. The old Cargo-candidate ordering no longer schedules implementation.

NuGet Hosted publication and native V3 reads are active under #287, followed by
Proxy/Group (#288) and governance/admission (#289). Its existing bounded parser
is reused; the format remains absent from the public capability catalog until
those admission gates pass. See the [NuGet roadmap](nuget-roadmap.md).
