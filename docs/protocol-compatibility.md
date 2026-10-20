# Protocol Compatibility Baseline

[简体中文](protocol-compatibility.zh-CN.md)

Status: current-protocol baseline for the Nexus comparison track. This document
covers only the protocols already present in Artifact Gateway: OCI, Maven, Raw,
Conan, npm, PyPI, Go, Cargo, and APT. Its unsupported rows describe the current source baseline, not the long-term
product boundary; see [the full repository roadmap](full-artifact-repository-roadmap.md).

## How to read “compatible”

- **Standard-path compatible** means real ecosystem clients complete the listed read, install, or upload flows. It does not mean that every optional endpoint in the upstream specification is implemented.
- **Gateway extension** means ecosystem reads or basic uploads stay native, while publication, deletion, or management requires an additional Gateway API.
- **Proxy/Group compatibility** covers only the listed read paths. It does not inherit Hosted writes, catalog, referrer, or lifecycle behavior automatically.
- **A passing regression gate** proves the current fixture and client flows. It is not evidence for every client version, authentication mode, or extension combination.
- **Preview or research** is not a public support claim. APT Hosted and NuGet remain in this category.

## Native client roots

Clients use Artifact Gateway's canonical protocol roots. Nexus-style
`/repository/<name>/...` aliases and the Go version-only ZIP upload have been
retired under #385; those old routes return 404. Update repository URLs and
regenerate lockfiles or cached metadata that still contain retired URLs.

| Format | Publication | Reads and Group resolution |
| --- | --- | --- |
| Maven | `/repository/maven/<repository>/` | Hosted: the same root; Proxy/Group: `/maven/<repository>/` |
| npm | `/npm/<repository>/` | `/npm/<repository>/`; generated tarball URLs use this root |
| PyPI | `/pypi/<repository>/legacy/` | `/pypi/<repository>/simple/`; distribution Locations use `/pypi/<repository>/packages/` |
| Raw | `/raw/<repository>/<path>` | `/raw/<repository>/<path>`; listing and resumable Locations use this root |
| Go | `PUT /go/<repository>/<escaped-module>/@v/<version>.zip` | `/go/<repository>` as `GOPROXY` |
| OCI | `/v2/<repository>/<image>/...` | The Registry V2 `/v2/` root |

These roots preserve standard package-client protocols. They do not emulate
Nexus REST APIs, users, permissions, or blob stores. Nexus discovery remains a
read-only migration input; importing data and changing production client URLs
are separate acceptance steps.

## Compatibility matrix

| Protocol | Current support | Explicitly unsupported | Nexus difference | Regression gate |
| --- | --- | --- | --- | --- |
| OCI Hosted / Proxy / Group | Hosted implements Registry V2 at `/v2/<repository>/<image>/...`: resumable blob upload, status/cancel/completion, Hosted-to-Hosted mount, blob GET/HEAD/Range, manifest PUT/GET/HEAD/DELETE, descriptor validation, media-type negotiation, tag movement/pagination, catalog, and referrers. Proxy read-through caches only manifests and blobs, with Basic/Bearer source credentials for direct registries or explicitly bound token issuers; credential rotation/clear isolates token and positive/negative content caches. Group is read-only for manifests, blobs, and tags, with Hosted members preferred. A default-disabled Hosted quarantine-read policy denies quarantined manifest closures and prevents Group fallback. | Direct blob deletion; writes through Proxy/Group; Proxy/Group catalog or referrer aggregation; and a Docker/ORAS black-box matrix beyond the current fixture. | Nexus exposes broader repository administration and browsing. Artifact Gateway implements the pull/push core first, keeps Proxy/Group read-only, and stores content-addressed bytes behind PostgreSQL metadata. | `go test ./internal/app ./internal/repository ./contracts`, `make native-oci-e2e`, and PostgreSQL/RustFS coverage for upload, Proxy cache, Group fallback, object intents, mount ownership, locks, tag metadata, and orphan collection. |
| Raw Hosted / Proxy / Group | Native Raw is rooted at `/raw/<repository>/<path>` and supports authenticated PUT replacement — unconditional, or conditional on `If-Match`/`If-None-Match` (create-only with `*`) evaluated atomically with the reference swap, including resumable completion — GET, HEAD, single and [bounded multipart byte ranges](raw-http-ranges.md), readable `Content-Disposition`, Digest/ETag, DELETE, streaming object reads, Hosted trailing-slash prefix listing, derived `.sha256`/`.sha512` sidecars, and resumable upload with immutable chunks and one-pass completion. A path is a mutable reference; `path + digest` is the immutable snapshot identity. Proxy/Group use HTTPS-allowlisted upstreams, bounded temporary staging, renewable distributed single-flight, streaming cache publication/read, and per-Gateway staging admission. A default-disabled Hosted quarantine-read policy hides paths, sidecars, and Hosted listing entries. | `If-Unmodified-Since` and conditional DELETE, browsing beyond the Hosted prefix projection, and non-HTTP tooling. Deployments still need a temporary volume, free-space monitoring, and hard ephemeral-storage limits. | Nexus Raw Hosted has a broader browse/admin experience. Gateway preserves its ordinary same-path replacement behavior for migration, while keeping incomplete bytes invisible, validating sidecar assertions without duplicate assets, and using metadata-driven deletion plus delayed object reclamation. | `go test ./internal/app ./internal/repository ./contracts`, `make native-raw-e2e`, and integration coverage for large-object streaming, staging admission, HEAD/Range, listing, checksums, resumable completion, conditional writes (concurrent writers on one validator), tombstones, orphan collection, and PostgreSQL/RustFS lifecycle. |
| Maven Hosted / Proxy / Group | Hosted accepts standard Maven/Gradle `PUT /repository/maven/<repository>/<assetPath>` and directly publishes each verified primary asset by default, requiring no companion call. A default-disabled `mavenStrictPublication` policy instead stages one coordinate until explicit `POST /repository/maven/<repository>/coordinates/<coordinate>:commit`. Client checksums and metadata are non-authoritative; Gateway generates both from verified objects. Proxy caches Maven assets; Group is read-only and Hosted-first. See [Maven Hosted publication](maven-hosted-publication.md). | Direct mode cannot guarantee atomic visibility across the independent files of one GAV if the publisher stops part-way. Strict mode requires a Gateway-specific commit. Client-authored metadata authority, cross-coordinate build transactions, mutable release overwrite, and writes through Proxy/Group are unsupported. | Default mode matches ordinary Nexus Hosted's zero-change client threshold and partial-publication trade-off. Strict mode provides stronger per-coordinate visibility than ordinary Nexus Hosted at an explicit integration cost. | `go test ./internal/app ./internal/repository ./contracts`, `make native-maven-e2e`, and fixtures for commit-free Maven/Gradle resolution, strict pre-commit 404, Proxy cache, Group fallback, checksum retries, commit replay/conflict, generated metadata, cursors, expiry, tombstone restore, and reclaim. |
| npm Hosted / Proxy / Group | Standard npm packument, exact package-version metadata, and tarball reads under `/npm/<repository>/<package>`; exact version responses preserve upstream signatures while rewriting tarball URLs, including the route used by Corepack for pinned package-manager installation. npm CLI publication of immutable SemVer versions to Hosted; scoped packages; `latest` and custom dist-tags; anonymous install/audit under repository policy; authenticated `GET /npm/<repository>/-/whoami` identity lookups answered from the Gateway principal subject without repository authorization; package-level browse with exact version deep links. Proxy provides verified metadata and tarball read-through caching, accepts one safe package root with harmless dot segments used by official tarballs, skips unrelated legacy versions that lack modern integrity, removes dist-tags that target skipped versions, performs conditional revalidation and negative caching, serves stale metadata on upstream failure, and applies bounded retry plus a distributed circuit breaker. Group merges Hosted and Proxy versions behind one Registry URL, keeps Hosted-first/member-order conflict semantics, rewrites tarball URLs to the Group, and filters members by grants and anonymous policy. Hosted versions support retention, tombstone/restore, delayed reclaim, promotion, and checkpointed replication. A default-disabled Hosted quarantine-read policy blocks the whole package version, filters packuments/dist-tags, and prevents Group reintroduction. Hosted accepts `npm dist-tag add/rm` after publication (`PUT`/`DELETE /npm/<repository>/-/package/<package>/dist-tags/<tag>`, Repository write) for visible, non-tombstoned, non-quarantined versions; each change commits atomically with its audit record, a tombstone removes tags naming the version, `latest` can be moved but not removed, and tarball bytes and version identity never change. `npm dist-tag ls` works on Hosted, Proxy (cached upstream tags), and Group (first member defining a tag is authoritative); Proxy and Group refuse tag writes. Console shows all tags targeting the selected version. Hosted also accepts native exact/range deprecation and clearing (Repository write), single-version/whole-package unpublish (Repository admin), and existing management restore with revision CAS, atomic actor/version audits, Group tombstone authority and a durable collection fence. Console exposes warnings, writer editing and administrator unpublish; [npm version governance](npm-version-governance.md) defines limits and recovery. | Authenticated upstream registries, Console dist-tag editing, npmjs.com account/time-window policy, and vulnerability-database integration. | Nexus exposes broader npm security integration. Artifact Gateway emphasizes immutable verified caching and explicit egress policy; only Hosted advertises lifecycle mutation. | `go test ./internal/protocol/npm ./internal/app ./internal/repository`, `make native-npm-e2e`, exact-version signature/HEAD/audit/quarantine tests, dist-tag Memory/PostgreSQL contract plus concurrent same-tag move and tag-versus-tombstone race tests, PostgreSQL/RustFS cross-instance cache coverage, migration/capacity/search checks, and real npm CLI Hosted publication, scoped/unscoped `npm dist-tag add/ls/rm` with empty-cache `npm install <pkg>@<tag>`, plus Group online/offline mixed-member install coverage. The gate verifies the official npm 11.6.2 archive against a fixed SHA-512, then publishes scoped/unscoped versions to PostgreSQL/RustFS and proves tag mutations, empty-cache installs, complete tarball size/SHA-256 identity and durable tag state after a fresh Gateway instance. The same fixed-client gate checks scoped/unscoped exact/range warning and clearing, single/whole unpublish, Group refusal to revive tombstones, fresh-instance management restore and all four original tarballs. Shared Memory/PostgreSQL contracts, concurrent publish/delete and metadata CAS, audit rollback, pre-149 upgrade, S3 partial collection/restart and permission revocation are tested. Console checks tags and warning/edit/unpublish states on desktop/narrow light and dark themes, with full Playwright coverage. |
| PyPI Hosted / Proxy / Group | Standard twine multipart uploads at `/pypi/<repository>/legacy/`; PEP 503 HTML and PEP 691 JSON Simple APIs; wheel/sdist core-metadata identity checks; immutable SHA-256 distribution files; real pip installation; anonymous reads; project browse with searchable exact-version deep links. Proxy requires SHA-256 upstream links, verifies archive metadata before caching, and serves cached files offline. Group merges Hosted and Proxy files with Hosted-first conflicts. Hosted versions support [file-level yank/unyank with version-scoped atomic management](pypi-yank.md), retention, tombstone/restore, delayed reclaim, promotion, and checkpointed replication. A default-disabled Hosted quarantine-read policy blocks every file in a quarantined project version, filters Simple metadata, and prevents Group reintroduction. | Authenticated upstream indexes, project metadata APIs beyond Simple, and vulnerability-database integration. | Nexus exposes additional PyPI administration. Artifact Gateway makes archive identity, upstream digests, and egress allowlists mandatory and uses version-scoped lifecycle operations. | `go test ./internal/app ./internal/repository`, `make native-pypi-e2e`, and PostgreSQL/RustFS cross-instance publication, search, yank/CAS/audit, tombstone, and restore coverage; `scripts/pypi-yank-storage-e2e.sh` verifies independent pg_dump/S3 restore. |
| Go Hosted / Proxy / Group | Standard `GOPROXY` reads rooted at `/go/<repository>/<escaped-module>/...`; `@v/list`, `@latest`, `.info`, `.mod`, and `.zip`; module/version escaping; `go.mod` and module ZIP identity validation; SHA-256 content-addressed storage; ETag/HEAD; stale Proxy lists and offline assets; authenticated upstream Proxy credentials (`none`/`basic`/`bearer`, sealed with `GATEWAY_SETTINGS_ENCRYPTION_KEY`, applied only to the Go Proxy fetch and stripped on cross-host redirects); anonymous and resource-prefix grant filtering; Hosted-first Group aggregation; search, capacity, Console version selection, usage snippets, and version deep links. Hosted publication accepts `PUT /go/<repository>/<escaped-module>/@v/<version>.zip`, validates the ZIP against the explicit module/version identity before publication, and derives `.mod` and `.info`, atomically expose all three, preserve identical retries, reject coordinate changes, and schedule publication scanning. Hosted module versions support management tombstone/restore and repository retention by canonical `module@version`; protocol reads, Group aggregation, search, and scan identities hide tombstones immediately. A durable reference-safe collector preserves all three references for a 24-hour recovery window, then fences restore and releases their Repository capacity. A byte object shared with a visible version remains physically stored until its last visible reference disappears. Immutable promotion reuses a verified three-representation snapshot in another Hosted Repository; checkpointed replication copies and verifies all three representations under target-specific object keys before one atomic publication. Both use the ZIP digest as the management distribution identity, reject Proxy targets, and enforce quarantine admission over every representation at request and final worker publication time. A default-disabled Hosted quarantine-read policy hides a quarantined module version from `@v/list` and `/@latest`, blocks every `info`/`mod`/`zip` representation, and prevents Group fallback. Go Proxy repositories mirror one allowlisted checksum database: the go command's `/go/<repository>/sumdb/<name>/supported` probe returns `200` only for names whose host the repository egress allowlist lists, and `/latest`, `/lookup/`, and `/tile/` requests are forwarded over allowlisted HTTPS egress with the repository egress proxy policy applied, passing upstream status codes, content types, and signed tree bytes through verbatim without caching and without attaching the upstream credential. | Client-side `GONOSUMDB`/`GOPRIVATE` scoping and checksum-database availability are the operator's responsibility; a proxy that is unreachable for a mirrored database fails the go command rather than falling back, per the go command's own proxying contract. | Nexus 3.93+ provides Go Hosted and Group repositories. Gateway uses an explicit module/version ZIP upload extension; Nexus leaves checksum-database access to the client, while Gateway Proxy repositories can mirror an allowlisted checksum database for air-gapped or egress-controlled environments. | `go test ./internal/app ./internal/repository`, `make native-go-e2e`, and PostgreSQL/RustFS cross-instance Hosted publication, promotion, replication, retention, tombstone/restore-versus-reclaim serialization, Proxy cache, immutable identity, search, capacity, conflict, and offline-read coverage. |
| Cargo Hosted / Proxy / Group | Sparse index and Cargo Registry API at `/cargo/<repository>/`; Hosted supports immutable `.crate` publication, search, yank/unyank, private/anonymous reads, management tombstone/restore, retention, quarantine, promotion, and checkpointed replication. Proxy verifies upstream checksums before caching and replays cached content offline. Group persists crate-version ownership, rejects conflicting members, and preserves the first owner's bytes through reorder and recovery. | Git index, `cargo owner`, authenticated Cargo upstream Proxy credentials, and writes through Proxy/Group. A mixed private Group must use an alternate registry identity, not crates.io source replacement. | Cargo uses its own sparse registry root rather than a Nexus-style `/repository/<name>/` alias; configure `sparse+` and a trailing slash. See [Cargo client setup](cargo-usage.md). | `make cargo-contract` with Cargo 1.96.0, `make integration-test` on PostgreSQL/RustFS, `make backup-restore-readiness`, and `make cargo-upgrade-readiness`. |
| APT Proxy / Group | Verbatim reads rooted at `/apt/<repository>/...` for `dists/` metadata, signatures, indexes, by-hash objects, and `pool/` packages; SHA-256 object caching; ETag/HEAD; anonymous and resource-prefix grant filtering; ordered Group fallback; capacity, search, Console browse, and source snippets. The non-advertised Hosted H2/H3 preview supports idempotent management staging and atomic snapshot publication, deterministic signed metadata, pinned remote public-key verification, a private-CA TLS option, an operator signing-state API and Console view, bounded signing metrics, Hosted GET/HEAD/range reads, capacity/search projection, exact signed-snapshot recovery, and real Debian install and key-rotation gates. Dedicated operator APIs and Console forms cover signed-snapshot export with offline archive integrity verification, trusted same-repository archive restore, deletion/retention/recovery/pruning, and target-signed promotion and replication. Staged bytes and incomplete snapshots remain absent from protocol reads. | Managed KMS/HSM signer-key custody and key recovery, installed deployment alerts, automatic key distribution, upstream authentication, a generic scheduled retention-policy executor, Console first-publication forms, and APT Group aggregation. | Nexus supports APT through ecosystem-specific repository formats and plugins. Artifact Gateway keeps advertised APT support Proxy/Group-only until H3 production signing passes its gates. | `go test ./internal/app ./internal/aptpublication ./internal/repository ./contracts`, `make native-apt-e2e`, `make apt-signer-rotation-e2e`, and PostgreSQL/RustFS migration, staging, cleanup, signed-snapshot, archive export/restore, distribution, capacity, and search coverage. |
| Conan | Conan 2 canonical remote `/conan/<repository>` with official 2.21.0 create/upload/list/download/install; manifest-last atomic Hosted publication reuses durable recipe/package sessions. Management publication requires the same complete manifest, hash identities and version-fenced atomic commit. Includes independent scoped authentication, Group/Proxy read-through, recipe/package revision delete and restore, delayed reference-safe reclaim, visible-reference browse/search at `/api/v2/repositories/{id}/conan/references`, immutable promotion, and checkpointed replication. A default-disabled Hosted quarantine-read policy blocks the recipe-revision and package-revision closure, hides revision metadata, and prevents Group fallback. | Conan 1, SCM revisions, optional metadata uploads, non-tgz archive formats, general upstream index aggregation, remote-to-remote copying, and package administration beyond immutable revision lifecycle. See [native publication limits](conan-native-publication.md). | Nexus exposes broader Conan administration. Artifact Gateway keeps publication transactional, makes deletion reversible until reclaim, and separates authenticated management browse from the Conan download handshake. | `go test ./internal/app ./internal/repository`, Conan handler tests, PostgreSQL lifecycle/search/replication coverage, reclaim-worker integration evidence, and `make conan-e2e` (pinned official client, Memory plus owned PostgreSQL/RustFS restart, interrupted reupload, full byte and target-only cold-install checks). |

Maven and OCI private upstream credentials, rotation/clear and cache boundaries are
documented in [Proxy egress](proxy-egress-design.md#protected-maven-and-oci-upstreams).
The isolated real-client gate is `scripts/private-upstream-auth-e2e.sh`.
Changing a credential's endpoint or OCI issuer realm/service requires a fresh
secret or explicit `scheme: none`; otherwise PATCH fails with
`400 upstream_credential_rebind_required` and preserves the existing version
and ciphertext. This rule also applies to Go Proxy repositories.

## Contract alignment

- The format-neutral scanner contract applies to immutable artifacts resolved
  by the native Maven, OCI, Raw, npm, PyPI, Go, and Conan stores. Detailed
  findings are persisted with artifact intelligence and do not alter package-
  protocol response bytes. Versioned quarantine is an independent governance
  layer that blocks promotion and replication at request and worker publication
  boundaries. Its independent per-Hosted quarantine-read policy is versioned
  and disabled by default; when enabled it denies protocol reads, filters
  metadata, and makes an earlier quarantined Group member authoritative. A
  Conan recipe revision is the atomic distribution unit and the only Conan
  quarantine anchor; package revisions remain independently scannable.
- Raw and OCI do not use the management publish-session contract. Raw writes are
  ordinary protocol `PUT` requests. OCI writes are ordinary Registry V2 upload
  and manifest requests.
- Maven standard PUT traffic publishes directly by default. A Maven Hosted
  repository can opt in to staging, making the explicit coordinate commit its
  visibility boundary. Conan uses a separate versioned management
  publish-session contract.
- `api/openapi/native-hosted.yaml` and its `components`, `management`, and
  `protocols` fragments are the editable contract source;
  `native-hosted-v1.json` is the generated executable bundle. `go test
  ./contracts` must fail when either form drifts from these protocol decisions.
- `docs/native-hosted-contract.md` remains the architectural contract for
  metadata authority, object lifecycle, idempotency, and deletion semantics.
- README should stay short and describe only the operator-facing protocol roots
  and fixture commands; this document owns the detailed compatibility matrix.

## Normative References And Overlays

| Protocol | Official reference | Gateway overlay |
| --- | --- | --- |
| OCI | [OCI Distribution Specification](https://distribution.github.io/distribution/spec/api/) | `api/openapi/protocols/oci.yaml` limits the Registry V2 surface to the implemented upload, blob, manifest, and tag routes. The handler tests and `make native-oci-e2e` are the executable overlay. |
| Raw | No protocol-wide Raw repository HTTP standard | `api/openapi/protocols/raw.yaml` defines the Gateway route grammar, mutable path-reference and immutable content semantics, and Range behavior; `make native-raw-e2e` is the executable overlay. |
| Maven | [Maven repository documentation](https://maven.apache.org/repositories/index.html) | `api/openapi/protocols/maven.yaml` records direct publication by default and the optional strict Gateway coordinate commit. [Maven Hosted publication](maven-hosted-publication.md) explains both workflows, and Maven/Gradle fixtures enforce them. |
| Conan | [Conan 2 remote documentation](https://docs.conan.io/2/reference/commands/remote.html) | `api/openapi/protocols/conan.yaml` defines the canonical `/conan/{repository}` handshake, reads and manifest-last native PUT workflow; management browse/session and lifecycle APIs remain available. |
| npm | [npm registry API package metadata](https://github.com/npm/registry/blob/main/docs/REGISTRY-API.md) | Hosted accepts npm CLI publication documents and serves authoritative packuments/tarballs. Proxy rewrites and verifies upstream content before caching. Group merges member packuments and keeps every tarball URL on the Group Registry; `make native-npm-e2e` is the executable compatibility overlay. |
| PyPI | [PEP 503](https://peps.python.org/pep-0503/), [PEP 691](https://peps.python.org/pep-0691/), [file yanking](https://packaging.python.org/en/latest/specifications/file-yanking/), and the [PyPA upload API](https://warehouse.pypa.io/api-reference/legacy.html) | Hosted accepts twine multipart uploads after archive metadata verification. Proxy and Group expose digest-pinned Simple links; `make native-pypi-e2e` runs real twine and pip clients through Hosted, Group, and offline Proxy paths. |
| Go | [Go Modules Reference: GOPROXY protocol](https://go.dev/ref/mod#goproxy-protocol) and [Nexus Go CLI usage](https://help.sonatype.com/en/go-cli-usage.html) | Hosted, Proxy, and Group implement the standard read protocol and validate module assets with `golang.org/x/mod`. Hosted uses Gateway's explicit module/version ZIP publication route; `make native-go-e2e` runs real publication and `go mod download` against Hosted and Proxy, then repeats Proxy resolution with a fresh client cache after the upstream stops. |
| Cargo | [Cargo registry index](https://doc.rust-lang.org/cargo/reference/registry-index.html) | `api/openapi/protocols/cargo.yaml` describes the sparse registry route; `make cargo-contract` runs the official client flows. See [Cargo client setup](cargo-usage.md). |
| APT | [Debian Repository Format](https://wiki.debian.org/DebianRepository/Format) | Proxy and Group preserve `Release`, `InRelease`, signature, index, and package bytes exactly; `api/openapi/protocols/apt.yaml` fixes the public route and `make native-apt-e2e` exercises cache and member fallback behavior. |

## Low-risk Go package boundary plan

The current `internal/app` package is acceptable for rapid iteration, but it is
now carrying protocol handlers, native Hosted lifecycle logic, cache
infrastructure, management APIs, and maintenance jobs in one compile unit. That
is convenient in early development and increasingly expensive for navigation,
testing, and future protocol work.

Do not perform a one-shot package migration. Move seams only after the contract
tests above are green, and keep each migration mechanically small.

1. **Protocol handlers.** Move HTTP parsing, protocol auth/challenges, range
   handling, and response shaping into `internal/protocol/oci`,
   `internal/protocol/raw`, `internal/protocol/maven`, and
   `internal/protocol/conan`. Each package should expose a small handler
   constructor and keep route grammar tests local.
2. **Native Hosted lifecycle.** Move format-specific metadata promotion,
   object-intent validation, tag movement, Maven coordinate commit, Raw path
   publication, and OCI upload completion into `internal/hosted/oci`,
   `internal/hosted/raw`, and `internal/hosted/maven`. These modules should be
   deep: handlers call a small interface, while PostgreSQL transactions and
   object-store ordering remain inside the lifecycle implementation.
3. **Cache infrastructure.** Move reusable cache quota, distributed lock,
   circuit breaker, task queue, and object-store streaming helpers into
   `internal/cache`. Keep protocol-specific cache policy in the protocol or
   hosted packages rather than making cache a dumping ground.
4. **Management API.** Move repository/group/grant/retention/audit APIs into
   `internal/admin`, with OpenAPI contract tests continuing to exercise the
   public route surface.
5. **Persistence adapters.** Split `internal/repository/postgres.go` only after
   the higher-level seams exist. A likely end state is
   `internal/repository/postgres` for concrete adapters and
   `internal/repository` for narrow domain interfaces and in-memory fakes.
6. **Maintenance jobs.** Move collectors and repair jobs into
   `internal/maintenance` after the lifecycle modules own the predicates they
   need to verify before deleting bytes.

## Verification checklist

- `go test ./...`
- `make native-oci-e2e`
- `make native-raw-e2e`
- `make native-maven-e2e`
- `make native-npm-e2e`
- `make native-pypi-e2e`
- `make native-go-e2e`
- `make native-apt-e2e`
- `make integration-test`
- `git diff --check`
- repository provenance scan excluding `console/node_modules`

This checklist is the first-phase gate for saying the current protocol baseline
is coherent. It does not prove full Nexus parity; it proves the current
contracts are explicit, tested, and ready for incremental comparison work.
