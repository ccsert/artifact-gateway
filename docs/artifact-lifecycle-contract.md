# Artifact Lifecycle Contract

## States

Every Artifact follows one of three lifecycle states:

| State | Reader visibility | Allowed transitions |
| --- | --- | --- |
| `staged` | Not resolvable | `visible`, collector reclaim |
| `visible` | Resolvable through its native protocol | `tombstoned` |
| `tombstoned` | Not resolvable | `visible` through management restore before collection, collector reclaim |

Publication is the only `staged -> visible` transition. It verifies bytes and
writes the format coordinate/reference in one metadata transaction. A mutable
protocol reference may be repointed only to a different immutable visible
Artifact. Tombstoning removes a visible coordinate/reference and records its
former identity; it never synchronously removes its byte object. Management
restore is the only `tombstoned -> visible` transition and succeeds only while
every required byte object remains recoverable. A collector makes that
transition unavailable once it reclaims an unreferenced object. Tombstoned Go
Module bytes continue charging physical Repository capacity during the 24-hour
recovery window; successful delayed collection releases that capacity.

Quarantine is not a fourth lifecycle state. It is a Repository-local,
versioned governance record over the immutable repository/format/coordinate/
digest identity. A quarantined Artifact remains lifecycle-visible, may still
be tombstoned or reclaimed normally, and remains quarantined after restore.
Quarantine always blocks Promotion and Replication. Protocol reads remain
compatible while the Repository's independent quarantine-read policy is
disabled; when that versioned policy is enabled, GET/HEAD is denied and
protocol metadata hides the quarantined distribution without changing its
lifecycle state. Release changes the governance decision and restores reads.
For Conan, the quarantine identity is the recipe revision because the recipe
and its visible package revisions are promoted and replicated atomically;
package revisions remain independent lifecycle and scanner identities.

## Go Module Slice

A Go Hosted module version is one immutable lifecycle unit containing exactly
one `.info`, `.mod`, and `.zip` representation. Repository retention groups
versions by canonical module path, applies minimum/maximum version and age
rules, and writes the same `module@version` tombstone as an explicit management
delete. Protocol reads, Group aggregation, search, and scan identity queries
hide the complete version immediately.

The scheduler admits tombstoned Go references to durable reclaim jobs only
after the default 24-hour recovery window. Each job coordinates on the content
object key, verifies the current tombstone generation, and persists a
`collecting` fence before touching S3. It deletes the byte object only when no
visible Go version shares it, then marks the expired tombstoned references
collected and releases their Repository capacity. Shared visible references
keep the physical object but do not extend the expired reference's restore
window. Tombstone and restore lock all three object keys in stable order;
restore fails closed with `ErrDisabled` once any required representation is
collecting or collected. Publication orphan cleanup uses a separate reclaim
intent and therefore cannot bypass the recovery window.

## Shared Persistence

Migration `000032_artifact_lifecycle.sql` adds two additive records:

- `artifact_tombstones` records Repository, format, coordinate, digest, and
  tombstone timestamp. The unique Repository/format/coordinate key makes a
  tombstone idempotent.
- `lifecycle_jobs` is a durable, idempotent work boundary for retention,
  promotion, replication, and physical reclamation. It supports semantic JSON
  idempotency, atomic pending-to-running claims, and terminal completion or
  failure. This slice does not yet start a worker; existing format collectors
  remain authoritative until a job consumer is introduced.

## OCI Slice

`DELETE /v2/{repository}/{name}/manifests/{digest}` retains its current
Registry V2 response and reader behavior. In the same PostgreSQL transaction it
removes tag references, writes an OCI Artifact Tombstone, releases the object
intent for delayed collection, and removes the manifest metadata. Subsequent
digest or tag reads remain `404`; the tombstone is management/lifecycle state,
not a protocol response.

This slice establishes the deletion pattern for Raw, Maven, and Conan. It does
not yet expose tombstone browsing, retention job execution, promotion, or
replication APIs.
