# Repository Grant Runtime Authorization Plan

[简体中文](repository-grant-authorization-plan.zh-CN.md) | [Documentation index](README.md)

## Purpose

Repository grants are currently versioned, persistent management data. This
plan promotes them to the authorization source for hosted repositories without
changing the authentication mechanisms or protocol-level error contracts.

The rollout is deliberately incremental. A repository must retain the current
static-policy behavior until an operator explicitly manages its grant set.

## Authorization Model

Authentication establishes a `Principal`; authorization evaluates one
repository operation for that principal. The evaluator returns an allow or deny
decision together with a stable source and reason for audit and metrics. It
does not write HTTP responses and does not parse protocol credentials.

The evaluator has these inputs:

- principal: authenticated actor and administrator bit;
- target: hosted repository ID, name, and format;
- operation: `read`, `write`, `intelligence`, or `admin`;
- policy: repository grant set and legacy static reader/writer patterns.

An administrator is always allowed. This preserves the bootstrap and recovery
path provided by `GATEWAY_ADMIN_TOKEN` and OIDC administrator subjects.

For any principal the evaluator applies one fixed order: a `none` account state
is denied, the administrator identity is allowed, a role allowed by
`RoleAllows(principal.Role, operation)` is allowed, then the principal's
per-repository grants are consulted, and the legacy static policy decides last.
Consequently only the administrator level reaches past grants: the global
`reader` and `writer` roles that used to cover every repository are removed, and
a `member` holds no repository capability of its own, so per-repository grants
are what decide for an ordinary account. `repositories:admin`
includes write, read, and intelligence writes; `repositories:write` includes
read; `repositories:read` permits only read; and `repositories:intelligence` is
an independent metadata-writing capability that does not imply any repository
read, publish, delete, or administration access.

Service Accounts intentionally have no global role and authenticate only
through explicit Repository Grants, which is the recommended shape for CI,
scanner, and third-party application credentials. Their credentials rotate
without changing the stable `service-account:<id>` principal. Standalone API
Keys retain their own global roles and are not interchangeable with Service
Account credentials. Grants are exact principal matches. A grant never grants
access to another repository. A grant may also carry an optional resource
prefix (`migrations/000048_repository_grant_resource_prefixes.sql`) that bounds
it to resources whose identity begins with the prefix; `grantMatchesResource`
implements this rule and an empty prefix matches every resource of the
repository.

Authorization Roles and Authorization Templates are reusable management
objects over grants. A role
(`migrations/000091_authorization_roles.sql`) is a named bundle of
`repositories:*` scopes: selecting one in a grant editor copies its scopes into
the grant as an explicit snapshot, so a later role edit cannot silently change
an already-persisted decision. A template
(`migrations/000083_authorization_templates.sql`) is a reusable grant bundle:
applying it to one repository replaces that repository's grant set with the
template's rules, after those rules are validated against the target
repository's format, and advances the stored version that marks a set managed.
Role and template management is administrator-only.

Until a repository has a managed grant set, legacy patterns remain in force:

- every protocol retains its current pre-grant authorization behavior; Maven
  and group/proxy read paths continue to use their static patterns, while
  Native Raw retains its existing authenticated-principal behavior;
- static maps retain their present wildcard semantics where they already apply;
- an absent reader map denies an unmatched caller, unless
  `GATEWAY_LEGACY_READ_DEFAULT=allow` restores the pre-0.4 posture.

The repository store exposes an unmodified default grant set as version `1`.
A successful `ReplaceRepositoryGrants`, including replacement with `[]`, moves
the version above `1`; that is the durable marker that grants are managed. An
explicit empty managed set denies every principal that reaches the grant stage,
and it revokes every non-administrator whose reach came from grants rather than
from the administrator level. This makes a new
deployment backward compatible while still making revocation possible without
deleting policy state.

## Operation Mapping

| Operation | Required scope | Routes |
| --- | --- | --- |
| Read | `repositories:read` | Native Maven download, OCI blob/manifest/tag fetch, Raw GET/HEAD, Conan proxy/read-through |
| Write | `repositories:write` | Native Maven publication, OCI upload/manifest/delete, Raw PUT/DELETE |
| Intelligence | `repositories:intelligence` | Write signatures, SBOM, provenance, license, and vulnerability summaries for an existing visible artifact without publish/delete/admin access |
| Admin | `repositories:admin` | Repository grant replacement and future repository-scoped administrative mutations; also includes intelligence writes |

V2 separates global discovery from known-resource operations. A principal with
an applicable `read` grant (including `write` and `admin`) may read the known
Repository's detail, retention policy, artifacts, and publish sessions. A
`write` grant may perform Repository-scoped mutations; `admin` manages grants.

`GET /api/v2/repositories` is permission-scoped rather than administrator-only:
an administrator sees every Repository, and any other authenticated principal
sees only the Repositories its global role or its grants allow it to read. A
Pending (`none`) account is refused. Filtering runs on the server over the
caller's own effective read permission, so the list never contains a Repository
the caller cannot read. The audit list, Repository/Group lifecycle, and the
remaining global management discovery routes stay administrator-only.

`GET /api/v2/repositories/{id}` still answers `404` for an absent Repository and
`403` for one that exists but is not readable, so a caller that already knows an
identifier can still confirm it exists. Bringing that response in line with the
list is tracked with the effective-access work; this plan does not claim it yet.

## Groups and Proxies

Native protocol routes identify a repository name, while Group and Proxy routes
may resolve to several members. Managed grants can be evaluated for a Group
member only when its persisted `repositoryId` explicitly identifies an active,
format-matching Repository. The runtime must never infer this relationship from
a member name, Group name, path, or endpoint.

Conan already carries this explicit binding. OCI, Maven, and Raw legacy Group
members do not yet persist it and therefore retain their legacy static-policy
behavior until the binding and per-candidate enforcement rollout is complete.
This is intentional compatibility behavior, not an authorization fallback for
an explicitly bound member.

The target candidate algorithm is shared by all formats:

| Candidate state | Result before cache or upstream access |
| --- | --- |
| Anonymous and not enabled by the existing Group/member policy | Exclude before authorization and cache lookup. |
| Authenticated, explicitly bound, managed grant allows read | Candidate is eligible; inspect only that candidate's cache and then its source. |
| Authenticated, explicitly bound, managed grant denies or lookup fails | Audit the bounded decision, increment grant-denial metrics, and skip the candidate. |
| Authenticated, unbound legacy member | Apply the format's existing static-policy behavior unchanged. |

If an authenticated request exhausts candidates solely because explicitly bound
members were denied, it returns the format's existing access-denied response,
never `404`. A later authorized candidate may still resolve successfully; this
does not reveal the denied member because it is not fetched, cached, or named
in the client response. A positive or negative cache entry is eligible only
after its recorded source member has passed the same candidate authorization
check. Authorization denials and failures are never cached.

| Format | Current bound-member behavior | Target terminal denial |
| --- | --- | --- |
| OCI Group | Legacy group-level static policy only | Registry `403 DENIED` response |
| Maven Group | Legacy group-level static policy only | Existing Maven `403` response |
| Raw Group | Legacy group and first-member static policy | Existing Raw `403` response |
| Conan Group | Bound members are skipped; unbound members use legacy policy | Existing Conan `403` response |

Conan has no native hosted artifact endpoint, so a managed Conan Repository is
an authorization target for a read-through remote. A Conan Group member opts
into grant evaluation by carrying its stable `repositoryId`, which must refer
to an active `format: conan` Repository. Unbound legacy members retain their
existing static policy. The runtime never infers this relationship from a
member name or endpoint.

## Protocol Contract

Authorization denial must preserve the protocol's established response:

- Maven and Raw return their current Basic authentication challenge/status.
- OCI retains its Registry Bearer `WWW-Authenticate` challenge and status.
- Management routes retain `application/problem+json` and their existing
  unauthenticated `access_denied` behavior.

The evaluator's source and reason are recorded in the audit log for
authorization denials on Maven, OCI, Raw, and Conan, legacy and native, and on
the management routes. npm, PyPI, Go, and APT denials record the bounded denial
counter but not the decision fields, which is tracked as separate work. Neither
is exposed through an artifact-not-found response or a principal-specific error
message. The fields are bounded policy values, never tokens, credentials, or
principal-derived labels.

`GET /api/v2/audits` is the administrator-only management API for these
records. Its `AuditRecord.authorizationSource` and
`AuditRecord.authorizationReason` fields are optional: they are present only
when a request reached a repository authorization decision. Current values are
bounded policy vocabulary, but API consumers must accept future bounded values
and treat an absent field as "no repository authorization decision". The
legacy `/api/v1/audits` response remains unchanged for V1 consumers.

## V1 Compatibility Surface

The `/api/v1/**` handlers remain a platform-administrator operation and do not
take part in the platform/repository split. They predate per-repository
authority, and a V1 Group member may carry no repository binding at all, so
nothing attributes it to a repository an administrator could hold. The two tiers
live on the v2 surface: hosted repositories, hosted groups, and the management
views over them. A V1 client that needs repository-scoped administration has to
move to `/api/v2`.

## Decision Vocabulary

Every repository authorization decision names the authority that reached it, so
the effective-access explainer and the audit record say which stage decided
rather than only whether access was allowed. These are the current bounded
values; a consumer must tolerate new bounded values, and absence of the fields
means the request never reached a repository authorization decision.

`authorizationSource`:

| Value | Emitted by |
| --- | --- |
| `administrator` | The platform administrator identity: the `admin` level, an administrator subject, or the static administrator token |
| `role` | A global account level; the reason names it |
| `repository_grants` | The per-repository grant set, either marked managed or naming this principal |
| `legacy_static` | The configured reader and writer patterns in `GATEWAY_REPOSITORY_READERS` and `GATEWAY_REPOSITORY_WRITERS` |
| `legacy_protocol` | The native protocol fallback that admits any authenticated principal: npm, PyPI, OCI, Raw, and APT |

`authorizationReason`:

| Value | Meaning |
| --- | --- |
| `administrator` | The administrator identity decided |
| `role_admin`, `role_member` | The named global level decided. Only `role_admin` allows; the other levels fall through to the grant set |
| `scope_granted` | A grant matched the principal, the operation, and the resource |
| `scope_not_granted` | The grant set applied and matched nothing |
| `grant_lookup_failed` | The grant set could not be read, so access is denied |
| `read_pattern_granted`, `write_pattern_granted` | A configured legacy static pattern matched |
| `authenticated` | The native protocol fallback admitted an authenticated principal |
| `repository_anonymous_read_enabled`, `repository_anonymous_read_disabled`, `global_anonymous_access_disabled`, `repository_not_active` | The anonymous-read explanation the effective-access answer adds; these never reach the denial counter |

The denial counter narrows this vocabulary further on purpose: it counts only
the grant stage, because its labels must stay bounded to a small set an operator
can alert on. Snapshot, quarantine, and password decisions use their own
sources; they are not repository authorization decisions and are not listed
here.

## Console Capability Derivation

The session payload keeps its current shape: `{actor, kind, role?, administrator,
oidc?}` from `/auth/session` and `/api/v2/identity`. It deliberately carries no
capability set. The two tiers are derived in the Console from data the session
already holds, in one module (`console/src/lib/authorization.ts`) that every
navigation item, route guard, and repository surface reads:

- `platformCapabilities(identity)` turns `administrator` into `platformAdmin`,
  `role === "none"` into `pending`, and either into `browseRepositories`.
- `repositoryPermissions(access)` turns one repository's effective-access answer
  into `read`, `write`, `administer`, and `intelligence`.

The repository tier cannot live in the session: it is answered per repository,
and the effective-access response is already the authoritative answer the server
enforces. A capability set on the session would be a second copy of a
per-repository decision, issued before any repository is named, and the two
could disagree. The platform administrator keeps every repository surface
regardless of the access answer, so the Console's own administration does not
depend on it.

## Metrics

`artifact_gateway_repository_authorization_denials_total` counts denied
decisions produced by an explicitly managed Repository grant set. It has only
these bounded labels:

- `format`: `management`, `maven`, `oci`, `raw`, `conan`, `npm`, `pypi`, `go`, or `apt`;
- `authorization_source`: currently the fixed value `repository_grants`;
- `authorization_reason`: `scope_not_granted` or `grant_lookup_failed`.

The metric must never label actor, repository name or ID, member, artifact
path, coordinate, request ID, trace ID, endpoint, or upstream host. Operators
can identify a policy rollout problem with:

```promql
sum by (format, authorization_reason) (
  increase(artifact_gateway_repository_authorization_denials_total[15m])
)
```

Legacy static-policy and unauthenticated protocol denials retain their
existing metrics. They are deliberately excluded from this grant-specific
counter so an operator can distinguish a grant rollout from pre-existing
authentication or static-policy failures.

## Rollout and Rollback

1. Add the evaluator with unit tests for hierarchy, legacy fallback, explicit
   empty grants, and decision metadata.
2. Wire it into Native Maven, then OCI, Raw, and Conan one protocol at a time;
   each slice includes allow/deny E2E coverage and V1 regression coverage.
3. Add Memory/Postgres parity, concurrent replacement checks, audit fields,
   metrics, and dashboard-safe labels.
4. Extend the management surface only after its scoped authorization contract
   is approved and tested.

Rollback is configuration- and data-safe: removing runtime use of the
evaluator returns protocols to static policy without rewriting grants. Restoring
the evaluator re-applies each persisted grant set immediately; no authorization
cache is permitted in the initial release.
