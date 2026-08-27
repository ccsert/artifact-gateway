# Protocol Compatibility Baseline

[简体中文](protocol-compatibility.zh-CN.md)

Status: current-protocol baseline for the Nexus comparison track. This document
covers only the protocols already present in Artifact Gateway: OCI, Maven, Raw,
Conan, npm, PyPI, Go, and APT. Its unsupported rows describe the current release, not the long-term
product boundary; see [the full repository roadmap](full-artifact-repository-roadmap.md).

## How to read “compatible”

- **Standard-path compatible** means real ecosystem clients complete the listed read, install, or upload flows. It does not mean that every optional endpoint in the upstream specification is implemented.
- **Gateway extension** means ecosystem reads or basic uploads stay native, while publication, deletion, or management requires an additional Gateway API.
- **Proxy/Group compatibility** covers only the listed read paths. It does not inherit Hosted writes, catalog, referrer, or lifecycle behavior automatically.
- **A passing regression gate** proves the current fixture and client flows. It is not evidence for every client version, authentication mode, or extension combination.
- **Preview or research** is not a public support claim. APT Hosted, Cargo, and NuGet remain in this category.

## Nexus-style repository roots

Maven, npm, PyPI, Raw, and Go clients may keep the familiar
`/repository/<name>/...` repository root while moving to Artifact Gateway.
`<name>` may identify a Hosted, Proxy, or Group target. The compatibility
router only resolves the target and internally dispatches to the canonical
format handler, so it does not duplicate or bypass authentication,
authorization, auditing, protocol validation, or object lifecycle behavior.
If a Repository and Group share a name, the Repository wins, matching canonical
route precedence. The exact target name `maven` is reserved because Gateway's
older canonical Maven route already owns `/repository/maven/<repository>/...`;
rename that one Nexus target during migration rather than accepting ambiguous
cross-repository routing.

| Format | Nexus-style client root | Verified migration flow |
| --- | --- | --- |
| Maven | `/repository/<name>/` | Real Maven deploy/resolve and Gradle publish/resolve; default direct publication requires no companion commit. |
| npm | `/repository/<name>/` | Real scoped/unscoped publish, Group install, cold package-lock and online/offline `npm ci`; generated tarball URLs retain this root. |
| PyPI | `/repository/<name>/` for upload and `/repository/<name>/simple/` for reads | Real twine root upload plus Hosted, Proxy, and Group pip download, including offline Proxy cache. |
| Raw | `/repository/<name>/<path>` | PUT, GET, HEAD, Range, DELETE, directory pagination, and resumable upload; discovery URLs retain this root. |
| Go | `/repository/<name>` as `GOPROXY`; `PUT /repository/<name>/<version>.zip` for Hosted | Real `go mod download` through Hosted and online/offline Proxy paths; Nexus-style Hosted upload infers and validates the module identity from the ZIP root. |

This is a package-client migration surface, not Nexus REST API, task, user,
permission, or blob-store emulation. OCI is intentionally excluded: Docker and
ORAS require the Registry V2 root `/v2/`, and Nexus commonly selects a Docker
repository by connector port or virtual host. An OCI migration must therefore
map the registry host and Gateway repository-name prefix (or perform the
equivalent ingress rewrite); `/repository/<name>/...` cannot be a conforming
Docker Registry endpoint.

## Compatibility matrix

| Protocol | Current support | Explicitly unsupported | Nexus difference | Regression gate |
| --- | --- | --- | --- | --- |
| OCI Hosted / Proxy / Group | Hosted implements Registry V2 at `/v2/<repository>/<image>/...`: resumable blob upload, status/cancel/completion, Hosted-to-Hosted mount, blob GET/HEAD/Range, manifest PUT/GET/HEAD/DELETE, descriptor validation, media-type negotiation, tag movement/pagination, catalog, and referrers. Proxy read-through caches only manifests and blobs. Group is read-only for manifests, blobs, and tags, with Hosted members preferred. A default-disabled Hosted quarantine-read policy denies quarantined manifest closures and prevents Group fallback. | Direct blob deletion; writes through Proxy/Group; Proxy/Group catalog or referrer aggregation; and a Docker/ORAS black-box matrix beyond the current fixture. | Nexus exposes broader repository administration and browsing. Artifact Gateway implements the pull/push core first, keeps Proxy/Group read-only, and stores content-addressed bytes behind PostgreSQL metadata. | `go test ./internal/app ./internal/repository ./contracts`, `make native-oci-e2e`, and PostgreSQL/RustFS coverage for upload, Proxy cache, Group fallback, object intents, mount ownership, locks, tag metadata, and orphan collection. |
| Raw Hosted / Proxy / Group | Native Raw is rooted at `/raw/<repository>/<path>` and supports authenticated unconditional PUT replacement, GET, HEAD, one byte Range, readable `Content-Disposition`, Digest/ETag, DELETE, streaming object reads, Hosted trailing-slash prefix listing, derived `.sha256`/`.sha512` sidecars, and resumable upload with immutable chunks and one-pass completion. A path is a mutable reference; `path + digest` is the immutable snapshot identity. Proxy/Group use HTTPS-allowlisted upstreams, bounded temporary staging, renewable distributed single-flight, streaming cache publication/read, and per-Gateway staging admission. A default-disabled Hosted quarantine-read policy hides paths, sidecars, and Hosted listing entries. | Conditional writes such as `If-Match`/create-only, multipart Range, browsing beyond the Hosted prefix projection, and non-HTTP tooling. Deployments still need a temporary volume, free-space monitoring, and hard ephemeral-storage limits. | Nexus Raw Hosted has a broader browse/admin experience. Gateway preserves its ordinary same-path replacement behavior for migration, while keeping incomplete bytes invisible, validating sidecar assertions without duplicate assets, and using metadata-driven deletion plus delayed object reclamation. | `go test ./internal/app ./internal/repository ./contracts`, `make native-raw-e2e`, and integration coverage for large-object streaming, staging admission, HEAD/Range, listing, checksums, resumable completion, tombstones, orphan collection, and PostgreSQL/RustFS lifecycle. |
| Maven Hosted / Proxy / Group | Hosted accepts standard Maven/Gradle `PUT /repository/maven/<repository>/<assetPath>` and directly publishes each verified primary asset by default, requiring no companion call. A default-disabled `mavenStrictPublication` policy instead stages one coordinate until explicit `POST /repository/maven/<repository>/coordinates/<coordinate>:commit`. Client checksums and metadata are non-authoritative; Gateway generates both from verified objects. Proxy caches Maven assets; Group is read-only and Hosted-first. See [Maven Hosted publication](maven-hosted-publication.md). | Direct mode cannot guarantee atomic visibility across the independent files of one GAV if the publisher stops part-way. Strict mode requires a Gateway-specific commit. Client-authored metadata authority, cross-coordinate build transactions, mutable release overwrite, and writes through Proxy/Group are unsupported. | Default mode matches ordinary Nexus Hosted's zero-change client threshold and partial-publication trade-off. Strict mode provides stronger per-coordinate visibility than ordinary Nexus Hosted at an explicit integration cost. | `go test ./internal/app ./internal/repository ./contracts`, `make native-maven-e2e`, and fixtures for commit-free Maven/Gradle resolution, strict pre-commit 404, Proxy cache, Group fallback, checksum retries, commit replay/conflict, generated metadata, cursors, expiry, tombstone restore, and reclaim. |
| npm Hosted / Proxy / Group | Standard npm packument, exact package-version metadata, and tarball reads under `/npm/<repository>/<package>`; exact version responses preserve upstream signatures while rewriting tarball URLs, including the route used by Corepack for pinned package-manager installation. npm CLI publication of immutable SemVer versions to Hosted; scoped packages; `latest` and custom dist-tags; anonymous install/audit under repository policy; package-level browse with exact version deep links. Proxy provides verified metadata and tarball read-through caching, accepts one safe package root with harmless dot segments used by official tarballs, skips unrelated legacy versions that lack modern integrity, removes dist-tags that target skipped versions, performs conditional revalidation and negative caching, serves stale metadata on upstream failure, and applies bounded retry plus a distributed circuit breaker. Group merges Hosted and Proxy versions behind one Registry URL, keeps Hosted-first/member-order conflict semantics, rewrites tarball URLs to the Group, and filters members by grants and anonymous policy. Hosted versions support retention, tombstone/restore, delayed reclaim, promotion, and checkpointed replication. A default-disabled Hosted quarantine-read policy blocks the whole package version, filters packuments/dist-tags, and prevents Group reintroduction. | Authenticated upstream registries, dist-tag mutation after publication, unpublish/deprecate, and vulnerability-database integration. | Nexus exposes broader npm security integration. Artifact Gateway emphasizes immutable verified caching and explicit egress policy; only Hosted advertises lifecycle mutation. | `go test ./internal/protocol/npm ./internal/app ./internal/repository`, `make native-npm-e2e`, exact-version signature/HEAD/audit/quarantine tests, PostgreSQL/RustFS cross-instance cache coverage, migration/capacity/search checks, and real npm CLI Hosted publication plus Group online/offline mixed-member install coverage. |
| PyPI Hosted / Proxy / Group | Standard twine multipart uploads at `/pypi/<repository>/legacy/`; PEP 503 HTML and PEP 691 JSON Simple APIs; wheel/sdist core-metadata identity checks; immutable SHA-256 distribution files; real pip installation; anonymous reads; project browse with searchable exact-version deep links. Proxy requires SHA-256 upstream links, verifies archive metadata before caching, and serves cached files offline. Group merges Hosted and Proxy files with Hosted-first conflicts. Hosted versions support retention, tombstone/restore, delayed reclaim, promotion, and checkpointed replication. A default-disabled Hosted quarantine-read policy blocks every file in a quarantined project version, filters Simple metadata, and prevents Group reintroduction. | Authenticated upstream indexes, yanking, project metadata APIs beyond Simple, and vulnerability-database integration. | Nexus exposes additional PyPI administration. Artifact Gateway makes archive identity, upstream digests, and egress allowlists mandatory and uses version-scoped lifecycle operations. | `go test ./internal/app ./internal/repository`, `make native-pypi-e2e`, and PostgreSQL/RustFS cross-instance publication, search, tombstone, and restore coverage. |
| Go Hosted / Proxy / Group | Standard `GOPROXY` reads rooted at `/go/<repository>/<escaped-module>/...`; `@v/list`, `@latest`, `.info`, `.mod`, and `.zip`; module/version escaping; `go.mod` and module ZIP identity validation; SHA-256 content-addressed storage; ETag/HEAD; stale Proxy lists and offline assets; anonymous and resource-prefix grant filtering; Hosted-first Group aggregation; search, capacity, Console version selection, usage snippets, and version deep links. Hosted publication accepts both Gateway's explicit module/version ZIP path and Nexus 3.93+'s `PUT /repository/<repository>/<version>.zip`; the latter derives the module from the canonical ZIP root before authorization. Both paths derive `.mod` and `.info`, atomically expose all three, preserve identical retries, reject coordinate changes, and schedule publication scanning. Hosted module versions support management tombstone/restore and repository retention by canonical `module@version`; protocol reads, Group aggregation, search, and scan identities hide tombstones immediately. A durable reference-safe collector preserves all three references for a 24-hour recovery window, then fences restore and releases their Repository capacity. A byte object shared with a visible version remains physically stored until its last visible reference disappears. Immutable promotion reuses a verified three-representation snapshot in another Hosted Repository; checkpointed replication copies and verifies all three representations under target-specific object keys before one atomic publication. Both use the ZIP digest as the management distribution identity, reject Proxy targets, and enforce quarantine admission over every representation at request and final worker publication time. | Quarantine-read enforcement, checksum database mirroring, and authenticated upstream proxies. | Nexus 3.93+ provides Go Hosted and Group repositories. Gateway now accepts its version-only Hosted ZIP upload convention while retaining the explicit module/version route as a Gateway extension; neither product mirrors checksum-database content through the Go repository itself. | `go test ./internal/app ./internal/repository`, `make native-go-e2e`, and PostgreSQL/RustFS cross-instance Hosted publication, promotion, replication, retention, tombstone/restore-versus-reclaim serialization, Proxy cache, immutable identity, search, capacity, conflict, and offline-read coverage. |
| APT Proxy / Group | Verbatim reads rooted at `/apt/<repository>/...` for `dists/` metadata, signatures, indexes, by-hash objects, and `pool/` packages; SHA-256 object caching; ETag/HEAD; anonymous and resource-prefix grant filtering; ordered Group fallback; capacity, search, Console browse, and source snippets. The non-advertised Hosted H2/H3 preview supports idempotent management staging and atomic snapshot publication, deterministic signed metadata, pinned remote public-key verification, a private-CA TLS option, an operator signing-state API and Console view, bounded signing metrics, Hosted GET/HEAD/range reads, capacity/search projection, exact signed-snapshot recovery, and real Debian install and key-rotation gates. Staged bytes and incomplete snapshots remain absent from protocol reads. | Managed KMS/HSM signer-key custody and key recovery, dedicated snapshot export tooling, installed deployment alerts, automatic key distribution, delete/restore, retention, promotion, replication, and upstream authentication. | Nexus supports APT through ecosystem-specific repository formats and plugins. Artifact Gateway keeps advertised APT support Proxy/Group-only until H3 production signing passes its gates. | `go test ./internal/app ./internal/aptpublication ./internal/repository ./contracts`, `make native-apt-e2e`, `make apt-signer-rotation-e2e`, and PostgreSQL/RustFS migration, staging, cleanup, signed-snapshot, capacity, and search coverage. |
| Conan | Conan 2 Group/Proxy read-through and native Hosted resolution under `/conan/v2/<repository>/...`; Hosted publication sessions, recipe/package revision delete and restore, delayed reference-safe reclaim, visible-reference browse/search at `/api/v2/repositories/{id}/conan/references`, immutable promotion, and checkpointed replication. A default-disabled Hosted quarantine-read policy blocks the recipe-revision and package-revision closure, hides revision metadata, and prevents Group fallback. | Conan 1, general upstream index aggregation, remote-to-remote copying, and package administration beyond immutable revision lifecycle. | Nexus exposes broader Conan administration. Artifact Gateway keeps publication transactional, makes deletion reversible until reclaim, and separates authenticated management browse from the Conan download handshake. | `go test ./internal/app ./internal/repository`, Conan handler tests, PostgreSQL lifecycle/search/replication coverage, reclaim-worker integration evidence, and `make conan-e2e`. |

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
| Conan | [Conan 2 remote documentation](https://docs.conan.io/2/reference/commands/remote.html) | `api/openapi/protocols/conan.yaml` documents Group/Proxy and native Hosted resolution; publication and management browse are versioned management operations, and protocol lifecycle remains within `/conan/v2`. |
| npm | [npm registry API package metadata](https://github.com/npm/registry/blob/main/docs/REGISTRY-API.md) | Hosted accepts npm CLI publication documents and serves authoritative packuments/tarballs. Proxy rewrites and verifies upstream content before caching. Group merges member packuments and keeps every tarball URL on the Group Registry; `make native-npm-e2e` is the executable compatibility overlay. |
| PyPI | [PEP 503](https://peps.python.org/pep-0503/), [PEP 691](https://peps.python.org/pep-0691/), and the [PyPA upload API](https://warehouse.pypa.io/api-reference/legacy.html) | Hosted accepts twine multipart uploads after archive metadata verification. Proxy and Group expose digest-pinned Simple links; `make native-pypi-e2e` runs real twine and pip clients through Hosted, Group, and offline Proxy paths. |
| Go | [Go Modules Reference: GOPROXY protocol](https://go.dev/ref/mod#goproxy-protocol) and [Nexus Go CLI usage](https://help.sonatype.com/en/go-cli-usage.html) | Hosted, Proxy, and Group implement the standard read protocol and validate module assets with `golang.org/x/mod`. Hosted accepts the Nexus version-only ZIP upload and Gateway's explicit module/version route; `make native-go-e2e` runs real publication and `go mod download` against Hosted and Proxy, then repeats Proxy resolution with a fresh client cache after the upstream stops. |
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
