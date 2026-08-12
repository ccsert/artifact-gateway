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

**Quarantine**:
A versioned, Repository-local governance decision attached to one immutable
Artifact identity. Quarantine blocks Promotion and Replication but does not
change native protocol reads or the Artifact lifecycle state; Release removes
that distribution block without restoring or republishing the Artifact.
For Conan, the distribution identity is the recipe revision and its complete
visible package closure; package revisions remain separate scanner and
lifecycle identities but are not independently quarantinable.
_Avoid_: Tombstone, delete, artifact state

## Distribution And Operations

**Promotion**:
A policy-controlled, auditable creation of a visible Artifact in another
Hosted Repository without changing the source Artifact.
_Avoid_: Move, overwrite

**Promotion Request**:
An idempotent administrative instruction that snapshots one visible source
Artifact and names a target Hosted Repository. It becomes a durable lifecycle
job; it is not the promoted Artifact itself.
_Avoid_: Copy request, move request

**Promotion Snapshot**:
The immutable source identity recorded by a Promotion Request: source
Repository, format, coordinate, and digest. The worker rechecks that this
identity remains visible and is not quarantined before creating the target
Artifact.
_Avoid_: Latest version, source selector

**Replication**:
An asynchronous, checkpointed copy of visible Artifact metadata and bytes to a
configured destination. Its durable plan retains the immutable source
coordinate and digest so the worker can recheck Quarantine before publication.
For aggregate PyPI versions, changed source-file membership parks the plan as
`replication_snapshot_changed`; exact idempotent replay refreshes checkpoints
before any complete-version publication.
_Avoid_: Backup, cache

**Operational Event**:
An immutable fact emitted in the same transaction as the governed state
transition that caused it. The first event set records Artifact quarantine and
release; it is not reconstructed from Audit records.
_Avoid_: Audit poll result, notification attempt

**Webhook Subscription**:
An administrator-managed HTTPS destination, encrypted HMAC secret, enabled
flag, and event-type filter. Disabling it stops creation of new deliveries but
does not erase existing delivery history.
_Avoid_: Callback job, proxy endpoint

**Webhook Delivery**:
The durable, at-least-once attempt state for one Operational Event and one
Webhook Subscription. It has a lease, bounded retries, terminal `dead` state,
and explicit replay while preserving the event identity.
_Avoid_: Operational Event, best-effort callback

**Quarantine Read Policy**:
A versioned Hosted Repository policy that controls whether protocol reads of a
quarantined Artifact remain backward-compatible or fail closed. It is disabled
by default and independent from promotion admission. Enabled Groups do not
fall through past a quarantined higher-priority identity.
_Avoid_: Tombstone, security admission policy

**Retention Policy**:
A versioned rule that determines when visible Artifacts become tombstoned.
_Avoid_: Garbage collection

**Orphan Collector**:
A delayed maintenance process that reclaims bytes no live Artifact, active
publication, or replication lease references.
_Avoid_: Retention job
