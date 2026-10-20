# Management API Domain Composition

[简体中文](0012-management-domain-composition.zh-CN.md) · [Documentation index](../README.md)

Status: accepted; implemented domain by domain under #280, starting with #392.

## Problem and alternatives

The generated management interface describes every operation, but one adapter
also owned every dependency. File splitting alone leaves that coupling intact.

1. Keep one adapter and a common aggregate dependency: fewer constructors, but
   unrelated domains remain coupled and cannot be constructed independently.
2. Compose domain handlers implementing disjoint subsets of the generated
   interface: explicit domain constructors and narrow ports, with one compile-time
   assertion proving the complete management surface.
3. Migrate all handlers to the generated strict interface at the same time:
   typed request/response objects, but a second whole-surface migration that also
   changes streaming upload/download orchestration and error ownership.

Choose option 2. The pinned generator already emits strict-server types; their
existence does not require using them. Strict request/response adapters can be
considered for a future concrete domain contract change. They do not replace
splitting domain dependencies, and are not a prerequisite for #280.

## Decision and implementation

`managementServer` composes domain handlers by embedding. Each domain owns its
operations once, declares its store ports, and has an independent constructor.
The generated `ServerInterface` assertion rejects missing or duplicate methods.
Generated routing and parameter binding stay at the HTTP edge. The first slice does
not change OpenAPI, Console or agctl behavior and introduces no second API.

`managementScope` contains only repository lookup, an explicit anonymous-policy
reader, audit recording, authentication, authorization and denial metrics. It
carries no `GatewayStore`, identity store, lifecycle queue, or format store.
Domain persistence is supplied separately. Anonymous policy is not inferred
from the concrete type behind the repository lookup port.

The first domain is repository/Group configuration: repository CRUD and
capabilities, format profiles, Group CRUD/members/capacity/resolution, and the
repository egress probe. Artifact browsing, access, identity, lifecycle,
operations, site and APT follow in separate PRs. The temporary legacy adapter
owns only operations that have not moved. It is removed after the last domain;
it is not a compatibility surface.

## Artifact discovery and publishing

The second slice (#396) extracts `artifactManagementHandler`. Catalog lookup,
per-format search, browse, usage and identity queries are explicit ports. Search
no longer obtains a full store through the native Maven publisher. Usage and
identity capability checks happen in composition and are injected explicitly;
missing optional capabilities still return their documented `not_supported`.
The native Maven/Conan publishers own streaming and transactional publication;
the domain receives only their operation interfaces. Indexed proxy directory
queries and the Maven navigation cache are also explicit dependencies.

Shared Group authorization/resolution only requires repository lookup. The
anonymous-policy read path requires only a read method, so a domain does not
need policy-write permission to serve an allowed anonymous browse request.
Signed pagination, repository/actor scope and cache/configuration revisions
retain their existing semantics.

The repository catalog and authorization scope are construction requirements;
format queries, publishers and aggregate queries can be supplied independently.
An authorized request needing an unconfigured capability returns
`501/not_supported`, explicitly described by the contract and generated outputs.
It neither panics nor treats missing capabilities as empty inventories. Account
state and repository permission checks run first; global search filters unreadable
repositories first. Without global session or Group metadata, administrators get
501 and other authenticated callers retain an opaque 404. Session metadata without
a publisher still permits resource authorization before reporting 501. Group
recursive search and browse also report missing capabilities for readable members.

## Access control

The third slice (#400) extracts `accessManagementHandler` for grants, roles,
templates and effective access. Its only domain stores are those four ports;
repository authorization, principal-wide blocks and denial audit use the shared
scope. Aggregated grant records are an explicit optional read port, rather than
a runtime assertion on the grants writer. Cross-repository scope filtering and
audit actor resolution are shared authorization helpers.

## Identity administration

The fourth slice (#431) extracts `identityManagementHandler`. All 25 identity
operations, including current identity, users, sessions, external identity links,
API keys, service accounts and OIDC settings, belong to this handler. Its explicit
ports contain only the management methods each record needs: API-key hash
lookups, login writes, session creation and expiry jobs are not part of them.
OIDC exposes only settings, replacement and connection testing; its runtime is
injected explicitly when configured. Authentication and audit use the shared
scope, without repository content, queues or site persistence.

Independent generated-HTTP tests construct this domain without `GatewayStore`
or repository/catalog ports. They exercise user CAS and password/session
management, one-time credential responses, revocation and audit. All 24
administrator operations reject unauthenticated, member and password-change
requests before touching domain stores. Current identity remains available to
authenticated members. Existing identity and OIDC lifecycle tests continue to
exercise the full composition. Lifecycle, operations, site and APT remain on
the temporary adapter; extracting identity does not complete #280.

## Verification and limits

Construct domain handlers directly with only their repository/Group ports and
exercise the generated HTTP boundary. Verify filtering, authorization and denial
audit, idempotency, Group membership checks and version conflicts. Keep the
full Go unit, coverage, lint, vet, OpenAPI and documentation gates.

This refactor neither changes persistence nor moves package protocol handlers.
It does not claim complete SQL-package isolation (#282), shared middleware
extraction (#283), or production acceptance.
