# Full Artifact Repository Roadmap

[简体中文](full-artifact-repository-roadmap.zh-CN.md) | [Documentation index](README.md)

The authoritative delivery objective and completion criteria are in
[Full Artifact Repository V1 Goal](full-artifact-repository-goal.md). This
document records architecture sequencing and implementation status.

## Product Goal

Artifact Gateway is becoming a complete artifact repository for its supported
formats, beginning with OCI, Maven, Raw, and Conan. A format is complete only
when it has Hosted, Proxy, and Group behavior; safe publication and retrieval;
browsing and search; logical deletion and retention; promotion and replication;
repository-level authorization, quotas, audit, and recovery evidence.

This is a strategic supersession of the read-only gateway boundary. Existing
protocol and `/api/v1`/`/api/v2` contracts remain compatibility commitments;
new lifecycle management belongs in a versioned management API and additive
schema migrations. The preflight and evidence work remains release
infrastructure, not the primary product goal.

## Completion Rules

An artifact becomes visible only through its format's **Publication** boundary.
Bytes are content-addressed and verified before publication. An Artifact is
never overwritten in place: a mutable protocol reference may move, but it must
continue to point to an immutable Artifact. Deletion creates a **Tombstone**;
the **Orphan Collector** reclaims bytes only after its grace period and a
reference recheck. Promotion and replication create auditable destination
artifacts and never mutate the source.

## Delivery Sequence

1. **Lifecycle foundation.** Establish common repository capability metadata,
   artifact/asset state transitions, durable asynchronous jobs, idempotency,
   tombstones, and retention predicates. Keep existing Raw/OCI/Maven behavior
   compatible while moving lifecycle ownership behind narrow modules.
2. **Complete the current formats.** Close the current gaps: native Conan
   Hosted publication/delete/search; Raw listing, checksums, and resumable
   upload; OCI catalog and referrers; Maven browsing/search and a supported
   publication companion. Each format needs black-box client tests for publish,
   resolve, delete, retain, and restore.
3. **Repository experience APIs.** Add versioned management APIs for artifact
   browsing, coordinate search, tombstone inspection, retention execution,
   and bounded asynchronous-job status. Do not retrofit incompatible behavior
   onto V1/V2 endpoints.
4. **Distribution workflows.** Add policy-gated promotion between Hosted
   Repositories, then resumable replication with checkpoints, integrity checks,
   retry policy, and destination authorization.
5. **Production scale.** Add capacity accounting, per-Repository quotas,
   concurrency limits, replication observability, backup/restore coverage for
   every lifecycle state, and release evidence integrated with deployment CI.

## Current Status

All five delivery stages are implemented for OCI, Maven, Raw, and Conan. Each
format has Hosted, Proxy, and Group workflows, a native lifecycle, management
browse where applicable, promotion, checkpointed replication, and operational
coverage in the backend completion checklist. The roadmap is retained as the
architectural sequence; release readiness is now governed by the executable
gates and evidence record rather than a further implementation slice.
