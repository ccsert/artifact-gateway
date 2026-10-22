# Helm HTTP preview

[简体中文](helm-hosted-preview.zh-CN.md) · [Documentation index](README.md)

Traditional HTTP Helm Hosted/Proxy/Group is a default-off preview under #290/#291. It provides
`index.yaml`, original chart/provenance downloads, an explicit multipart upload
API, bounded browsing, repository grants and capacity reservations. It remains
absent from the formal Format enum, format profiles, repository lists, active
capabilities and Console selectors. Existing OCI chart storage is a separate
protocol. Lifecycle and formal admission remain #292.

## Enable and provision

Apply the append-only migrations using the normal runner and configure the
existing PostgreSQL and verified S3/RustFS ports. Explicitly set
`GATEWAY_HELM_PREVIEW_ENABLED=true`; invalid booleans fail startup. Enable the
same preview on a worker/standalone node with `retention` work enabled to collect
expired upload objects. A restrictive `GATEWAY_WORKER_FORMATS` filter must include
`helm`. API-only nodes do not collect uploads. Diagnostics place the collector in
`previewWorkerFormats:["helm"]`, outside the admitted `workerFormats` enum.

The supplied Compose stack forwards `GATEWAY_HELM_PREVIEW_ENABLED` from `.env`,
defaulting to `false`. Set it to `true` before recreating the API and worker
services that should serve or maintain this preview.

An administrator creates a private Hosted preview with
`POST /api/v2/previews/helm/repositories` and `{"name":"charts-internal"}`. Keep
the returned ID and Gateway-relative `sourceUrl`; the formal repository catalog
does not list previews. An optional actor-scoped `Idempotency-Key` replays the
same create request, while a changed body returns 409. Read the receipt through
`GET /api/v2/previews/helm/repositories/{id}`. Proxy and Group provisioning is described below.

Existing repository grant APIs can bind this ID. Resource prefixes match the
case-sensitive chart name; every version of a chart uses that name as its
resource. Replace, single-row upsert and template application accept an empty
prefix for the whole repository or up to 63 lowercase ASCII letters, digits and
hyphens beginning with a letter or digit. Versions and URL paths are rejected.
This permission does not expose Helm through formal artifact search.
Index and preview browse requests accept an applicable prefix grant,
then filter each chart. Downloads and uploads check the full chart name.
Read credentials support Bearer or HTTP Basic with the Gateway credential as
password. The Basic username cannot select an actor. Management uploads use
Bearer authentication. Explicit invalid/revoked credentials always fail.
Creation is private by default; anonymous reads require both an explicit `anonymousRead:true` and the enabled global policy. Group anonymous reads additionally require every member to allow anonymous reads.

## Publish a complete closure

There is no universal traditional `helm push` protocol. Use the declared
Gateway-specific operation:

```text
PUT /api/v2/previews/helm/repositories/{id}/charts/{chartName}/{version}
Content-Type: multipart/form-data
Authorization: Bearer <Gateway credential>
Idempotency-Key: <optional actor-scoped replay key>
```

The `chart` file part is required and must be named `{name}-{version}.tgz`.
An optional `provenance` file part must be named `{name}-{version}.tgz.prov`.
Both must be sent in the same request. When using the generated Console client,
pass named `File` values with these exact filenames so multipart preserves them.
`Chart.yaml` determines identity and must
match the path exactly: names are lowercase ASCII letters, digits and hyphens
(maximum 63 characters), and versions are strict SemVer, including optional
prerelease/build metadata. Gateway does not coerce `1.2` or `v1.2.3`.

The entire closure is immutable. Exact chart and provenance replay returns 201;
changing either byte stream, removing provenance or attaching it later returns
409. Publish a new version to add or replace a signature. Malformed multipart,
identity mismatches, unsafe/truncated archives and invalid provenance return 400;
compressed upload bounds return 413; quota conflicts return 409; unavailable or
corrupt published objects return 503 without serving those bytes.

The compressed chart bound is 64 MiB, provenance and original `Chart.yaml` are
1 MiB each, and stored metadata is 256 KiB. Validation caps all expanded tar data
across packaged dependencies at 256 MiB, total entries at 10,000 and packaged
dependency nesting at eight levels. Links, devices, duplicate/traversing paths,
multiple gzip members, bad gzip checksums, missing tar end markers, YAML aliases,
duplicate mapping keys and extra YAML documents are rejected. No archive entry
is extracted to the filesystem. Dependency URLs are never fetched by publishing.

Provenance must be an original OpenPGP clearsigned Helm document containing the
same chart metadata and complete chart SHA-256 under its exact filename. Gateway
checks that binding and signature packet structure, preserves original bytes and
does not assert publisher trust. Consumers verify signatures with an explicitly
trusted public keyring. Gateway neither receives nor copies signing private keys.

Before object writes, a persistent one-hour session reserves the chart plus
provenance size. Commit reopens both complete objects, checks size/SHA-256,
revalidates chart/provenance binding, then atomically commits the publication and
audit record. Pending uploads are absent from every index and browse result.
Retry the exact request after a dropped response or process restart. An expired
reservation returns 410 for its replay key; use a new key to start again. Expired
upload collection shares the publication lock and uses session-specific object
keys, so it cannot delete a replacement publication. Collection is idempotent.

## Consume and inspect

Use the returned source location as `https://gateway.example/helm/charts-internal`
in an owned Helm repository configuration. The index is at `index.yaml`. Entries
contain original chart metadata, full archive SHA-256 and relative URLs such as
`charts/owned/1.2.3/owned-1.2.3.tgz`. Appending `.prov` selects provenance from the
same committed closure. No upstream or alternate-source URL appears in an index.
The index is a deterministic, authorized snapshot; unchanged publications produce
identical YAML and ETags. GET/HEAD downloads support conditional and range reads
and verify complete persisted bytes before serving a requested range.

`GET /api/v2/previews/helm/repositories/{id}/charts` returns at most 4096 committed
charts in one consistent snapshot; a larger inventory fails with 422 rather than
silently truncating. `GET .../charts/{name}/{version}` returns identity, size,
digests and download locations. `GET .../{id}/capacity` reads committed chart plus
provenance usage; repository admins set `{"quotaBytes":N}` with PUT on that path.
Zero means unlimited. Concurrent pending reservations count toward the same quota.

## Proxy and Group source snapshots

An administrator creates a Proxy through the same preview repository POST with
`type:"proxy"`, an explicit HTTPS `endpoint` for its index, optional
`allowedHosts` (at most 32 additional HTTPS authorities), `egressMode` (`direct`
or `environment`) and optional `upstreamAuth:{scheme,username,secret}`. Supported
schemes are `none`, `basic`, and `bearer`. The write-only source secret is sealed
with `GATEWAY_SETTINGS_ENCRYPTION_KEY`; local Gateway credentials and signing
private keys are independent. Responses expose only the scheme, optional username
and `credentialsConfigured`. A repository PUT replaces configuration with a
required current `version`. Changing endpoint/allowlist while retaining a source
credential requires a fresh secret or explicit `scheme:"none"`.

Only HTTPS URLs without userinfo, query, fragment, escaped/traversing paths or
trailing-dot hosts are accepted. Every chart URL must belong to the index origin
or an explicitly allowed authority. Every transport redirect independently checks
DNS/egress and strips source credentials after crossing origins, including a
redirect back to the initial origin. Client Authorization, cookies and proxy
credentials never become source credentials. This preview accepts exactly one
URL per index entry and deliberately rejects mirror/plugin/signature-query URLs.

The complete index is at most 1 MiB/4096 identities. YAML aliases/anchors,
duplicate/non-string keys, more than 64 levels, more than 100,000 nodes, extra
documents, ambiguous duplicate chart versions and non-SHA-256 digests fail closed.
A positive document is fresh for 30 seconds and revalidated using ETag and
Last-Modified. Only unavailable dial/DNS/deadline failures permit stale positive
metadata; HTTP denials, TLS/egress failures, malformed content and changed digests
do not. A stale document remains readable while the same source configuration is
active. A source config change creates a new cache namespace.

Index entries point only to
`/helm/{view}/snapshots/{snapshotId}/charts/{name}/{version}/{name}-{version}.tgz`;
appending `.prov` uses the same frozen entry. Each persisted snapshot pins source
repository/version, original metadata, full chart digest and one asset URL. A
chart GET first fetches the full bounded archive and its optional `.prov`, checks
identity, metadata, size/SHA-256 and provenance binding, then uses the existing
quota reservation/immutable publication transaction. Missing charts, bad digests,
authorization failures and malformed provenance never fall through to another
source. Only a source provenance 404 fixes an absent signature; attaching it later
requires a new chart version. Interrupted writes stay invisible and resume after
restart. Verified cached closures support offline GET/HEAD/range reads and count
toward the repository quota. Pending closures use the existing one-hour expiry
and collector. Source metadata is not an admitted artifact identity.

Create a Group with `POST /api/v2/previews/helm/groups` and
`{name,members:[repositoryId,...],anonymousRead?}`. The 1–32 direct Hosted/Proxy
members are ordered; nested Groups, duplicates and cycles are rejected. PUT on
`/groups/{id}` replaces the order with a required current `version`; GET reads it.
The same caller must pass every current member's read boundary before discovery.
Chart prefix grants filter the intersection of readable charts. The first
indexed name/version binds a durable source authority. Reordering cannot switch
it, and any upstream/member error blocks the whole index. If that owner is
removed, changes configuration/version, disappears from its current index, or
advertises different bytes, reads fail with 409 instead of selecting a fallback.
Previously frozen cached bytes survive an upstream index change, provided the
owner remains an authorized active member with the same configuration version.
An administrator can create a new Group to explicitly establish a new authority;
this preview does not automatically rebind existing authorities after rotation.

`GET /api/v2/previews/helm/groups/{id}/sources/{chartName}/{version}` reads an
already established authority and reports source repository/version, digest,
optional upstream URL and whether a complete closure is cached. It checks every
member's chart permission and performs no implicit source fetch/download. An
unselected coordinate returns 404. Snapshots are create-only and bounded to 128
per view (each at most 4096 entries/8 MiB serialized metadata); new snapshots beyond
that limit return 422. There is no snapshot pruning API in this preview. All
Proxy/Group write operations return 405. Cached immutable conflicts/source version
changes return 409, upstream missing/denied/corrupt inputs return 502, cache bounds
return 422, and transient storage/transport failures return 503. Formal lifecycle,
scan/quarantine/distribution and visible-cache eviction stay unavailable under
#292; there is no implicit unsupported governance operation.

## Reproducible acceptance and remaining scope

Run `make native-helm-e2e` with Docker, the repository Go toolchain, curl and
Node.js 24 available. The gate also verifies a named-file upload through the
generated request type and multipart serializer over real HTTP. It downloads the
official Helm 3.19.0
archive for a supported platform and verifies a checked-in fixed SHA-256. It uses
only owned default-bridge PostgreSQL/RustFS containers, empty Docker credentials,
empty target-only Helm configuration/cache, a synthetic signed chart and a
consumer-owned public keyring. It exercises the full Gateway routes, signed
`helm package`, declared upload/replay, database/handler restart, `repo add/update`,
cold `pull --verify`, independent `verify`, private upstream source caching, mixed
Hosted/Proxy Group source conflicts/reordering, upstream-offline reads after restart,
permission failures and real credential
rotation/revocation. A chart above 1 MiB compressed also exercises the upload body
boundary.

Helm 3.19.0's `install --dry-run=client` still checks Kubernetes reachability before
rendering. This gate supplies an owned GET-only discovery fixture, rejects every
write request and records the exact binary Git commit and corresponding upstream
source branches. That case proves cold chart consumption and dry-run rendering;
it does not prove installation into a real Kubernetes cluster.

Evidence under `output/native-helm-e2e/<run>/` includes source/head/tree hashes,
fixed-client provenance, raw JSON test events, an explicit mandatory test list,
required/summary results, raw CLI command outputs, original upstream source proof
and exact owned-resource cleanup. Missing or skipped mandatory cases fail.

This preview does not expose Proxy/Group publication, Console creation/
upload/browse, agctl commands, tombstones/restore, retention of visible charts,
quarantine/scanning, promotion/replication, full backup/upgrade acceptance or Nexus
Helm migration. These remain #292 or their migration dependencies. There is
no production routing or source migration/cutover in this delivery.

Protocol references: [Helm chart repository guide](https://helm.sh/docs/topics/chart_repository/),
[Helm provenance](https://helm.sh/docs/topics/provenance/),
[fixed Helm 3.19.0 release](https://github.com/helm/helm/releases/tag/v3.19.0).
