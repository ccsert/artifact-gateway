# NuGet Hosted preview

[简体中文](nuget-hosted-preview.zh-CN.md) · [NuGet roadmap](nuget-roadmap.md) · [Documentation index](README.md)

NuGet Hosted is an explicitly enabled preview. It has its own repository and
publication DTOs under `/api/v2/previews/nuget`, and a native `/nuget/{name}`
protocol endpoint. It remains absent from the admitted format profiles, formal
repository creation/catalog, and Console format selector. The separately enabled
[Proxy/Group preview](nuget-proxy-group-preview.md) extends native reads.
Symbols publication, signature verification, promotion, tombstone/restore,
retention and full governance admission remain tracked by #289.
There is no `agctl` command for the preview API.

## Enable and provision

Apply the append-only migrations with the normal migration runner and configure
the existing PostgreSQL and verified S3/RustFS ports. Set
`GATEWAY_NUGET_PREVIEW_ENABLED=true` on the API process. The default is false;
invalid boolean values fail startup. An API-only node does not start cleanup
workers. Enable the same preview on a worker/standalone node with retention
work enabled to collect expired uploads. A restrictive worker-format filter
that excludes NuGet also excludes this preview collector.
Runtime diagnostics and node listings report this collector under
`previewWorkerFormats:["nuget"]`; `workerFormats` keeps the admitted format enum.

An administrator creates a repository through
`POST /api/v2/previews/nuget/repositories` with a JSON body such as
`{"name":"dotnet-internal","anonymousRead":false}`. The receipt contains the
repository ID, `preview:true` and its absolute `sourceUrl`. Replaying the same
`Idempotency-Key` as the same actor returns that repository; changing its body
returns 409. Keep the receipt: preview repositories do not appear in the formal
repository list. `GET /api/v2/previews/nuget/repositories/{id}` reads it back.

Roles, account state and grants are evaluated by the same Gateway authorizer.
Existing grant/role APIs may bind the preview repository ID. Resource prefixes
match canonical lowercase package IDs, for example `acme.`. The service index
and search discovery accept an applicable prefix grant; every package action
checks the full ID and search results are individually filtered.

For native push, `X-NuGet-ApiKey` is an existing Gateway credential. Read requests
accept Bearer credentials or HTTP Basic with the credential as its password;
the Basic username never selects or impersonates the actor. Configure a source
using its returned service-index URL and an owned `NuGet.Config`. Push with
`dotnet nuget push PACKAGE.nupkg --source SOURCE --api-key CREDENTIAL`.
Explicit invalid/revoked credentials are rejected even when anonymous reads are
enabled. Anonymous access requires both global policy and repository opt-in.
These credentials are downstream Gateway credentials, not upstream secrets.

## Publication and recovery

The canonical identity is lowercase package ID plus normalized version. The
module uses the existing nupkg/nuspec validator, preserves the original nuspec,
and records the complete archive's SHA-256, SHA-512, dependency groups and
version ranges. The maximum archive size is 256 MiB, nuspec size 1 MiB, and
stored metadata size 256 KiB. Floating dependency ranges and ambiguous or
malformed metadata fail explicitly.

Native PackagePublish performs reserve, verified staging and commit as one
client operation. Advanced callers can use the independently authorized
publication-session API: reserve with an `Idempotency-Key`, PUT the original
nupkg to the session's `/package`, then POST `/commit`. Sessions are bound to
the initiating actor and repository. Commit reopens and hashes the complete
object, revalidates identity and dependency metadata, then atomically stores
the visible publication and audit record. Reserved and staged uploads are not
listed by any read endpoint. Identical native pushes replay the first visible
publication; different bytes at the same canonical identity return 409.

After a dropped staging response, retry the same session and exact bytes.
After a dropped commit response or process restart, read the session and replay
commit. A committed receipt keeps its original publication time. Replaying,
staging or committing an expired session returns 410; its GET receipt remains
readable. Start a new session with a new idempotency key. Reservation
expiry is one hour. Replacements use a distinct immutable object key, and
expired-upload cleanup shares the publication lock, so an older cleanup attempt
cannot delete a new visible publication. Collection markers are durable and a
failed delete/marker is retried. Visible package governance is outside this
preview.

Use GET/PUT `/api/v2/previews/nuget/repositories/{id}/capacity` for preview usage
and quota. PUT requires repository-wide administration. `usedBytes` and
`objectCount` include committed publications; quota admission also includes
active reservations. PostgreSQL commits and quota updates share repository
locks. Backup reference verification includes committed NuGet objects and
excludes staging-only objects; backup metadata fingerprints include both tables.
This reference coverage does not itself claim a separate full NuGet disaster
recovery acceptance run.

## Native reads and validation

The service index advertises PackagePublish/2.0.0,
PackageBaseAddress/3.0.0, gzip RegistrationsBaseUrl/3.6.0, minimal search and
autocomplete. Flat-container package reads support GET/HEAD, byte ranges,
ETag/conditional reads, original nuspec and SHA-512 sidecars. Search implements
`q`, `skip`, `take`, `prerelease`; autocomplete also accepts `id`. Metadata
enumeration is bounded to 4096 versions and 8 MiB; exceeding the preview bound
returns `nuget_preview_metadata_limit` instead of truncating dependency data.
Repository URLs in responses use the request's external host and HTTPS scheme
(TLS or the deployment's forwarded HTTPS indicator).

Run `make native-nuget-e2e` for the owned acceptance gate. It pins official
.NET SDK 10.0.100 by root-index digest and platform child digest, uses an empty
Docker credential configuration and isolated client homes/configuration/caches,
and starts only its own PostgreSQL/RustFS resources on random ports. It checks
atomic visibility, quota races, upload/commit response loss, full readback,
cleanup serialization, and backup references. Official-client acceptance
publishes a three-package dependency chain, rejects a missing transitive
dependency, restores from the target alone with a cold cache, restarts Gateway,
PostgreSQL and RustFS, restores cold again, then executes offline with the
target stopped. Required tests may neither fail nor skip. The evidence directory
`output/native-nuget-e2e/<project>/` retains source hashes, client/tool provenance,
JSON test results and zero-resource cleanup records. Local acceptance, merged
CI and production readiness are separate facts.

Protocol references, read on 2026-10-20: [PackagePublish](https://learn.microsoft.com/en-us/nuget/api/package-publish-resource),
[flat container](https://learn.microsoft.com/en-us/nuget/api/package-base-address-resource),
[registration](https://learn.microsoft.com/en-us/nuget/api/registration-base-url-resource),
[search](https://learn.microsoft.com/en-us/nuget/api/search-query-service-resource),
[dotnet restore](https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-restore).
