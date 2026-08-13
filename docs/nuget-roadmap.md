# NuGet Repository Roadmap

## Status and priority

NuGet is a deferred ecosystem candidate and is no longer a first-priority
roadmap item. Cargo is evaluated ahead of it after APT H3. NuGet remains
valuable because it is the Microsoft-supported package
mechanism for .NET, Visual Studio, and `dotnet` clients, and because its V3
service index gives a stable discovery boundary for private repositories.

The N1 byte-contract foundation is implemented: Artifact Gateway can validate
a complete `.nupkg` ZIP, read one bounded root `.nuspec`, reject ambiguous XML
or archive paths, normalize NuGet versions, and derive a case-insensitive
canonical identity. NuGet is intentionally absent from the format catalog,
Console, OpenAPI, and repository creation until the declared protocol
capabilities below are executable.

Normative implementation references are Microsoft's
[NuGet Server API overview](https://learn.microsoft.com/nuget/api/overview),
[package content API](https://learn.microsoft.com/nuget/api/package-base-address-resource),
[`.nuspec` reference](https://learn.microsoft.com/nuget/reference/nuspec), and
[package version rules](https://learn.microsoft.com/nuget/concepts/package-versioning).

## N1: immutable package and publication contract

- Keep package ID and version ownership inside the `.nupkg`; publication input
  may declare an expected identity but cannot override the manifest.
- Treat IDs case-insensitively and use the normalized NuGet version for
  uniqueness, so `1`, `1.0`, and `1.0.0.0` cannot become different artifacts.
- Add a quota-reserving, idempotent publication session and content-addressed
  staged object before any visible metadata is written.
- Specify duplicate normalized versions, repository signing policy, package
  signature verification, symbol-package scope, audit, and interrupted-upload
  cleanup explicitly.
- Freeze the PackagePublish resource and management session surface in OpenAPI
  only after Memory and PostgreSQL conformance exists.

Acceptance gate: bounded malformed ZIP/XML tests, official-client-built
`.nupkg` fixtures, normalized identity vectors cross-checked against
`NuGet.Versioning`, persistence conformance, upload recovery, and no visible
package before publication commits.

## N2: native Hosted restore

- Serve a V3 service index whose resource URLs are derived from the external
  request origin and repository path.
- Implement PackageBaseAddress package/version enumeration and `.nupkg` reads,
  Registrations metadata, and the minimum search/autocomplete resources needed
  by Visual Studio and `dotnet` clients.
- Preserve `GET`, `HEAD`, ranges, ETags, conditional requests, authorization,
  anonymous policy, capacity, browse, search, and stable deep links.
- Build registration metadata from committed package records and `.nuspec`
  dependencies; do not reconstruct identity from filenames.

Acceptance gate: a clean .NET SDK container adds the Gateway source, restores
an application and transitive dependencies, repeats offline from stored bytes,
and never observes a staged or partially indexed version.

## N3: Proxy and ordered Group

- Discover upstream resources from each V3 service index rather than assuming
  nuget.org URL shapes.
- Bound metadata/package caching, negative caching, redirects, upstream
  authentication, circuit breaking, and egress allowlists.
- Resolve Groups by normalized package ID and version with deterministic member
  ownership; a higher-priority identity claims the request before asset
  fallback is considered.
- Preserve repository source mapping semantics so private package namespaces do
  not silently fall through to public upstreams.

Acceptance gate: real `dotnet restore` covers Hosted, Proxy, ordered Group,
authenticated upstream, cache replay, normalized-version collisions, and
dependency-confusion boundaries.

## N4: lifecycle, scanning, and distribution parity

- Add tombstone, restore, retention, delayed reclaim, immutable promotion, and
  checkpointed replication over the package plus its owned metadata.
- Resolve `.nupkg` assets for manual and publication scanning; retain SBOM,
  license, vulnerability, signature, and provenance evidence without claiming
  unsupported scanner behavior.
- Apply quarantine admission and optional read enforcement to the normalized
  package identity across direct and Group reads.
- Extend backup/restore, metrics, audit, webhooks, upgrade compatibility,
  Console workflows, and runtime OpenAPI validation.

Acceptance gate: the full lifecycle/security matrix passes against Memory,
PostgreSQL, RustFS, workers, and official `dotnet`/NuGet clients. Only then may
the format catalog advertise NuGet as a supported repository type.

## Delivery order

APT Hosted remains the active format-completion priority and Cargo is the next
candidate. NuGet N1 through N4 are retained as a technically reviewed backlog,
but no publication work is scheduled. The existing parser stays covered while
capability discovery continues to describe only executable behavior.
