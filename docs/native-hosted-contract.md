# ADR: Native Hosted Domain and Management API Contract

Status: accepted for Native Hosted Platform V3 planning. This document freezes
the implementation contract; it does not make the endpoints available in the
current V2 binary.

## Scope and terms

A **Repository** is the durable policy, authorization, retention, and storage
namespace for exactly one format (`raw`, `oci`, or `maven`). A Repository has
one Hosted source. A **Group** is an ordered, read-only view of Repository
members of the same format; it does not own artifact bytes. A **member** is the
membership edge between a Group and a Repository, with a unique position. An
**artifact coordinate** is the immutable identity used by its format: a Raw
canonical path, an OCI `repository@sha256:<digest>` (tags are mutable refs), or
a Maven `groupId:artifactId:version[:extension[:classifier]]`.

A **publish session** is the only way to create Hosted bytes. It stages a
format-specific coordinate and declared objects, accepts resumable object
uploads, verifies every declared digest and size, then atomically promotes the
coordinate. Sessions expire and cannot be reused after `committed`, `aborted`,
or `expired`. `Idempotency-Key` scopes a create request to its authenticated
actor and request target for 24 hours; a reused key with a different payload
returns `409 idempotency_conflict`.

The session creation body is a discriminated contract: Raw supplies `format:
raw`, a canonical path, and declared object; OCI supplies `format: oci`, a
manifest digest, declared blobs, and optional tag; Maven supplies `format:
maven`, a complete coordinate, required POM object, and components/checksums.
The repository format must match this body. Upload and commit are the shared
write protocol; protocol reads expose none of these objects before commit.

Group membership is owned and mutated only by the Group. The management
collection is `/groups/{groupId}/members`; each replacement supplies the full
ordered member list with `If-Match` for the Group version. A replacement is
rejected unless every Repository exists, has the Group's format, appears once,
and has a unique contiguous position beginning at zero. The service changes the
membership edges and Group version in one PostgreSQL transaction. Repository
responses may embed this read model, but no Repository member-write endpoint
exists.

This contract covers native Hosted Raw, OCI, and Maven. Conan remains V2
read-through only. Group resolution, external Proxy, and read-through cache
continue to obey `docs/v2-contract.md`.

## Metadata, object lifecycle, and transactions

PostgreSQL is authoritative for Repository, Group, member, principal grant,
retention policy, publish session, artifact coordinate, object reference, and
audit rows. S3-compatible storage holds immutable byte objects only, under a
digest-addressed key (`native/sha256/<hex>`); object keys are never client input.
Redis supplies short leases and idempotency coordination only. Loss of Redis
must fail closed for the affected concurrent operation, never change durable
truth.

Object upload precedes metadata promotion because PostgreSQL cannot join an S3
transaction. The service first records a `staging` object intent in PostgreSQL,
uploads the digest-addressed object, verifies size and digest by a read/HEAD,
then runs one PostgreSQL transaction that locks the publish session and target
coordinate, inserts object references, changes the coordinate to visible, and
marks the session `committed`. A reader serves an object only through a visible
coordinate and committed reference. Thus an uploaded-but-uncommitted object is
unreachable; a committed coordinate never points to an unverified object.

Replacing an immutable coordinate is rejected with `409 coordinate_exists`.
OCI tag updates create a new immutable manifest coordinate and atomically move
the tag reference in that same transaction. Deletion is logical: it writes a
tombstone and removes the coordinate from resolution. Retention is evaluated
against visible coordinates, never against a bare S3 listing.

The orphan collector runs after a configurable grace period (minimum 24 hours).
It deletes a staged object only when no live object reference, nonexpired
session, or active lease names its digest; it rechecks this predicate in a
PostgreSQL transaction immediately before S3 deletion. A failed delete is
retryable. An S3 deletion that succeeds before the final metadata update leaves
a tombstoned, non-readable reference and is repaired by the next collector run.
No API exposes direct object keys.

## Authorization, errors, pagination, and compatibility

Management endpoints require a bearer access token. `repositories:read` permits
list/get; `repositories:write` permits repository, member, policy, publish, and
deletion changes; `repositories:admin` permits grants and retention changes.
Repository grants can narrow those scopes. Raw/OCI/Maven protocol reads and
writes use their protocol authentication contracts, but must resolve to the
same principal and Repository authorization policy.

Management API operations are Bearer-only. Protocol read operations override
that global management security declaration: Raw and Maven accept HTTP Basic
with the resolver token or a Gateway Bearer token, while OCI uses the Registry
Bearer token exchange and its `WWW-Authenticate: Bearer` challenge. A protocol
route also permits an unauthenticated request only when its Group and resolved
Repository both allow anonymous reads; otherwise Raw and Maven return a Basic
challenge. A generated management client must never infer Bearer-only security
for a protocol route from the management API default.

All management failures use `application/problem+json` with the fields in the
OpenAPI `Problem` schema. `code` is stable machine-readable API surface;
`message` is safe for an operator; `requestId` correlates audit data. Unknown
fields are rejected on writes. Responses may add fields. Existing fields do not
change meaning or type within `/api/v2`; incompatible changes require `/api/v3`.

Collection endpoints use opaque, URL-safe `pageToken` and bounded `pageSize`
(default 50, maximum 200). Results are sorted by immutable `id`; a token
captures the sort position, not a database offset. `nextPageToken` is omitted
at the end. A token used for another endpoint or after its 15-minute lifetime
returns `400 invalid_page_token`.

## Native formats and protocol contracts

Protocol routes are distinct from the authenticated management API and use the
protocol principal mapped to the same Repository grant policy. A session's
declared objects remain unreadable until its commit transaction succeeds; every
protocol route returns `404` for an absent, staged, expired, aborted, or deleted
coordinate. The versioned OpenAPI file carries these exact routes and response
shapes.

**Raw.** `GET /raw/{repository}/content/{path}` reads a committed immutable
object. `path` is a gateway catch-all rather than a single URL segment, so a
canonical path such as `releases/acme/app-1.2.3.tar.gz` remains a multi-segment
path; it rejects empty, directory, dot, dot-dot, and percent-encoded segments.
An unauthenticated request returns `401` plus `WWW-Authenticate`; a session
must commit before the same path returns `200` with bytes.

**OCI.** `GET /v2/{name}/manifests/{reference}` reads a committed OCI manifest
by immutable `sha256:` digest or mutable tag and returns `Docker-Content-Digest`.
`GET /v2/{name}/blobs/{digest}` returns a committed blob by digest. OCI uses a
Bearer `WWW-Authenticate` challenge on `401`. Manifest/tag resolution and blob
reads are invisible before commit; a tag move atomically changes only the tag
reference while preserving immutable manifest coordinates.

**Maven.** `GET /repository/maven/{repository}/{assetPath}` reads a canonical
multi-segment Maven asset path, for example
`org/acme/widget/1.2.3/widget-1.2.3.pom` or its component/checksum. A Maven
coordinate commits its POM and declared component objects together; snapshot
and repository metadata are generated from committed coordinates, never
accepted as client-owned mutable metadata. Uncommitted POM, component, checksum,
and generated metadata paths are all non-readable.

Raw accepts a canonical non-directory path and immutable byte digest. OCI
accepts manifest publication by digest, then optional tag movement. Maven
accepts a complete coordinate plus POM and component/checksum objects. Direct
bucket access, arbitrary Maven metadata writes, OCI digest deletion,
cross-repository copies, and Gitea package administration are non-goals.

## Gitea retirement boundary

`GiteaClient` remains an implementation of the V2 Hosted read adapter for the
existing OCI/Maven Groups. Native Hosted handlers do not call it and do not
read Gitea's database, object store, or package API. Migration is per
Repository: create a native Repository in shadow-read mode, import and verify
coordinates into native storage, compare protocol reads by digest, switch the
Group member from the Gitea adapter to the native Repository in one membership
transaction, retain Gitea read fallback for the defined rollback window, then
remove the fallback. Gitea package deletion occurs only after the retention
window and an operator-approved inventory match. No automatic reverse sync is
provided.

## Executable contract

`api/openapi/native-hosted-v1.json` is the versioned management and protocol
contract source of truth. `go test ./contracts` parses and validates OpenAPI
references, then checks group membership and Raw/OCI/Maven lifecycle fixtures.
`make api-contract` runs that check; CI invokes the same target before the full
test suite.
