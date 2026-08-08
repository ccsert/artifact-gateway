# Repository Console Experience Roadmap

Status: planning checklist for closing the Repository, Group, Hosted, and Proxy
Console experience gap. This document turns the Nexus gap analysis into
implementation-ready work items.

Progress update 2026-07-31:

- Phase 1 Maven Proxy browse usability is mostly implemented in the Console:
  cache entries are grouped by Maven version, checksum/signature sidecars are
  collapsed, client-side pagination is present, and Proxy Maven publish is no
  longer shown as an action.
- Phase 2 has started: Maven Proxy now has a first-class V2 cache browse
  endpoint with `groupBy=version|component|asset`, `assetFilter`, `q`,
  `pageSize`, and scoped opaque `pageToken`. The Console Maven Proxy browse view
  uses this endpoint instead of the legacy V1 operations cache list.
- Usage snippets currently reflect authenticated Maven Proxy access. Anonymous
  variants remain blocked on Phase 5 anonymous-access policy.
- V2 Proxy cache browse now supports Maven, OCI, Raw, and Conan cache indexes.
  Maven retains its component/version projections; the other formats use the
  format-neutral Asset projection. Cache operations use the formal management
  OpenAPI contract and generated Console client.
- Phase 3 has started: Proxy capacity now reports live cache bytes and cache
  object counts through the existing repository capacity endpoint, and the
  Console explains Hosted storage versus Proxy cache storage.
- Phase 3 Proxy capacity now also separates primary bytes, sidecar bytes,
  negative cache entries, expired cache entries, and reclaimable bytes for Maven
  Proxy repositories.
- Phase 4 has started: Maven Proxy browse now includes a warm-cache control that
  accepts a Maven GAV or repository path, triggers pull-through, and refreshes
  the current cache listing on success.
- Maven Proxy cache invalidation now supports exact path and prefix deletion from
  the Console. It removes cache indexes and leaves byte reclamation to the
  Orphan Collector.
- Maven Proxy now supports forced refresh from upstream, upstream health/circuit
  status, and negative-cache clearing from the Repository detail surface.
- V2 Repository and Group management now include `anonymousRead`; native OCI,
  Maven, and Raw protocol reads honor Repository policy, and V2 Groups only
  expose anonymous members whose backing Repository also allows anonymous read.
- Deletion UX now covers OCI tag unlink, Maven version tombstones, Raw path
  deletion, and selected Conan package revision tombstones.
- Group capacity reports member Repository contributions and never represents a
  Group as owning Artifact or cache bytes.
- Anonymous read decisions are recorded with the anonymous actor and bounded
  authorization source/reason values across protocol and management browse paths.

Progress update 2026-08-08:

- The audit Console now uses a server-side cursor page endpoint with signed,
  filter-scoped tokens and inclusive time-range filters. The original array
  endpoint remains available for compatibility.

## Goals

- Make Hosted Repository, Proxy Repository, and Group pages feel like one
  coherent product surface.
- Preserve the domain differences: Hosted owns visible Artifacts and Assets;
  Proxy owns cache entries for an upstream; Group is only an ordered resolution
  view.
- Give operators clear ways to browse, authorize, inspect storage, and operate
  each repository type without exposing raw object-store implementation details.
- Support anonymous read and browse use cases through explicit administrator
  policy, not ad hoc unauthenticated fallthrough.

## Shared Information Architecture

Every Repository detail page should use the same top-level shape, with tabs
enabled or adapted by repository type.

| Tab | Hosted Repository | Proxy Repository | Group |
| --- | --- | --- | --- |
| Overview | Format, state, artifact counts, storage, recent activity | Format, state, upstream endpoint, cache health, recent activity | Format, state, ordered members, effective anonymous policy |
| Browse | Visible Artifacts and Assets | Cached upstream components and Assets | Resolvable view across members, with source member shown |
| Access | Repository grants and anonymous read policy | Repository grants and anonymous read policy | Group/member anonymous read policy and inherited repository grants |
| Storage | Hosted object bytes, tombstones, retention, reclaimable bytes | Cache bytes, live/expired/negative entries, quota, reclaimable bytes | Member contribution summary; no owned bytes |
| Operations | Publish, delete/tombstone, restore, promotion, replication, retention | Warm, refresh, invalidate, upstream health, cache cleanup | Resolution diagnostics, member ordering, conflict inspection |
| Settings | Format policy, retention, capacity, publication controls | Endpoint, allowed hosts, TTLs, egress, cache policy | Member order, member policy, anonymous behavior |

## Concepts To Show In The UI

- **Repository**: durable format namespace for policy, authorization, and either
  Hosted content or one configured upstream.
- **Hosted Repository**: owns visible Artifacts and Assets introduced by
  publication or protocol upload.
- **Proxy Repository**: resolves from one allowlisted upstream and may keep a
  read-through cache. The cache is local data, but not a published Artifact.
- **Group**: ordered view over Hosted and Proxy Repositories. It does not own
  bytes.
- **Artifact**: client-visible identity, such as Maven
  `groupId:artifactId:version`.
- **Asset**: immutable byte object under an Artifact, such as a Maven JAR, POM,
  checksum, OCI blob, or Raw path object.
- **Cache Entry**: Proxy cache index plus cached byte object for an upstream
  response. It must be browsed like a component, not like raw object-store keys.
- **Publication**: Hosted-only transition that makes staged content visible.
- **Tombstone**: Hosted-only durable non-resolution record for a previously
  visible Artifact.
- **Retention Policy**: Hosted lifecycle rule. Proxy cache cleanup should be
  presented as cache policy, not retention.

## Maven Proxy Browse Experience

Current issue: Proxy cache listings expose every cached path as a top-level row,
including `.sha1`, `.sha256`, `.md5`, and `.asc` sidecars. This is hard to scan
and does not match Maven operator expectations.

Target behavior:

- Group cache entries as `groupId:artifactId -> version -> assets`.
- Default list row is one Maven version, for example
  `aopalliance:aopalliance:1.0`, not four rows for JAR, POM, and checksums.
- Default columns: Maven coordinate, primary assets, primary size, source member,
  cached-at/last-hit when available.
- Expand a version row to show:
  - tree path: `groupId -> artifactId -> version`
  - primary files first: `.jar`, `.pom`, `.module`, classifier JARs
  - checksum/signature sidecars collapsed under `Checksums / signatures`
  - copyable direct download URLs
- Search supports groupId, artifactId, version, classifier, extension, and file
  path.
- Filters support primary assets only, checksums/signatures, extension, source
  member, cached age, and upstream status.
- Pagination is by component or version, not by individual Asset file.

Example display:

```text
aopalliance:aopalliance
  1.0
    aopalliance-1.0.jar      4.4 KiB
    aopalliance-1.0.pom      363 B
    Checksums / signatures   2 files
```

## Proxy Pagination API

The current cache entries endpoint is operationally useful but too coarse for
large repositories because the Console must fetch the full list and page it in
memory.

Required backend work:

- Add a first-class V2 Proxy browse endpoint per format, not only a legacy
  operations endpoint.
- Support `pageSize`, `pageToken`, `q`, and stable sorting.
- Support Maven `groupBy=component|version|asset`.
- Return `nextPageToken` and either `totalEstimate` or `hasMore`.
- Support prefix filters that can be evaluated before reading all cache index
  records.
- Ensure page tokens are scoped to repository, format, query, grouping, and sort.
- Preserve a raw asset mode for diagnostics, but keep it out of the default UI.

## Proxy Capacity And Storage

Proxy capacity must not be reported as zero just because it has no Hosted
Artifacts. It owns cache bytes.

Proxy storage metrics should include:

- live positive cache entries
- negative cache entries
- primary Asset bytes
- sidecar/checksum bytes
- expired cache entries
- reclaimable bytes
- cache quota and usage percent
- oldest and newest cache timestamps
- upstream endpoint and source member breakdown

Hosted storage metrics should remain based on visible/tombstoned Artifacts,
Assets, retention, and orphan-collector eligibility.

Group storage metrics should not claim owned bytes. It should show member
contribution totals and resolution order.

## Proxy Operations

Proxy Repositories should not expose publication actions. Publication is a
Hosted-only concept.

Proxy-specific actions:

- **Warm cache**: input Maven GAV, Maven path, Raw path, OCI reference, or Conan
  reference and trigger a pull-through read.
- **Refresh cache**: revalidate or re-fetch selected cached entries from the
  upstream.
- **Invalidate cache**: remove a single path, a prefix, a component, a version,
  or all cache entries for a repository.
- **Inspect upstream health**: show last success, last failure, status-code
  distribution, circuit-breaker state, retry count, and latency.
- **Clear negative cache**: remove cached misses so new upstream content can be
  discovered immediately.

Hosted-specific actions:

- publish/upload
- delete/tombstone
- restore tombstone
- promotion
- replication
- retention execution

Group-specific actions:

- reorder members
- inspect effective resolution for a coordinate/path/reference
- inspect which member wins when multiple members contain the same coordinate
- diagnose anonymous-access availability across members

## Anonymous Access

Anonymous access is required for public or internal open-read repositories where
clients should pull without credentials. The product must support both anonymous
protocol reads and anonymous browse/query in the Console or API.

### Use Cases

- Anonymous Maven dependency resolution from CI/build tools.
- Anonymous Docker/OCI pull where repository policy allows it.
- Anonymous Raw downloads for public static assets or release files.
- Anonymous Conan package install where repository policy allows it.
- Anonymous browse/search/query for public repositories or Groups.
- Authenticated administration remains required for writes, settings, grants,
  cache invalidation, publication, promotion, and retention.

### Policy Model

Anonymous must be an explicit managed policy, not an implicit bypass.

Recommended model:

- A global setting: `anonymousAccess.enabled`.
- Per Repository setting: `anonymousRead.enabled`.
- Per Group setting: `anonymousRead.enabled`.
- Optional per-member anonymous inclusion for Groups.
- Optional resource prefix or content-selector support in a later phase.
- Anonymous principal should appear consistently in audit as `anonymous`.

Effective anonymous read should require:

- global anonymous enabled
- target Repository or Group anonymous read enabled
- for Groups, at least one resolved member allows anonymous read
- operation is read/browse/query only
- no write/admin/publish/cache-mutating operation is ever anonymous

### Console Management

The administrator should be able to configure anonymous access in the Console:

- Global Settings page: enable/disable anonymous access globally.
- Repository Access tab: allow anonymous read/browse for this Repository.
- Group Access tab: allow anonymous read/browse for this Group and show member
  anonymous eligibility.
- Warning copy for public exposure when enabling anonymous access.
- Audit preview: show the effective anonymous principal and allowed operations.

### API Surface

Required API additions:

- Global anonymous settings endpoint.
- Repository anonymous policy endpoint or field on Repository update.
- Group anonymous policy endpoint or field on Group update.
- Effective-access endpoint that explains why anonymous is allowed or denied.
- Audit records must include anonymous access source and decision reason.

### UI Behavior

- If anonymous browse is enabled, public browse pages can load read-only without
  a token.
- Anonymous users must not see admin-only controls.
- Login/token UI should remain available for privileged operations.
- Protocol usage snippets should omit credentials when anonymous read is enabled
  and include Basic/Bearer examples when it is not.
- Repository and Group headers should clearly show `Anonymous read: enabled` or
  `disabled`.

### Security Defaults

- Anonymous access defaults to disabled globally.
- Enabling anonymous globally does not automatically expose existing
  Repositories; each Repository or Group must opt in.
- Anonymous write/admin is unsupported.
- All anonymous reads are audited.
- Management API writes remain authenticated and authorized.

## Hosted / Proxy / Group Interaction Consistency

The UI should keep identical layout and vocabulary where it helps operators, but
not pretend that the backing model is the same.

Shared interactions:

- browse content
- copy usage snippets
- inspect assets/files
- configure read/admin access
- view storage/capacity
- inspect audit and recent activity
- configure anonymous read

Hosted-only interactions:

- publish/upload
- tombstone/delete
- restore
- retention execution
- promotion
- replication source/target where policy allows

Proxy-only interactions:

- upstream endpoint edit
- allowed hosts edit
- warm cache
- refresh cache
- invalidate cache
- inspect upstream health and circuit breaker

Group-only interactions:

- member ordering
- resolution diagnostics
- member contribution summary
- group-level anonymous read policy

## Implementation Checklist

### Phase 1: Fix Browse Usability

- [x] Maven Proxy default browse groups by component/version.
- [x] Sidecars hidden by default and visible in a collapsed detail section.
- [x] Search and filters work on Maven component semantics.
- [ ] Proxy Repository usage snippets reflect authentication/anonymous policy.
- [x] Console no longer uses raw cache paths as primary rows.

### Phase 2: Backend Browse And Pagination

- [x] Add V2 Proxy browse endpoint.
- [x] Add `pageSize`, `pageToken`, `q`, and stable sort.
- [x] Add Maven `groupBy` modes.
- [x] Add tests for page-token scoping and query consistency.
- [x] Migrate Console away from legacy V1 operations cache endpoint.
- [x] Add formal OpenAPI schema and generated client coverage for the V2 Proxy
  browse endpoint and operations.
- [x] Extend V2 Proxy browse endpoint beyond Maven.

### Phase 3: Capacity And Storage

- [x] Proxy capacity reports cache bytes and entry counts.
- [x] Sidecar bytes and primary bytes are separated.
- [x] Expired and reclaimable cache bytes are reported.
- [x] Group capacity reports member contribution, not owned bytes.
- [x] Console explains Hosted vs Proxy vs Group storage semantics.

### Phase 4: Proxy Operations

- [x] Warm cache by coordinate/path/reference.
- [x] Invalidate cache by path and prefix.
- [x] Extend invalidation to component, version, or whole repository presets.
- [x] Refresh selected cache entries from upstream.
- [x] Show upstream health and circuit-breaker state.
- [x] Clear negative cache.

### Phase 5: Anonymous Access

- [x] Add global anonymous-access setting.
- [x] Add Repository anonymous read policy.
- [x] Add Group anonymous read policy and member eligibility view.
- [x] Add Repository anonymous effective-access explanation endpoint.
- [x] Support anonymous protocol reads where policy allows.
- [x] Support Repository anonymous browse/query where policy allows.
- [x] Hide privileged Console controls for anonymous users.
- [x] Audit all anonymous reads as `anonymous`.
- [x] Add safe Conan revision delete/tombstone management actions.

### Phase 6: Documentation And Product Copy

- [x] Add UI help text for Repository, Hosted, Proxy, Group, Artifact, Asset,
  Cache Entry, Publication, Tombstone, Retention Policy, and Cache Policy.
- [x] Add operator docs for anonymous access.
- [x] Add protocol usage examples for authenticated and anonymous modes.
- [x] Add migration notes from legacy V1 Groups to V2 Proxy Repository views.

### Phase 7: Audit Operations

- [x] Add a cursor-paginated audit page endpoint without changing the legacy
  array response.
- [x] Scope and sign audit page tokens by every active filter and expiration.
- [x] Add inclusive server-side `from` and `to` time-range filters.
- [x] Add PostgreSQL and Memory Store parity tests for ordering and cursors.
- [x] Move the Console audit table from client-side slicing to server paging.

## Definition Of Done

- `maven-central-proxy` is browsable without file-level noise in the default
  view.
- Large proxy caches do not require loading all entries into the browser.
- Proxy capacity is no longer displayed as zero when cache bytes exist.
- Hosted, Proxy, and Group pages share layout but present only valid actions.
- Anonymous pull and anonymous browse can be enabled from the Console and are
  audited.
- Documentation explains why Proxy has cache operations instead of publication.
