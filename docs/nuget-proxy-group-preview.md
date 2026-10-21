# NuGet Proxy and Group preview

[简体中文](nuget-proxy-group-preview.zh-CN.md) · [Hosted preview](nuget-hosted-preview.md) · [NuGet roadmap](nuget-roadmap.md)

NuGet Hosted, Proxy and Group share the explicitly enabled NuGet preview. Set
`GATEWAY_NUGET_PREVIEW_ENABLED=true` after applying the append-only migrations
with the standard runner. The default remains off. NuGet is absent from the
formal format profiles, repository/Group catalog and Console selector. This
stage has no `agctl` configuration commands. Governance and public admission
remain tracked by [#289](https://github.com/ccsert/artifact-gateway/issues/289).

## Configure a source and ordered Group

An administrator creates a Proxy with
`POST /api/v2/previews/nuget/repositories`, optionally using an
`Idempotency-Key`. A complete request is:

```json
{
  "name": "dotnet-private-proxy",
  "type": "proxy",
  "anonymousRead": false,
  "endpoint": "https://packages.example.test/v3/index.json",
  "allowedHosts": ["package-cdn.example.test"],
  "egressMode": "direct",
  "upstreamAuth": {"scheme": "bearer", "secret": "WRITE_ONLY_UPSTREAM_SECRET"}
}
```

The response includes `id`, `version`, `preview:true`, the native `sourceUrl`,
and credential status without the secret. The endpoint must be an explicit
HTTPS V3 service index. Resource paths come from that index, rather than a
nuget.org URL template. Additional HTTPS authorities require `allowedHosts`;
an omitted port means 443. URL user information, ambiguous paths and unknown
required resource versions fail explicitly. `egressMode` accepts `direct` or
the existing `environment` policy. Private certificate authorities must be
trusted by the Gateway runtime using its normal trust configuration.

`GET /api/v2/previews/nuget/repositories/{id}` reads the configuration.
`PUT` at the same path replaces it using `version` for compare-and-swap. Include
the unchanged name/type and the complete desired endpoint, hosts and policy.
Changing the endpoint or host binding requires a fresh upstream secret or an
explicit `{"scheme":"none"}`. A missing request credential preserves an existing
secret only within that binding. Basic authentication requires `username` and
`secret`; Bearer uses `secret`. Unknown schemes fail. Stored secrets use the
existing sealed upstream-secret purpose. Missing keys or invalid ciphertext
fail on both cold and warm cache reads.

Create an ordered Group using
`POST /api/v2/previews/nuget/groups` with
`{"name":"dotnet-team","members":["HOSTED_UUID","PROXY_UUID"]}`.
Members are active NuGet preview repositories, ordered exactly as supplied;
there is no implicit Hosted-first reorder. Groups contain 1–32 distinct members.
GET `/api/v2/previews/nuget/groups/{id}` returns its receipt; PUT replaces its
configuration using `version`, the unchanged name and the complete member list.
Proxy and Group service indexes advertise reads only. Push and publication
sessions remain Hosted operations.

## Identity, credentials and source recovery

Canonical lowercase package ID plus normalized version selects an authority.
The first successful Group resolution durably binds that tuple to a member ID
and configuration version. Reordering members preserves it. The metadata and
archive must agree on ID, version, dependency graph and discovered package URL.
Missing bytes or a changed/removed owner cannot select another member with the
same tuple. Different versions may have different owners.

`GET /api/v2/previews/nuget/groups/{id}/sources/{packageId}/{version}` verifies
the complete package and returns its member ID/version, SHA-256 and size. Save
this receipt before changing source configuration. Every configuration update
creates a new Proxy cache namespace; old metadata cannot authorize new reads.
Previously committed bytes remain referenced and count against quota.

After a deliberate configuration or credential rotation, the old Group binding
returns 409. An administrator can POST to the same source path plus
`/revalidate` with `{"groupVersion":"CURRENT_GROUP_VERSION","repositoryVersion":"PRIOR_OWNER_VERSION"}`.
This verifies the current source's complete bytes and atomically advances only
the same member with the same SHA-256 and size. It cannot switch members or
replace bytes. Retrying a lost successful response preserves that result.
Changed bytes, stale Group versions and removed members fail explicitly.

Local Gateway credentials, upstream Basic/Bearer secrets and signing keys are
separate. The native source accepts the existing local Bearer credential or
Basic password; the Basic username does not select the principal. Group reads
require the caller's read/resource grant on every ordered member. Discovery
may use a prefix grant, but each returned package is filtered against its full
ID. There is no separate synthetic Group repository grant. Anonymous access
requires the global policy, Group opt-in and every member's opt-in. An explicit
invalid, revoked or disabled local credential never falls back to anonymous.

Upstream credentials are applied only at the configured origin. An allowed
cross-origin resource does not receive them; redirects strip credentials when
crossing origins and do not reattach them on a later return. Local authorization,
cookies and proxy authorization never become upstream credentials. Every hop
uses the existing egress, DNS, TLS and redirect boundary. Reports and problem
responses exclude source secrets.

## Cache and storage boundaries

V3 JSON documents have a 30-second positive cache and 10-second negative cache.
Expired positive documents use ETag/Last-Modified conditionals. A 304 requires
an intact prior document. A transport dial/DNS/deadline outage may reuse an
intact positive document; TLS failures, denied CONNECT requests, HTTP 401/403,
malformed metadata and changed bindings do not admit that fallback. An expired
negative entry does not become a positive offline result.

Metadata is not a published artifact. A package becomes visible only after
complete nupkg inspection, SHA-256/SHA-512 and dependency validation, verified
object staging and atomic publication. Replays and concurrent fills keep one
publication in the same source namespace. Commit and quota updates share the
existing repository lock. Old and new committed namespaces both count toward
quota and backup references; staging remains invisible. Missing or corrupt
cached objects fail instead of changing the Group owner. Expired staging uses
the existing preview collector and its publication/collection fence.

Registration supports inline and external pages, at most 64 pages/4096 versions,
and requires exact page bounds, counts and unique canonical versions. Search
and autocomplete follow source pages of 100 entries and reject incomplete or
duplicate results. The aggregate limit is 4096 package versions. A JSON document
is at most 8 MiB; a repository namespace holds at most 4096 documents/64 MiB.
Exceeding a bound fails rather than truncating dependency data. Package, nuspec
and stored metadata limits remain those of the Hosted preview.

Dependency comparison supports common .NET Framework, .NET Standard,
.NET Core and modern .NET long/short framework names. Unknown platform-qualified,
portable or other framework shapes fail explicitly. Floating ranges and
unknown version ranges fail explicitly. Upstream listed/unlisted state is preserved: search and autocomplete exclude unlisted versions, while exact-version inventories, metadata and complete reads retain them. Local Hosted unlist actions remain outside this stage. NuGet V2,
Proxy/Group push, signature verification, symbols, promotion, tombstone/restore,
retention of visible caches and full search ranking parity remain outside it.
Backup metadata fingerprints include the new tables; full dedicated NuGet
disaster-recovery acceptance is not implied by reference coverage.

## Acceptance and evidence

Run `make native-nuget-e2e`. Its official .NET SDK 10.0.100 root index and platform
child are pinned by immutable digest. Docker credentials, NuGet configuration,
client homes and caches belong to the random test project. The existing Hosted
lane retains the host Gateway runtime. The private-CA Proxy/Group lane uses an
owned Linux Gateway in the pinned SDK container with its own CA file; this is
not evidence of macOS private-CA configuration.

The official client restores three-package dependency chains from one Group
source, checks a Hosted/upstream same-ID conflict and different-version owners,
and executes each result without restore. The Proxy version is explicitly unlisted upstream and must still restore by exact version. Basic and Bearer each cover cold,
warm and source-stopped cold recovery after Gateway/PostgreSQL/RustFS restart.
Complete cached archives retain size, SHA-256, original nuspec and bound source
identity. Warm reads must not fetch those archives again. Required tests cannot
fail or skip. `output/native-nuget-e2e/<project>/` records source file hashes,
each runtime binary/toolchain provenance, test JSON, client results and empty
owned-resource cleanup inventories. Bind CI evidence to its exact source tree;
local acceptance does not claim merge, public format admission or production
readiness.

Official protocol references, read on 2026-10-21:
[service index](https://learn.microsoft.com/en-us/nuget/api/service-index),
[flat container](https://learn.microsoft.com/en-us/nuget/api/package-base-address-resource),
[registration](https://learn.microsoft.com/en-us/nuget/api/registration-base-url-resource),
[search](https://learn.microsoft.com/en-us/nuget/api/search-query-service-resource),
[autocomplete](https://learn.microsoft.com/en-us/nuget/api/search-autocomplete-service-resource).
