# Artifact Lifecycle Contract

## States

Every Artifact follows one of three lifecycle states:

| State | Reader visibility | Allowed transitions |
| --- | --- | --- |
| `staged` | Not resolvable | `visible`, collector reclaim |
| `visible` | Resolvable through its native protocol | `tombstoned` |
| `tombstoned` | Not resolvable | collector reclaim only |

Publication is the only `staged -> visible` transition. It verifies bytes and
writes the format coordinate/reference in one metadata transaction. A mutable
protocol reference may be repointed only to a different immutable visible
Artifact. Tombstoning removes a visible coordinate/reference and records its
former identity; it never synchronously removes its byte object.

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
