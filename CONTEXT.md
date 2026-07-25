# Artifact Gateway Domain

Artifact Gateway is a multi-format artifact repository. It stores, governs,
and distributes software artifacts through native package protocols.

## Repository Model

**Repository**:
A durable, format-specific namespace that owns artifact policy, authorization,
and either hosted bytes or a configured upstream source.
_Avoid_: Registry, bucket, remote

**Hosted Repository**:
A Repository whose visible artifacts and metadata are owned by Artifact Gateway.
_Avoid_: Local cache, proxy

**Proxy Repository**:
A Repository that resolves artifacts from one configured, allowlisted upstream
and may retain a read-through cache.
_Avoid_: Mirror

**Group**:
An ordered, format-specific view over Hosted and Proxy Repositories. A Group
does not own artifact bytes.
_Avoid_: Repository group, virtual repository

**Artifact**:
A client-visible immutable version, manifest, or coordinate in a Repository.
Its format determines its canonical identity.
_Avoid_: File, package

**Asset**:
One immutable byte object belonging to an Artifact, such as a Maven JAR, OCI
blob, or Conan package file.
_Avoid_: Artifact file, blob

**Publication**:
The atomic transition that makes a verified staged Artifact visible to readers.
_Avoid_: Upload, commit

**Tombstone**:
The durable record that makes a previously visible Artifact non-resolvable while
allowing deferred reclamation of its bytes.
_Avoid_: Hard delete

## Distribution And Operations

**Promotion**:
A policy-controlled, auditable creation of a visible Artifact in another
Hosted Repository without changing the source Artifact.
_Avoid_: Move, overwrite

**Replication**:
An asynchronous, checkpointed copy of visible Artifact metadata and bytes to a
configured destination.
_Avoid_: Backup, cache

**Retention Policy**:
A versioned rule that determines when visible Artifacts become tombstoned.
_Avoid_: Garbage collection

**Orphan Collector**:
A delayed maintenance process that reclaims bytes no live Artifact, active
publication, or replication lease references.
_Avoid_: Retention job
