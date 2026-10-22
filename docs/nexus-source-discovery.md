# Nexus source discovery and native mapping

[简体中文](nexus-source-discovery.zh-CN.md) · [Documentation index](README.md)

Issue #381 adds an administrator-only, read-only Nexus 3 source service. The
contract lives in `api/openapi/native-hosted.yaml` and generates the management
server and Console/CLI SDK types together. There is no `agctl` command yet; the
Console source workflow is described below. This service does not copy bytes, create repositories, schedule
jobs or perform production migration/cutover.

## Connection and credentials

`POST /api/v2/migration-sources` accepts `name`, `baseUrl` and optional
`credentials: {username, password}`. Only HTTPS is accepted; user information,
query strings, fragments and ambiguous context paths are refused. A Nexus context
path such as `https://nexus.example.org/nexus` is supported. Use a dedicated
read-only source account with access to the desired repositories and their
configuration. The service never enumerates source users, hashes, API keys,
security exports, scripts or task endpoints, and never sends a source write.

`GATEWAY_SETTINGS_ENCRYPTION_KEY` must decode to 32 bytes when storing credentials.
AES-GCM binds each encrypted credential to its source UUID. PostgreSQL stores
only ciphertext. GET/list responses return `credentialsConfigured`, never a
username, password or ciphertext. The key must be available to every API node;
missing/wrong keys fail explicitly rather than using anonymous access.

`GET /migration-sources` supports `limit` (1–100, default 20) and an opaque UUID
`cursor`; it returns `items` and `nextCursor`. GET by UUID returns the current
`version`. PUT and DELETE require this version in `If-Match`. Omit `credentials`
on PUT to retain them; replace the whole object to rotate them, or use
`clearCredentials: true` to remove them. Changing `baseUrl` requires explicitly
replacing or clearing configured credentials. Stale versions return 412. DELETE
removes the connection and ciphertext; it never changes the source or target.

All operations require a platform administrator without a pending password
change. Responses use `Cache-Control: no-store`; audit records retain source UUID,
operation, actor and result without source URLs, credentials or upstream bodies.
Audit uses the existing management audit sink; an unavailable sink is subject to
the deployment's audit monitoring and retention policy.

## Protected egress

The client reuses the Gateway egress factory, environment proxy policy, TLS
verification and DNS pinning. Every redirect is refused. Asset URLs must match the
configured HTTPS origin and exact format-specific source file route;
userinfo, query/fragment, traversal and a foreign origin fail. Equivalent percent
encoding is accepted only when its decoded path exactly matches safe asset
metadata: `%2B`/`%2b` may represent a literal `+`, and the returned URL is rebuilt
from the approved origin and metadata path. Encoded dot traversal and double
encoding cannot satisfy that exact match. Metadata paths containing literal `%`,
`#` or `?` remain unsupported. Raw/Maven use `/repository/<source-name>/<asset-path>`, OCI uses its validated
`v2/<name>/manifests|blobs/<reference>` storage identity, and Conan uses
`v2/conans/.../files/<file>`. Unknown shapes remain unsupported. Discovery
validates these URLs without downloading them; durable batch planning performs
any required graph and manifest reads.

Direct private endpoints are denied by default. An operator can set an exact
hostname and private CIDR allowlist, for example:

```sh
GATEWAY_MIGRATION_SOURCE_PRIVATE_ENDPOINTS=nexus.internal=10.20.30.0/24,10.20.30.7=10.20.30.7/32
```

The default Compose deployment forwards this setting from its environment file
to Gateway and inherited background roles; omitting it keeps the allowlist empty.

An entry permits only that hostname and addresses inside its RFC1918/ULA CIDR.
Wildcards, URL/port syntax, public CIDRs, broader-than-private ranges, loopback,
link-local and metadata addresses are refused. Resolved addresses are still pinned
and certificates must be trusted by the operating system. Source administrators
cannot change this process configuration through the API. When an environment
proxy is selected, the existing policy delegates DNS/network enforcement to that
trusted proxy. This setting neither disables TLS nor approves redirects/downloads.

## Preflight and bounded discovery

`POST /migration-sources/{sourceId}/preflight` reads the repositories visible to
the configured credential and format-specific configuration. Its
`visibility: credential_visible` is deliberately not a claim of a globally
complete source inventory. Empty results cannot prove there are no inaccessible
repositories. Preflight inspects at most 100 repositories; the source list itself
is capped at 500 entries.

`POST /migration-sources/{sourceId}/discovery` accepts explicit `repositories`
(1–10 unique names), `maxPages` (1–100, default 25) and `maxAssets` (1–10,000,
default 10,000). It exhausts the Nexus Assets API continuation tokens (Components API for npm identities) for selected
supported Hosted repositories. Budgets apply to the entire operation: 128 source
requests, 2 MiB per decoded response, 16 MiB total and 30 seconds including source
lookup, DNS, TLS and pagination. Supported and unsupported asset entries both
consume the asset budget. There are no automatic retries.

Raw, Maven RELEASE, OCI (`docker`), Conan and npm Hosted assets are supported. npm component names/versions come from Components REST, with exactly one tarball asset per version; packument and archive closure validation happens in the durable batch. MIXED Maven repositories
return identifiable release-version paths; SNAPSHOT directories, shared
`maven-metadata.xml` and unclassifiable paths appear separately in
`unsupportedAssets` with a reason. A release is identified by Maven's
`group/artifact/version/artifact-version…` layout. SNAPSHOT-only repositories,
unknown formats/types and unsafe/unreadable settings are explicit unsupported
results. Maven RELEASE repositories containing a SNAPSHOT path fail the operation.
Proxy/Group repositories return configuration and ordered member dependencies
only; their cached/group asset inventory is not copied or enumerated.

A successful result has `state: complete`, `sourceVersion`, repositories, assets,
unsupported assets and request/page/byte statistics. This is a bounded observation
of the visible source at request time, not a durable manifest or a consistent
snapshot during concurrent source publication. A response interrupted by failed
pagination, repeated/cyclic continuation tokens, duplicate paths, untrusted asset
URLs, invalid JSON or exhausted budgets fails without returning partial success.
401/403 upstream responses, TLS failures and timeouts never become empty success.
Problem responses expose bounded codes only: `source_unauthorized`,
`source_forbidden`, `source_tls_failed`, `source_timeout`,
`source_pagination_cycle`, `source_download_url_denied`,
`source_limit_exceeded`, and related declared failures. Upstream failures use 502,
timeouts 504, budget/visibility/snapshot failures 422, and unavailable encryption
or target lookup 503. Invalid source input is 400 and a missing source is 404.

## Explicit mapping preview

`POST /migration-sources/{sourceId}/mapping-preview` accepts `mappings`, each
containing `sourceRepository`, native `targetName`, `format` (`raw`/`maven`/`oci`/`conan`/`npm`),
`type` (`hosted`/`proxy`/`group`) and optional ordered `targetMembers`. It re-reads
source configuration and checks native names, duplicate targets, compatible
existing target repositories, format/type and each Group member's explicit
mapping. Missing/incompatible dependencies and reordered members are invalid.

Each source field is classified as `expressible`, `manual` or `unsupported`.
Examples include native repository names and ordered members, manual storage/cache
policy choices, and unsupported source authentication/security configuration.
Source credential and permission models are never copied. Unknown field values
and upstream credential values are not echoed. `ready` validates Hosted mapping
shape; Proxy/Group `manual` means native policies/credentials still need explicit
configuration. `invalid` and `unsupported` make `valid: false`. The preview writes
no source or target business resources. Actual provisioning continues to use the
existing supported repository/Group contracts.

The PostgreSQL migration is append-only `000146_nexus_migration_sources.sql`.
Synthetic Nexus fixtures cover pagination, release/MIXED/SNAPSHOT, multiple
repositories, unknown types, interruption/cycles, hostile links, permissions, TLS,
timeouts, budgets and operator private-address policy. Real PostgreSQL tests cover
HTTP create/read/restart/update/clear/rotate/delete, ciphertext, stale versions,
paging and concurrent compare-and-swap. `make integration-test` (also run by CI)
executes the source HTTP tests in `internal/app` and the independent
`internal/nexussource/pgstore` package against a newly created PostgreSQL/RustFS
fixture. The contract gate verifies that every package requiring the integration
build tag is included in that authoritative Compose test command. These are local
fixture evidence; they do
not constitute real-source migration, remote CI or production acceptance.

## Console source workflow

Open **Operations → Migration sources**, or use
`/operations?tab=migrations&source=<source UUID>` for a saved connection. The
route and its deep links require a platform administrator, as do the APIs. The
Console uses the generated management client for paginated source list/detail,
create/update/delete, preflight, discovery and mapping preview.

Credentials are write-only form inputs. Keeping credentials omits the field;
replacement sends a new pair; clearing sends `clearCredentials: true`. Inputs
clear after every submission, including failures, and when the editor or tab
closes. They never enter browser storage, URL state, query keys or query/mutation
caches. Connection versions travel in `If-Match`; a conflict asks the operator
to read the latest version rather than silently overwriting it. Error text is
selected from bounded failure codes and never echoes a transport/upstream body.

Preflight labels its credential-visible scope. Select at most ten repositories
and enter each native target name explicitly. Only selected supported Hosted
repositories enter asset discovery; Proxy/Group remain configuration previews.
Group member targets follow the source's declared member order and must all be
mapped explicitly. Discovery distinguishes supported assets and unsupported
paths, and labels summed asset size as source metadata, not transferred or
verified bytes. Mapping results distinguish validation, manual configuration,
unsupported fields and conflicts. Supported Hosted mappings can continue to the
[batch form](nexus-migration-console.md), where existing target IDs and fresh
capacity evidence must be explicitly confirmed before a durable batch is created.

Refresh failures keep the previous successful observation with an error. Changing
the source, its connection version, selected repositories or target inputs
invalidates the observations that depend on them; late responses cannot replace
another source's result. Reopening reads the saved connection but does not invent
a persisted discovery snapshot. The source view does not copy bytes. Use the
separate batch view for durable execution and honest acceptance observations;
target-only cold-client evidence remains unverified until obtained for that batch.

Focused state tests cover initial/refetch failures, retained data, pagination,
source/version races, ordered mapping and credential lifecycle. The real-route
Playwright layout gate covers 1440px and 390px, both languages/themes, bounded
16/24px flow, columns, long paths, overflow and administrator deep-link denial.
`console/e2e/migration-sources-live.spec.ts` additionally exercises the real
Gateway and PostgreSQL against `console/e2e/support/nexusSourceFixture.mjs`:
create/read, preflight, multi-repository discovery/preview, page reopen, credential
rotation/clear, read refusal and deletion. Run it with `GATEWAY_ADMIN_TOKEN`,
`GATEWAY_RESOLVER_TOKEN` and `MIGRATION_SOURCE_FIXTURE_URL` against an isolated
Gateway. The TLS fixture needs a test certificate trusted by that Gateway and an
operator-owned exact-host/private-address allowlist; neither TLS nor private
network enforcement is bypassed. This fixture gate does not prove real Nexus
inventory completeness, byte migration, remote CI or production readiness.

Source API behavior follows Sonatype's official
[Repositories API](https://help.sonatype.com/en/repositories-api.html),
[Assets API](https://help.sonatype.com/en/assets-api.html) and
[pagination](https://help.sonatype.com/en/pagination.html). The metadata/byte
separation also follows the direction recorded in [ADR 0009](adr/0009-native-architecture.md).
