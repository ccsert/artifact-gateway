# NuGet Hosted preview

[简体中文](nuget-hosted-preview.zh-CN.md) · [NuGet roadmap](nuget-roadmap.md) · [Documentation index](README.md)

NuGet Hosted is an explicitly enabled preview. It has its own repository and
publication DTOs under `/api/v2/previews/nuget`, and a native `/nuget/{name}`
protocol endpoint. It remains absent from the admitted format profiles, formal
repository creation/catalog, and Console format selector. The separately enabled
[Proxy/Group preview](nuget-proxy-group-preview.md) extends native reads.
The [#449](https://github.com/ccsert/artifact-gateway/issues/449) governance slice
adds unlist/relist, retained tombstones and quarantine read enforcement. Symbols
publication, signature verification, scanning, promotion/replication, retention,
reclamation, Console and full governance admission remain tracked by #289.
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
failed delete/marker is retried. Replaying a committed push preserves all
existing listing, tombstone and quarantine decisions.

Use GET/PUT `/api/v2/previews/nuget/repositories/{id}/capacity` for preview usage
and quota. PUT requires repository-wide administration. `usedBytes` and
`objectCount` include committed publications; quota admission also includes
active reservations. PostgreSQL commits and quota updates share repository
locks. Backup reference verification includes committed NuGet objects and
excludes staging-only objects; backup metadata fingerprints include both tables.
This reference coverage does not itself claim a separate full NuGet disaster
recovery acceptance run.

## Listing and retained governance

Hosted `PackagePublish/2.0.0` supports standard
`DELETE /nuget/{name}/api/v2/package/{id}/{version}` to unlist and `POST` at the
same path to relist. Both require package-level write permission, return 204
for the desired state and reject a tombstoned version with 404. The official
`dotnet nuget delete ID VERSION --source SOURCE --api-key CREDENTIAL` command
uses this unlist behavior. Unlisted packages disappear from search and
autocomplete. Registration retains the version with `listed:false`, and the
flat-container directory retains it; exact GET/HEAD, nuspec, SHA-512, ranges and
conditional downloads continue to work. Replaying a push never relists it.

Repository administrators read retained identities with
`GET /api/v2/previews/nuget/repositories/{id}/packages/{packageId}/{version}/governance`.
POST at the same path takes the complete immutable `expectedDigest`, the
independent `expectedGovernanceVersion`, one action (`unlist`, `relist`, `delete`,
`restore`, `quarantine`, `release`), and a non-empty `reason` up to 2048 characters.
The receipt contains `listed`, `tombstoned`, `quarantined`, the new
`governanceVersion` and actor/time/reason evidence. A changed identity or stale
version returns 409; re-read before making a new decision. State and audit commit
atomically. Exact no-op decisions keep the version and do not duplicate audit.

`delete` installs a retained tombstone; `restore` removes only that tombstone.
`quarantine` and `release` independently change the governance restriction.
Neither release nor native relist restores a tombstoned version, and restore
never releases quarantine or relists a package. Governance keeps immutable bytes,
publication sessions, backup references and used capacity. It does not free quota,
permit replacement bytes at the same coordinate, or run delayed reclamation.
Restore, quarantine release and relisting re-read and verify retained package
bytes before removing a restriction; corruption preserves the prior decision.

The administrator-only GET/PUT endpoint
`/api/v2/previews/nuget/repositories/{id}/quarantine-read-policy` returns
`{"version":"1","enabled":false}` by default. PUT requires `expectedVersion`
and `enabled`; policy state and audit commit together. Enabling it hides
quarantined identities from registration, search, autocomplete and flat-container
lists, and rejects every exact representation's GET/HEAD before range or
conditional handling. A tombstone always has the same read barrier. Policy
lookup failure fails closed. With the policy disabled, quarantine preserves
native reads; release removes only its own restriction.

Proxy governance applies only to already verified local cached identities;
uncached remote metadata is not a governance identity. Local unlist/relist is a
Hosted operation; Proxy requests return 405. Other actions protect the cached
identity and its remote metadata projection. Group governance is evaluated on
the pinned canonical member before visibility: a hidden owner cannot fall back
to another member's package. Neither governance nor policy enables the preview
or admits NuGet to formal format catalogs. Migration `000182` is append-only;
older binaries do not understand its read barriers and must not serve the
upgraded NuGet preview.

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
target stopped. It also performs official-client unlisting and exact cold
restore, rejects cold restore while quarantine enforcement or a tombstone is
active, preserves enforcement through all three restarts, and restores the same
bytes after an explicit governance recovery. Required tests may neither fail nor skip. The evidence directory
`output/native-nuget-e2e/<project>/` retains source hashes, client/tool provenance,
JSON test results and zero-resource cleanup records. Local acceptance, merged
CI and production readiness are separate facts.

Protocol references, read on 2026-10-20: [PackagePublish](https://learn.microsoft.com/en-us/nuget/api/package-publish-resource),
[flat container](https://learn.microsoft.com/en-us/nuget/api/package-base-address-resource),
[registration](https://learn.microsoft.com/en-us/nuget/api/registration-base-url-resource),
[search](https://learn.microsoft.com/en-us/nuget/api/search-query-service-resource),
[dotnet restore](https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-restore).
