# Protocol Compatibility Baseline

Status: current-protocol baseline for the Nexus comparison track. This document
covers only the protocols already present in Artifact Gateway: OCI, Maven, Raw,
and Conan. Its unsupported rows describe the current release, not the long-term
product boundary; see [the full repository roadmap](full-artifact-repository-roadmap.md).

## Compatibility matrix

| Protocol | Current support | Explicitly unsupported | Nexus difference | Regression gate |
| --- | --- | --- | --- | --- |
| OCI Hosted | Native Registry V2 repository rooted at `/v2/<repository>/<image>/...`; blob upload start, resumable PATCH, upload status, upload cancel, digest completion, blob mount from another Hosted OCI repository, GET/HEAD blob, byte Range, manifest PUT by tag or digest, descriptor digest/size validation, manifest GET/HEAD with media-type negotiation, manifest delete, tag movement, and `tags/list` pagination. | Registry catalog `/v2/_catalog`, referrers API, direct blob deletion, repository browsing, replication, and full Docker/ORAS black-box client matrix beyond the current fixture. | Nexus exposes broader repository administration and browsing around Docker/OCI repositories. Artifact Gateway intentionally exposes the protocol write/read surface first and keeps object bytes content-addressed behind PostgreSQL metadata. | `go test ./internal/app ./internal/repository ./contracts`, `make native-oci-e2e`, PostgreSQL/MinIO integration coverage for uploads, object intents, mount ownership, advisory locks, manifest/tag metadata, and orphan collection. |
| Raw Hosted | Native Raw repository rooted at `/raw/<repository>/<path>`; authenticated PUT, GET, HEAD, single byte Range, Digest verification, ETag/Digest response headers, DELETE, content-addressed object storage, streaming GET/Range through `Open`/`OpenRange`, paginated directory-prefix listing through a trailing-slash GET/HEAD, derived `.sha256`/`.sha512` sidecars, and resumable uploads through `POST ?resumable=1`, `PATCH ?uploadId=`, offset status, digest-verified completion, and cancellation. | Conditional write/update semantics, repository browsing beyond a prefix projection, and non-HTTP client tooling. | Nexus Raw Hosted has a broader browsing/admin experience. Artifact Gateway exposes visible path metadata from PostgreSQL, validates sidecar assertions without persisting them, keeps resumable bytes invisible until digest-verified metadata promotion, and keeps bytes content-addressed with metadata-driven delete and delayed object reclamation. | `go test ./internal/app ./internal/repository ./contracts`, `make native-raw-e2e`, integration coverage for large object streaming, Range behavior, listing, derived checksums, resumable cross-instance completion, tombstones, orphan collection, Digest/ETag, and PostgreSQL/MinIO lifecycle. |
| Maven Hosted | Standard Maven/Gradle `PUT /repository/maven/<repository>/<assetPath>` staging; server-derived coordinate and asset names; client checksum sidecars as assertions; client metadata as compatibility no-op; generated metadata/checksums from committed coordinates; explicit `POST /repository/maven/<repository>/coordinates/<coordinate>:commit`; commit idempotency is bound to a canonical asset set and one key for 24 hours; committed-coordinate browse/search with signed pagination at `/api/v2/repositories/{id}/maven/coordinates`; logical delete, management restore before delayed reclaim, and Maven/Gradle fixture coverage. | Silent auto-publication from plain Maven traffic, client-authored metadata authority, cross-coordinate transactions, mutable release overwrite, and publication without the Gateway Maven/Gradle companion commit call. | Nexus can make standard deploy traffic visible without an extra Gateway-specific commit signal. Artifact Gateway chooses explicit commit because Maven/Gradle HTTP traffic has no portable transaction-complete event across POM, artifacts, metadata, and sidecars. | `go test ./internal/app ./internal/repository ./contracts`, `make native-maven-e2e`, Maven/Gradle client fixtures for partial uploads, checksum retries, same-key commit replay, conflicting commit keys, expected asset conflicts, generated metadata, signed browse cursors, session expiry, tombstone restore, and reclaim. |
| Conan | Conan 2 Group/Proxy read-through and native Hosted resolution under `/conan/v2/<repository>/...`; Hosted publication sessions, recipe/package revision delete and restore, delayed reference-safe reclaim, and visible-reference browse/search at `/api/v2/repositories/{id}/conan/references`. | General upstream index aggregation, remote-to-remote copying, replication, and package administration beyond immutable revision lifecycle. | Nexus exposes broader Conan administration. Artifact Gateway keeps publication transactional, makes deletion reversible until reclaim, and separates authenticated management browse from the Conan download handshake. | `go test ./internal/app ./internal/repository`, Conan handler tests, PostgreSQL lifecycle/search coverage, and reclaim-worker integration evidence. |

## Contract alignment

- Raw and OCI do not use the management publish-session contract. Raw writes are
  ordinary protocol `PUT` requests. OCI writes are ordinary Registry V2 upload
  and manifest requests.
- Maven is the only current format with a management publish session, and its
  visible publication boundary is the explicit coordinate commit route.
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
| Raw | No protocol-wide Raw repository HTTP standard | `api/openapi/protocols/raw.yaml` defines the Gateway route grammar, immutable object semantics, and Range behavior; `make native-raw-e2e` is the executable overlay. |
| Maven | [Maven repository documentation](https://maven.apache.org/repositories/index.html) | `api/openapi/protocols/maven.yaml` records standard PUT staging plus the Gateway-specific coordinate commit. Maven/Gradle fixtures enforce it. |
| Conan | [Conan 2 remote documentation](https://docs.conan.io/2/reference/commands/remote.html) | `api/openapi/protocols/conan.yaml` documents Group/Proxy and native Hosted resolution; publication and management browse are versioned management operations, and protocol lifecycle remains within `/conan/v2`. |

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
- `make integration-test`
- `git diff --check`
- repository provenance scan excluding `console/node_modules`

This checklist is the first-phase gate for saying the current protocol baseline
is coherent. It does not prove full Nexus parity; it proves the current
contracts are explicit, tested, and ready for incremental comparison work.
