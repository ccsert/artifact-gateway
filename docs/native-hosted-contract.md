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

### Maven coordinate publication

Maven and Gradle do not define a portable transaction-complete request across
their independent POM, primary artifact, attached-artifact, checksum, and
metadata uploads. `maven-metadata.xml`, a checksum sidecar, a last observed
request, and a quiet period are therefore not commit signals: each can be
absent, reordered, retried, or written before a failed attached artifact. The
Gateway never infers publication completion from standard HTTP traffic.

The production flow retains standard Maven repository URLs and HTTP `PUT`
uploads. A publish-authorized `PUT /repository/maven/{repository}/{assetPath}`
opens or appends to the publisher's one `open` session for the server-derived
`repository + groupId:artifactId:version` coordinate. The Gateway derives the
canonical asset name, byte digest, size, and coordinate from the path and bytes,
records an intent before S3 upload, and returns `201` while the object is staged.
Client checksum sidecars are assertions only: the Gateway verifies or discards
them and generates readable checksums from verified primary objects. Client
repository and snapshot metadata are accepted only as compatibility no-ops;
readable metadata is generated from visible coordinates.

An optional Gateway Maven extension and Gradle plugin are required for atomic
visibility. After their normal deploy uploads succeed, they call
`POST /repository/maven/{repository}/coordinates/{coordinate}:commit` with an
idempotency key and expected non-metadata asset names. The extension is a
transport companion, not a replacement repository protocol: resolution and
uploads remain standard Maven HTTP. A deploy without it may stage and resume
uploads but cannot become visible; it expires rather than silently publishing a
partial coordinate. This is an intentional compatibility limit because standard
Maven clients expose no universal finalization hook.

The commit caller must be the session publisher or a narrowly authorized release
principal. Its expected-name list is an incompleteness assertion only; it never
supplies a digest, size, metadata, object key, or visibility decision. In one
PostgreSQL transaction, the Gateway locks the open session and coordinate,
confirms the POM parses to the requested coordinate, confirms every expected
asset is verified and staged, derives checksum and metadata references, rejects
an already-visible immutable release coordinate, inserts all references, makes
the coordinate visible, and marks the session `committed`. This transaction is
the trusted server-controlled commit signal. Readers consult only committed
references, so every POM, artifact, checksum, and metadata path is `404` until
commit succeeds.

Commit is idempotent for 24 hours per publisher, repository, coordinate, and
`Idempotency-Key`: an identical retry returns the committed artifact, while a
changed expected-name set returns `409 idempotency_conflict`. Concurrent commits
and duplicate uploads serialize on the session and coordinate locks. A missing
POM, unverified object, unexpected duplicate asset, coordinate conflict, or
malformed POM leaves the session open and invisible and returns `422` or `409`.
Expired sessions return `409 session_expired` and require a fresh upload session.
Promotion never overwrites a release; snapshots create a new immutable
timestamped coordinate and move generated metadata in the same transaction.
Rollback before visibility is abort plus orphan collection. After visibility it
is a logical tombstone or a new promotion under retention policy, never mutation
or deletion of S3 bytes.

PostgreSQL owns the session, derived asset inventory, object intent, verified
reference, idempotency record, and commit lock. S3 stores bytes only at
server-derived digest keys. A failed S3 upload has no verified object and cannot
commit; a failed PostgreSQL promotion leaves unreachable, retryable staged bytes.
Expiration or abort removes intents. The collector claims only unreferenced
intents older than 24 hours with `FOR UPDATE SKIP LOCKED`, rechecks in the
claiming transaction that no committed reference exists, then deletes the S3
byte and finalizes the intent. Gitea is not consulted by this path.

### CCS-44 implementation sequence

1. Make the Maven protocol `PUT` adapter derive a coordinate from a canonical
   asset path, reuse the single open PostgreSQL session for that publisher and
   coordinate, append the verified object intent, and return staging success.
   The more-specific `coordinates/{coordinate}:commit` route must take
   precedence over the catch-all asset route.
2. Implement the commit adapter behind one `CommitMavenCoordinate` module. It
   owns session locking, POM identity parsing, expected-name validation,
   idempotency replay, release/snapshot conflict policy, generated metadata and
   checksum references, and the one promotion transaction. Protocol handlers
   must call this interface rather than reproduce its checks.
3. Ship the Maven extension and Gradle plugin with an opt-in repository flag.
   During migration, existing clients can upload to staging but cannot publish;
   enable atomic publication only after the extension is configured. Keep GET
   resolution unchanged, so already committed Gitea or native coordinates remain
   standard-client-readable.
4. Add black-box Maven and Gradle fixtures for partial POM/JAR/sidecar failure,
   metadata/checksum retry, identical and conflicting commit retries, concurrent
   commit, session expiry/restart, and an S3-success/PostgreSQL-failure retry.
   Add PostgreSQL integration coverage for the 24-hour `SKIP LOCKED` collector
   and its reference recheck.

Non-goals for CCS-44 are guessing a completion event for unmodified clients,
making client metadata authoritative, cross-coordinate transactions, and a
Gitea runtime fallback in the write path.

Raw accepts a canonical non-directory path and immutable byte digest. OCI
accepts manifest publication by digest, then optional tag movement. Maven
accepts a complete coordinate plus POM and component objects followed by the
explicit Maven commit signal. Direct
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
