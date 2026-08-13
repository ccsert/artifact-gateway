# Cargo Repository Research And Recommended Roadmap

Status: research recommendation, not an implemented protocol or an admitted
format profile. This document uses only Gitea and Cargo-owned documentation as
protocol evidence.

## Decision

Cargo is worth making the next new ecosystem after APT H3. It should take the
next-format planning position ahead of NuGet, while APT production signing,
recovery, and lifecycle remain the active completion work.

This is an engineering-priority recommendation rather than a claim about
customer demand. The reasons are:

- Cargo defines a native publication API, a download endpoint, search, yank,
  and two documented index protocols. A Hosted implementation does not need an
  invented publication companion.
- One published version owns one immutable `.crate` archive and one index row,
  with a server-computed SHA-256 checksum. That is a smaller initial publication
  and restore surface than a format whose client discovers many metadata
  resources.
- Gitea demonstrates that the stock Cargo client can publish, add, install,
  yank, unyank, and search against a private registry. Its current guidance
  prefers a sparse index while retaining a Git index as a compatibility option
  ([Gitea Cargo registry](https://docs.gitea.com/usage/packages/cargo/)).
- The immutable archive is a good fit for Artifact Gateway's digest identity,
  scanning, quarantine, promotion, replication, and RustFS object model.

The main cost is not the `.crate` upload. It is making sparse-index generation,
Proxy source identity, and ordered Group ownership agree for every crate
version. Cargo must remain absent from `Format`, OpenAPI, repository creation,
and the Console until the declared capabilities pass the repository admission
gate in [the format extension guide](format-extension-guide.md).

Primary evidence:

- [Gitea Cargo package registry](https://docs.gitea.com/usage/packages/cargo/)
- [Cargo registries](https://doc.rust-lang.org/cargo/reference/registries.html)
- [Cargo registry index](https://doc.rust-lang.org/cargo/reference/registry-index.html)
- [Cargo Registry Web API](https://doc.rust-lang.org/cargo/reference/registry-web-api.html)
- [Cargo registry authentication](https://doc.rust-lang.org/cargo/reference/registry-authentication.html)
- [Cargo source replacement](https://doc.rust-lang.org/cargo/reference/source-replacement.html)
- [`cargo package`](https://doc.rust-lang.org/cargo/commands/cargo-package.html)
- [`cargo yank`](https://doc.rust-lang.org/cargo/commands/cargo-yank.html)

## Normative Protocol Baseline

A Cargo registry has an index and may advertise a Web API through the index
root's `config.json`. Cargo treats an index URL beginning with `sparse+` as a
sparse HTTP registry; any other remote index URL uses the Git protocol. The
index configuration supplies
([Cargo registries](https://doc.rust-lang.org/cargo/reference/registries.html),
[registry index](https://doc.rust-lang.org/cargo/reference/registry-index.html)):

- `dl`, the `.crate` download base or URL template;
- optional `api`, the Web API base required for publication and other registry
  commands; and
- optional `auth-required`, which tells Cargo to authenticate API, download,
  and sparse-index requests.

The index has one lowercased path per crate and one JSON line per version.
Every line contains the exact SHA-256 `cksum` of the downloadable `.crate`.
Each version may appear only once for a crate, ignoring SemVer build metadata.
After insertion, an index row is immutable except for its `yanked` field.
These are Cargo's
[registry-index requirements](https://doc.rust-lang.org/cargo/reference/registry-index.html),
not Gitea-specific behavior.

The Web API operations needed for the initial Artifact Gateway profile are:

| Operation | Cargo path relative to `api` | Method | Authentication |
| --- | --- | --- | --- |
| Publish | `/api/v1/crates/new` | `PUT` | Included |
| Yank | `/api/v1/crates/{crate}/{version}/yank` | `DELETE` | Included |
| Unyank | `/api/v1/crates/{crate}/{version}/unyank` | `PUT` | Included |
| Search | `/api/v1/crates?q=...&per_page=...` | `GET` | Historically documented as not included |

The publish body is a framed binary payload: little-endian JSON length,
publication metadata JSON, little-endian crate length, then the `.crate`
bytes. The registry computes the checksum; the publish JSON does not supply the
index `cksum`. Cargo permits a successful publish response before the index is
observable and polls briefly afterward
([Cargo Registry Web API](https://doc.rust-lang.org/cargo/reference/registry-web-api.html)).
Artifact Gateway should choose the stronger compatible rule: do not return
success until the immutable download reference and index row are atomically
visible.

The current Cargo documentation says `auth-required: true` covers all API,
download, and sparse-index requests, while the Web API table still labels
search as unauthenticated. Therefore private `cargo search` behavior must be an
official-client acceptance case, not an assumption encoded only from one of
those descriptions
([registry index authentication](https://doc.rust-lang.org/cargo/reference/registry-index.html),
[Web API search](https://doc.rust-lang.org/cargo/reference/registry-web-api.html#search)).

## Recommended Artifact Gateway Surface

Use one canonical sparse registry root per Repository or Group, with a trailing
slash:

```toml
[registries.gateway]
index = "sparse+https://gateway.example/cargo/<repository>/"

[registry]
default = "gateway" # optional
```

For CI, configure a Cargo credential provider and inject the token rather than
committing credentials. Cargo sends the token value verbatim in the
`Authorization` header. Since Artifact Gateway protocol tokens use the Bearer
scheme, the Cargo token value is `Bearer <gateway-token>`, matching Gitea's
documented private-registry configuration. With the built-in token provider,
CI may inject `CARGO_REGISTRIES_GATEWAY_TOKEN`
([Cargo registry authentication](https://doc.rust-lang.org/cargo/reference/registry-authentication.html),
[Gitea credentials example](https://docs.gitea.com/usage/packages/cargo/#configuring-the-package-registry)).

Every Repository type uses the same client-facing read shape:

| Route | Hosted | Proxy | Group |
| --- | --- | --- | --- |
| `GET /cargo/{repository}/config.json` | Generated | Generated | Generated |
| `GET/HEAD /cargo/{repository}/{index-path}` | Generated index row set | Cached upstream row set | Ordered merged row set |
| `GET/HEAD /cargo/{repository}/api/v1/crates/{crate}/{version}/download` | Stored `.crate` | Verified cache/upstream | Owning member |
| `GET /cargo/{repository}/api/v1/crates` | Local search | Cached/upstream search | Merged search |
| `PUT /cargo/{repository}/api/v1/crates/new` | Publish | Rejected | Rejected |
| `DELETE .../{crate}/{version}/yank` | Yank | Rejected | Rejected |
| `PUT .../{crate}/{version}/unyank` | Unyank | Rejected | Rejected |

`config.json` should set `dl` to
`https://gateway.example/cargo/{repository}/api/v1/crates`, set `api` to the
Repository root without a trailing slash, and set `auth-required: true` when
the registry is private. A private sparse endpoint follows Cargo's bootstrap
flow: an unauthenticated `config.json` request receives `401`; Cargo retries
with its credential, and the authenticated configuration declares
`auth-required: true`. A `WWW-Authenticate: Cargo login_url="..."` challenge
may direct an operator to token acquisition
([Cargo sparse authentication](https://doc.rust-lang.org/cargo/reference/registry-index.html#sparse-authentication)).

The Cargo Web API also specifies owner-management endpoints. They are not
needed in the first profile because Artifact Gateway Repository grants remain
the authorization source. `cargo owner` should be explicitly documented as
unsupported until a deliberate mapping to Repository principals exists.

### Hosted

The canonical immutable identity is the Repository, a collision-safe crate-name
key, the SemVer version key with build metadata excluded for uniqueness, and
the computed `.crate` digest. Preserve the published display name and version,
but reject case-insensitive and `-`/`_` confusable name collisions as Cargo's
index guidance recommends.

Publication should:

1. Decode both publish lengths with strict metadata, compressed-size, expanded-
   size, file-count, path, and time bounds.
2. Stream the `.crate` to a digest-addressed staged RustFS object while
   computing SHA-256.
3. Inspect the compressed package with the same bounded-archive discipline as
   other native formats, and cross-check the normalized `Cargo.toml` identity
   against the publish metadata. `cargo package` documents that Cargo creates a
   compressed `.crate` and rewrites the manifest for distribution
   ([`cargo package`](https://doc.rust-lang.org/cargo/commands/cargo-package.html)).
4. Translate publish dependency fields to the registry-index schema exactly;
   the two schemas deliberately differ for renamed dependencies, version
   requirements, features, and checksum ownership
   ([publish-to-index differences](https://doc.rust-lang.org/cargo/reference/registry-index.html#json-schema)).
5. In one metadata transaction, reserve quota, commit the immutable artifact
   reference, insert the index version, write audit state, and make both index
   and download resolution visible.

Cargo does not send an idempotency key. An exact retry can still be recognized
by actor, Repository, normalized coordinate, and body digest. The same
coordinate and digest should return the committed result; a changed digest for
an existing version must fail as an immutable-coordinate conflict.

Yank is not deletion. It changes only the index `yanked` field, prevents new
resolution from selecting that version, and leaves the `.crate` downloadable
for existing lockfiles. Unyank reverses only that flag. A Tombstone remains a
separate Artifact Gateway lifecycle transition
([`cargo yank`](https://doc.rust-lang.org/cargo/commands/cargo-yank.html)).

### Proxy

A sparse Proxy should cache upstream `config.json`, per-crate index responses,
and `.crate` bytes under format-owned keys. Before publication to its local
cache, it must verify the downloaded bytes against the selected index row's
SHA-256 checksum. Conditional refresh should preserve upstream `ETag` or
`Last-Modified`; Cargo itself uses those validators and `304` for sparse-index
caching. Negative caching must distinguish the index protocol's documented
`404`, `410`, and `451` absent responses
([Cargo sparse caching and absence](https://doc.rust-lang.org/cargo/reference/registry-index.html#sparse-protocol)).

The Gateway owns the client-facing `config.json` and rewrites its `dl`, `api`,
and authentication fields to Gateway routes. It must not silently rewrite a
crate version, checksum, or explicit dependency registry. An authenticated
upstream credential is server-side configuration and must never appear in the
client response, audit details, or cache keys.

Cargo source replacement has a strict equivalence assumption: the replacement
contains exactly the same source code and cannot add crates absent from the
original source. A crates.io mirror should therefore point only to a dedicated,
checksum-preserving Proxy
([Cargo source replacement](https://doc.rust-lang.org/cargo/reference/source-replacement.html)):

```toml
[registries.gateway-crates-io]
index = "sparse+https://gateway.example/cargo/crates-io-proxy/"

[source.crates-io]
replace-with = "gateway-crates-io"
```

A mixed private Group is an alternate registry, not a valid crates.io source
replacement.

### Ordered Group

A Group is a synthetic read-only registry. It generates its own `config.json`
and merges the requested crate's index rows from active members. Ownership is
claimed by normalized crate name plus version-uniqueness key. If current
members expose identical immutable index data and checksum, the Group may
de-duplicate it. If their checksum or any immutable index field differs, Group
creation/member replacement or the new publication must report a conflict; it
must not select different bytes merely because member order changed.

The first successful exposure records an immutable Group claim containing the
owner, index-row digest, and `.crate` digest. Reordering or adding members must
not change that claim. Removing or tombstoning the owning member needs a
deliberate migration/tombstone rule and cannot silently repoint the same Group
coordinate. This is stricter than the ordinary dynamic Group rule because
Cargo's index permits only `yanked` to change after a version row is added
([Cargo index immutability](https://doc.rust-lang.org/cargo/reference/registry-index.html#json-schema)).

The claim owner supplies the index row, `.crate` download, search result,
quarantine decision, and failure outcome. A checksum mismatch, authorization
denial, or quarantined claimed identity must not fall through to another
member.

The merged index and download resolver must share one ownership function. If
they decide independently, Cargo may select the checksum from one member and
download bytes from another. Group search de-duplicates with the same owner
rule. Publish, yank, and unyank are rejected because Group state is read-only;
those mutations target a Hosted member directly.

## Sparse Index Versus Git Index

Artifact Gateway should ship sparse-only and make its sparse URL canonical.

- Cargo's sparse protocol fetches only relevant metadata files over HTTP and
  can save substantial time and bandwidth; HTTP/2, `ETag`/`Last-Modified`, and
  explicit invalidation are documented performance mechanisms
  ([Cargo sparse protocol](https://doc.rust-lang.org/cargo/reference/registry-index.html#sparse-protocol)).
- A Git index would add Git smart-HTTP, repository maintenance, ref updates,
  compaction, repair, and a second publication consistency boundary. Gitea can
  justify that because it already owns Git repositories; Artifact Gateway does
  not.
- Cargo warns that a registry URL is recorded in the lockfile and recommends
  one canonical protocol when a registry is not crates.io. Offering sparse and
  Git URLs as peers creates different source identities even when their rows
  match
  ([Cargo sparse limitations](https://doc.rust-lang.org/cargo/reference/registry-index.html#sparse-limitations)).

A Git index should be reconsidered only after measured compatibility demand
shows that the supported Cargo client baseline cannot use sparse. It must then
be a separate accepted contract with one canonical URL and an explicit
migration/source-replacement story, not a hidden alias.

## Lifecycle And Governance Fit

| Artifact Gateway concern | Cargo mapping and required rule |
| --- | --- |
| Artifact identity | One crate name/version plus immutable `.crate` SHA-256; version uniqueness ignores build metadata. |
| Publication scanning | Scan the committed `.crate` by coordinate and digest. Publication metadata may seed descriptive fields, but scanner-owned SBOM, licenses, and vulnerabilities remain independent evidence. |
| Manual scanning | Select a visible crate version from browse/search; never require an operator to type an object key. |
| Security admission | Evaluate the immutable source digest before promotion. `autoScanOnPublish` remains asynchronous and must not falsely imply that `cargo publish` was synchronously blocked. |
| Quarantine | Keep lifecycle visibility unchanged; when read enforcement is enabled, omit the version from sparse index/search and deny its download. Ordered Group must not fall through. |
| Retention | Never treat Cargo yank as deletion. Hosted should default to no automatic removal of published `.crate` versions because deletion breaks existing lockfiles. Reclaim failed/staged uploads and bounded Proxy cache safely; any destructive Hosted retention must be explicit, previewable, tombstoned, restorable, and strongly warned. |
| Promotion | Move one immutable `.crate` plus its exact index metadata as the atomic unit. Same destination coordinate/digest is idempotent; different digest conflicts. Reject or explicitly preserve a yanked source state, and optionally require that same-registry dependencies are reachable at the destination. |
| Replication | Copy bytes by digest, verify SHA-256, then publish the destination index row. Checkpoint byte and metadata completion separately and never expose an index row first. |
| Search and browse | Use original display identity plus normalized collision key; deep-link directly to Repository, crate, version, and digest. |
| Audit and authorization | Cargo mutation errors use Cargo's `{"errors":[{"detail":"..."}]}` shape, while management operations retain `application/problem+json`. Repository grants, not Cargo owner records, are authoritative initially. |

Cargo's own publishing guidance treats a crates.io release as permanent, and
`cargo yank` explicitly preserves bytes for existing lockfiles. Gitea permits
delete-and-republish through its package UI, but that product choice should not
weaken Artifact Gateway's immutable-coordinate rule
([Cargo publishing](https://doc.rust-lang.org/cargo/reference/publishing.html),
[`cargo yank`](https://doc.rust-lang.org/cargo/commands/cargo-yank.html),
[Gitea Cargo registry](https://docs.gitea.com/usage/packages/cargo/#publish-a-package)).

## Recommended Delivery Roadmap

### C0: frozen contract and byte parser

- Freeze the sparse-only route, canonical identity, collision policy, publish
  framing limits, Cargo error envelope, and private-auth behavior.
- Add a bounded `.crate` reader and publish-metadata-to-index translator without
  adding Cargo to the public format catalog.
- Generate fixtures only with the official `cargo package` and `cargo publish`
  clients; retain malformed framing, gzip/tar expansion, duplicate path,
  traversal, metadata mismatch, renamed dependency, feature, SemVer build
  metadata, and checksum vectors. Cover current index fields including
  `features2`, schema `v`, `rust_version`, and `pubtime`; Gitea is a client-flow
  reference, not a substitute for Cargo's current index schema.

Exit: parser/property tests and Memory/PostgreSQL identity conformance pass;
malformed bytes never create an object intent or visible index entry.

### C1: Hosted sparse registry

- Implement idempotent atomic publication, sparse `config.json` and crate index
  files, immutable downloads, search, yank/unyank, private/public reads,
  capacity, quota, audit, browse, and stable deep links over PostgreSQL and
  RustFS.
- Generate index responses from committed records; never expose client-owned
  index files as mutable storage.
- Add exact-retry recovery across process failure between object upload and
  metadata publication.

Exit: a clean official Rust image performs `cargo publish`, `cargo add`,
`cargo install`, `cargo search`, `cargo yank`, and `cargo yank --undo`; staged
versions are never observable and a previously resolved lockfile still
downloads a yanked version.

### C2: checksum-preserving Proxy

- Implement sparse upstream fetch, checksum-gated `.crate` caching,
  conditional/negative cache behavior, redirect and egress protection,
  authenticated upstreams, and sanitized diagnostics.
- Prove a dedicated crates.io Proxy as an exact Cargo source replacement; keep
  mixed private Groups configured as alternate registries.

Exit: online and upstream-offline `cargo build/install` succeed from verified
cache; corrupted upstream bytes never become visible; a real crates.io source
replacement resolves an application without source-identity drift.

### C3: immutable ordered Group

- Implement Group index merge, search merge, and download resolution through
  one persisted identity-claim function.
- De-duplicate only identical immutable index rows and checksums. Reject
  conflicting member admission or publication instead of changing content at
  an already exposed Group coordinate.
- Validate member reordering, removal, tombstone, quarantine, authorization,
  and failure against the persisted claim, with no lower-member fallback.

Exit: a private Hosted-plus-Proxy Group works as an alternate registry; member
collisions are explicit; repeated resolution, process restart, and member
reordering preserve the same index-row and `.crate` digests. The mixed Group is
demonstrably rejected as a crates.io source replacement configuration.

### C4: lifecycle, intelligence, and distribution parity

- Add Tombstone, restore, safe format-specific retention defaults, delayed
  reclaim, manual and automatic scan selection, quarantine read enforcement,
  immutable promotion, checkpointed replication, webhooks, metrics, backup,
  restore, and upgrade evidence.
- Make promotion admission reason over crate coordinate/digest and preserve the
  index/download publication ordering at every destination.
- Add Console creation and lifecycle workflows only with the final truthful
  capability profile.

Exit: lifecycle/security/distribution tests pass against Memory,
PostgreSQL/RustFS, workers, and official Cargo clients. Only then may Cargo be
advertised as supporting Hosted, Proxy, and Group.

## Acceptance Matrix

| Capability | Hosted | Proxy | Group | Required black-box evidence |
| --- | --- | --- | --- | --- |
| Sparse config/index | Generated from committed rows | Rebased Gateway config plus cached rows | Generated ordered merge | `cargo metadata` and dependency resolution |
| Publish atomicity | Required | N/A | N/A | Interrupted and exact-retry `cargo publish`; no partial visibility |
| Download/checksum | Stored digest must equal index `cksum` | Upstream bytes verified before cache visibility | Selected owner checksum and bytes agree | Corruption rejection, GET/HEAD, ranges, ETag |
| Search | Local | Upstream/cache | Ordered de-duplicated | `cargo search --registry gateway` public and private cases |
| Yank/unyank | Mutable flag only | N/A | N/A | New resolution excludes yank; existing lockfile still downloads; undo restores selection |
| Authentication | Publish and optional private reads | Gateway auth plus isolated upstream credential | Group and member read gates | Missing, invalid, read-only, and publish token cases |
| Proxy replay | N/A | Required | Through Proxy member | Resolve after upstream shutdown from RustFS |
| Ordered collision | N/A | N/A | Required | Same crate/version/different immutable data conflicts; membership changes preserve existing claims |
| Retention/restore | Safe no-delete default; explicit tombstone/restore | Bounded cache reclaim/refetch | Member-derived | Dry-run, tombstone, restore, delayed reference-checked reclaim |
| Scanning/quarantine | Immutable `.crate` identity | Cached artifact identity | Owner identity | Manual/automatic scan and index/download read enforcement |
| Promotion/replication | Source and destination | N/A | N/A | Same-digest idempotency, changed-digest conflict, resume, checksum, admission |
| Recovery/upgrade | Required | Required | Required | PostgreSQL/RustFS backup restore, migration no-op/checksum, real client replay |

This matrix is the minimum format-admission evidence. Unit tests or a synthetic
HTTP client do not replace the official `cargo` fixture.
