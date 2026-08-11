# ADR: V2 Anonymous Read, Raw, and Conan 2 Contract

Status: accepted for V2 planning. This is the source of truth for V2 format work; implementations MUST NOT weaken it without a replacement ADR.

## Terms and routing

| Term | Meaning |
| --- | --- |
| Repository | One addressable format endpoint and policy boundary, not a storage bucket. |
| Hosted | Native PostgreSQL/object-store repository. |
| Proxy | Member fetching one explicitly allowed external upstream. |
| Group | Ordered read-only virtual repository over members of one format. |

Names are lowercase DNS labels separated by `-`; names are unique within a format. Reserved routes are `/v2/<group>/...` (OCI), `/maven/<group>/...`, `/raw/<group>/<path>`, and `/conan/v2/<group>/...`. Static routes are matched before all format routes: `/api/v1`, `/metrics`, `/livez`, `/readyz`, and `/api/v1/operations` remain management or operational endpoints. Format prefixes are disjoint, so there is no cross-format Group fallback. The names `api`, `metrics`, `livez`, `readyz`, `operations`, `v2`, `maven`, `raw`, and `conan` are reserved and rejected as Group names. Within a format, a Group route wins over no other format and member resolution begins only after the complete prefix and Group name match.

A Group contains only members of its own format. All Hosted members are considered in ascending position before Proxy members; equal positions are rejected. The first successful Hosted response wins. Cache keys include format, group, canonical repository/path, member, endpoint, and requested representation. Resolution visits each Hosted member's positive or negative cache, then its upstream, before visiting any Proxy member's positive or negative cache or upstream. A negative cache entry is a member-local `not_found` result: it permits the next Hosted member and, only after every Hosted member is exhausted, Proxy resolution. A Proxy negative entry can never hide a Hosted response. Cache keys are invalidated when a member endpoint changes.

## Read access and audit

`CONTRACT: anonymous-default-deny`

Every read begins denied. The global anonymous-access policy and each addressed Repository or Group policy default to false. The effective anonymous policy is an AND: global policy, addressed target policy, and for a Group the resolved member Repository policy must all explicitly allow it. A member policy may narrow a Group but never make it more public. Policies are evaluated after route normalization. All other reads require valid bearer, Basic, or configured OIDC credentials, then the existing repository-scoped grant check. For an authenticated request, the resolver checks the grant for each candidate member before that member's cache or upstream is accessed.

`CONTRACT: anonymous-read-methods`

An anonymous request may resolve only `GET` or `HEAD`, after both policy switches are enabled. All other methods are denied before cache or source access and produce an `access_denied` audit record. This restriction also applies to format routes whose protocol otherwise supports writes or administrative operations.

| Request | Policy | Expected result | Required audit outcome |
| --- | --- | --- | --- |
| unauthenticated read | absent or false | 401 challenge; 404 only where protocol requires hiding | `access_denied` |
| unauthenticated read | `anonymous=true` | continue format resolution | resolved outcome, or terminal failure outcome |
| authenticated but ungranted | any | 403, or protocol-prescribed 404 | `access_denied` |
| administrator | any | continue resolution | resolved outcome, or terminal failure outcome |

`CONTRACT: audit-fields`

Each attempt records timestamp, request ID / trace ID, actor (`anonymous` when applicable), format, group, canonical repository or path, representation, member, member type, upstream host, operation, HTTP status, outcome, cache disposition, and byte count. Audit records never contain authorization headers, tokens, Conan credentials, or unredacted upstream query secrets. Outcomes include `access_denied`, `not_found`, `proxy_denied`, `upstream_error`, `internal_preferred`, and `resolved`.

## Raw protocol

`CONTRACT: raw-path-normalization`

Raw addresses use `/raw/<group>/<path>`. After validating the Group, the gateway returns `404` for a path ending in `/` before path-segment validation; this is the directory-listing response. Otherwise it percent-decodes each segment once and rejects empty segments, `.`, `..`, backslashes, NUL, encoded slash, and a path whose re-encoded form differs from canonical. It does not follow upstream redirects. Valid paths are joined with `/` and remain case-sensitive.

Raw supports `GET` and `HEAD` of a file. It supports one RFC 7233 byte range and returns `206` / `416`; multipart ranges are rejected with `416`. Directory listing is not supported: a path ending in `/` returns `404`. Trusted upstream `Content-Type` is preserved; otherwise use `application/octet-stream`. Return a strong ETag when known and `Digest: sha-256=<base64>`.

`CONTRACT: raw-checksum`

Checksum sidecars are ordinary read-only files named `<path>.sha256` or `<path>.sha512`. The Gateway validates that a sidecar body is lowercase hex plus an optional newline before serving or caching it. It does not generate, fetch, or use a sidecar to validate a Raw body: Raw has no authoritative manifest binding the two. A malformed sidecar returns `502`, audits `upstream_error`, and is not cached. When a body digest is known, a mismatching available sidecar is served unchanged because its immutable bytes are the requested artifact; the mismatch is recorded as `upstream_error` and the sidecar response is not cached. If both sidecars exist they are independent representations, with no preferred algorithm.

`CONTRACT: raw-proxy-allowlist`

A Raw Proxy endpoint MUST use HTTPS and its host MUST appear in that repository's explicit allowlist. DNS rebinding, redirects, link-local/private addresses, and endpoint credentials are rejected. A denied Proxy is never contacted and is audited as `proxy_denied`.

`CONTRACT: raw-cache`

Raw successful file bodies are read-through cached for 15 minutes by default, subject to Group quota. `404` and `410` are negatively cached for one minute; authorization failures, malformed paths, and upstream `5xx` are not cached. Entries retain content type, ETag, digest, size, member, and endpoint. Invalidation is explicit by canonical path and cannot remove a Hosted entry because a Proxy refreshes.

## Conan 2 protocol

`CONTRACT: conan2-only`

V2 supports Conan 2 resolution for a Conan 2.x client using v2 REST, including
native Hosted metadata and files. Conan 1, remote-to-remote copying, upstream
index aggregation, and server-side recipe generation are out of scope. Hosted
publication uses the versioned publish-session management API; immutable
revision deletion and restore remain in the existing `/conan/v2` family.

Conan routes use `/conan/v2/<group>/conans/...`; the remote URL MUST include the group. The sole protocol exception is the Conan 2 Basic-login handshake `GET /conan/<group>/v2/users/authenticate`; it follows the same resource policy and is not a general user API. Conan 1 authentication and non-GET authentication methods return `404`. The client gets the normal protocol challenge when anonymous access is disabled; credentials are never forwarded to a Proxy.

`CONTRACT: conan-coordinate`

A recipe coordinate is `name/version/user/channel`, with immutable revision
`rrev`. A package coordinate adds `package_id`, and a package revision adds
`prev`: `name/version/user/channel#rrev/package_id#prev`. The gateway stores
each segment separately and performs only strict URL segment decoding. A
revision is immutable once observed. Omitted `rrev` or `prev` caches selected
upstream metadata only for its metadata TTL; it never creates a permanent
alias.

`CONTRACT: conan2-read-endpoints`

The Gateway supports only these Conan 2 read endpoints, all `GET` or `HEAD` under `/conan/v2/<group>/conans`:

| Endpoint suffix | Response contract |
| --- | --- |
| `/{name}/{version}/{user}/{channel}/revisions` | JSON object with `revisions`, an array of objects containing string `revision` and numeric or RFC3339-string `time`. |
| `/{name}/{version}/{user}/{channel}/revisions/{rrev}/search` | JSON object with a `packages` object; required only for the Conan 2 download handshake. |
| `/{name}/{version}/{user}/{channel}/revisions/{rrev}/files` | JSON object with `files`, a map from filename to an object containing lowercase-hex `sha256` and non-negative numeric `size`. |
| `/{name}/{version}/{user}/{channel}/revisions/{rrev}/files/{filename}` | Raw recipe file; verify against the corresponding files metadata entry before caching. |
| `/{name}/{version}/{user}/{channel}/revisions/{rrev}/packages/{package_id}/revisions` | JSON object with `revisions`, using the same revision shape. |
| `/{name}/{version}/{user}/{channel}/revisions/{rrev}/packages/{package_id}/latest` | JSON object with selected `revision` and RFC3339 `time`; required only to resolve an omitted `prev`, and cached as metadata. |
| `/{name}/{version}/{user}/{channel}/revisions/{rrev}/packages/{package_id}/revisions/{prev}/files` | JSON object with `files`, using the same file metadata shape. |
| `/{name}/{version}/{user}/{channel}/revisions/{rrev}/packages/{package_id}/revisions/{prev}/files/{filename}` | Raw package file; verify against the corresponding files metadata entry before caching. |

Every `{name}`, `{version}`, `{user}`, `{channel}`, `{rrev}`, `{package_id}`, `{prev}`, and `{filename}` occupies exactly one path segment. Decode each segment once; reject empty values, `.`, `..`, `/`, `\\`, NUL, `%2f` in any case, and raw or percent-encoded `#`. Coordinates are never reconstructed into `#`-delimited paths. File-list `filename` keys use the same rules and no slash, so a metadata response cannot name a file outside its requested coordinate. Unsupported methods or endpoint shapes return the Conan protocol's `404` response and audit `not_found`; malformed segment or metadata shape returns `400` and audit `upstream_error`. The revision-scoped `search` endpoint does not broaden general search or index support.

Recipe metadata, package metadata, and download URLs resolve Hosted-first. Recipe manifests, package manifests, and artifacts are verified against Conan metadata checksums before caching. A mismatch returns `502`, records `upstream_error`, and is never served or cached.

`CONTRACT: conan-cache`

Conan artifact bodies have a default 15 minute TTL; recipe/package/revision metadata has one minute; terminal `404` is negatively cached for one minute. Upstream `401`, `403`, `429`, `5xx`, malformed metadata, and checksum mismatch are not negative-cached. Keys include every coordinate field, representation, member, and endpoint. Quota is charged to the Group; eviction and invalidation retain the audit trail.

`CONTRACT: conan-proxy-allowlist`

Conan Proxy endpoints use the same HTTPS, host allowlist, redirect, and address restrictions as Raw Proxy endpoints. The allowlist is separate from OCI and Maven and is per repository.

## Configuration, migration, and operations

The V2 schema extends the existing format-specific Group tables with additive `anonymous` columns on Groups and members; Proxy members additionally persist their own `allowed_hosts`. Group owns `cache.quota_bytes`, while each Proxy member owns its host allowlist. No OCI or Maven row is rewritten in place. Migrations are additive, transactional, and forward-only. The existing OCI/Maven tables themselves are the compatibility projection, so no duplicate view can drift from their canonical rows. Rollback is an application rollback followed, when needed, by a forward compensating migration, never a destructive down migration.

The compatibility query surface remains exact: legacy OCI code can continue to select `oci_groups(name, enabled, created_at)` and `oci_group_members(group_name, name, member_type, endpoint, position)`; Maven uses the corresponding `maven_*` columns. The new policy columns default to false, so populated legacy rows preserve default-deny behavior. The current `resolver_audit_log(group_name, repository, member_name, outcome, actor, occurred_at)` remains readable through its existing columns; V2 audit fields are additive nullable columns. Existing OCI and Maven reads continue to use these same tables during and after the policy migration.

| Setting | Required semantics |
| --- | --- |
| `read_policy.anonymous` | Boolean, defaults false per Group/Repository. |
| `proxy.allowed_hosts` | Non-empty exact DNS host allowlist persisted on each Proxy member. |
| `cache.ttl` | Defaults: artifact 15m, metadata 1m, negative 1m; positive duration only. |
| `cache.quota_bytes` | Positive byte limit per Group; never delete another Group's cache. |
| `cache.max_object_bytes` | Positive per-response limit; exceedance is not cached. |

Metrics use bounded labels only. Protocol metrics use `format`, `operation`, `outcome`, `cache_disposition`, and member type; HTTP metrics use the fixed request `class` and status-code family; database metrics use the fixed pool names `primary`, `artifact-locks`, `coordinator`, and `notifications`, fixed connection states, and fixed closure reasons. They MUST NOT label with path, coordinate, actor, upstream URL, checksum, or repository name. Counters cover requests, authorization denials, cache hit/miss/negative hit, proxy denials, checksum mismatches, upstream failures, bytes served, quota rejections, runtime health, and PostgreSQL pool pressure.

## Adapter boundary and compatibility matrix

The format adapter owns route parsing, response shapes, conditional/range semantics, coordinate validation, and cache keys. The resolver owns Group ordering, policy invocation, audit recording, bounded metrics, and Proxy ordering. Native Hosted reads use PostgreSQL metadata and verified object-store bytes directly. Format adapters MUST not route native requests through an external upstream client.

| Capability | OCI | Maven | Raw V2 | Conan 2 V2 |
| --- | --- | --- | --- | --- |
| Native Hosted | supported | supported | supported | supported |
| Proxy and read-through cache | supported | supported | specified | specified |
| Anonymous reads | V2 policy migration | V2 policy migration | specified | specified |
| Standard client fixture | ORAS/Docker | Maven/Gradle | curl + HTTP range fixture | Conan 2.x fixture |

`CONTRACT: fixtures-and-upgrade`

The Raw fixture MUST cover canonical and rejected paths, `GET`, `HEAD`, range, checksum, content type, cache hit, negative cache, allowlist denial, anonymous denial/allow, and audit fields. The Conan fixture MUST use a Conan 2.x client to resolve a revisioned recipe and package, verify checksum failure, cache hit, negative cache, allowlist denial, anonymous denial/allow, and audit fields. Upgrade tests MUST apply the V2 additive migration to an OCI/Maven populated database, verify existing endpoints and audit records remain readable, and verify a rollback application binary does not require V2 rows.
