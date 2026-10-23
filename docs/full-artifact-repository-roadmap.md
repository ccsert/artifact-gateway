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

This supersedes the read-only gateway boundary. Historical management APIs,
Nexus URL aliases, authentication models and internal interfaces are not
compatibility requirements. Follow [ADR 0009](adr/0009-native-architecture.md):
change OpenAPI sources, server and consumers together, with no parallel legacy
implementation. Native package protocols still follow their standards; byte
integrity, authorization and durable publication remain required. Schema files
are append-only. Preflight and evidence support delivery rather than replace
the product goal.

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
   tombstones, and retention predicates. Move lifecycle ownership behind narrow
   modules with executable native-protocol and persistence contracts.
2. **Complete the current formats.** Close the current gaps: native Conan
   Hosted publication/delete/search; Raw listing, checksums, and resumable
   upload; OCI catalog and referrers; Maven browsing/search and a supported
   publication companion. Each format needs black-box client tests for publish,
   resolve, delete, retain, and restore.
3. **Repository experience APIs.** Add versioned management APIs for artifact
   browsing, coordinate search, tombstone inspection, retention execution,
   and bounded asynchronous-job status. Management changes may break historical
   interfaces; update the contract, server, Console and agctl in the same slice.
4. **Distribution workflows.** Add policy-gated promotion between Hosted
   Repositories, then resumable replication with checkpoints, integrity checks,
   retry policy, and destination authorization.
5. **Repository authorization.** Evaluate repository operations through one
   ordered chain: account state, administrator, global role, managed
   per-repository grants, then legacy static policy. Add permission-filtered
   repository discovery, effective-access explanation, and reusable
   authorization roles and templates.
6. **Production scale.** Add capacity accounting, per-Repository quotas,
   concurrency limits, replication observability, backup/restore coverage for
   every lifecycle state, and release evidence integrated with deployment CI.

## Current Status

All six delivery stages are implemented for OCI, Maven, Raw, and Conan. Each
format has Hosted, Proxy, and Group workflows, a native lifecycle, management
browse where applicable, promotion, checkpointed replication, and operational
coverage in the backend completion checklist. This records the initial V1
architectural sequence, not completion of the broader format backlog. Current
support and limits are described by the [protocol matrix](protocol-compatibility.md);
private upstreams, migration execution and new ecosystems continue under their
own Issues. Executable gates and evidence govern each delivered slice.

Migration adapters now cover Maven release, Raw, OCI, Conan 2, npm and PyPI Hosted; see the [PyPI release-file and selection-metadata boundary](nexus-pypi-migration.md). Helm adds durable closure replication and RubyGems adds yank/recovery, while both remain default-off previews. These slices do not establish complete format or kkRepo parity.
