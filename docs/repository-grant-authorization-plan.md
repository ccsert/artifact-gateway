# Repository Grant Runtime Authorization Plan

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

For non-administrators, a managed grant set is authoritative for its target
repository. `repositories:admin` includes write, read, and intelligence writes;
`repositories:write` includes read; `repositories:read` permits only read; and
`repositories:intelligence` is an independent metadata-writing capability that
does not imply any repository read, publish, delete, or administration access.
API keys may intentionally have no global role and authenticate only through
explicit repository grants, which is the recommended shape for CI and scanner
credentials. Grants are exact principal matches. A grant never grants access to
another repository.

Until a repository has a managed grant set, legacy patterns remain in force:

- every protocol retains its current pre-grant authorization behavior; Maven
  and group/proxy read paths continue to use their static patterns, while
  Native Raw retains its existing authenticated-principal behavior;
- static maps retain their present wildcard semantics where they already apply;
- an absent reader map retains the existing local-development unrestricted-read
  behavior.

The repository store exposes an unmodified default grant set as version `1`.
A successful `ReplaceRepositoryGrants`, including replacement with `[]`, moves
the version above `1`; that is the durable marker that grants are managed. An
explicit empty managed set denies every non-administrator. This makes a new
deployment backward compatible while still making revocation possible without
deleting policy state.

## Operation Mapping

| Operation | Required scope | Routes |
| --- | --- | --- |
| Read | `repositories:read` | Native Maven download, OCI blob/manifest/tag fetch, Raw GET/HEAD, Conan proxy/read-through |
| Write | `repositories:write` | Native Maven publication, OCI upload/manifest/delete, Raw PUT/DELETE |
| Intelligence | `repositories:intelligence` | Write signatures, SBOM, provenance, license, and vulnerability summaries without publish/delete/admin access |
| Admin | `repositories:admin` | Repository grant replacement and future repository-scoped administrative mutations; also includes intelligence writes |

V2 separates global discovery from known-resource operations. A principal with
an applicable `read` grant (including `write` and `admin`) may read the known
Repository's detail, retention policy, artifacts, and publish sessions. A
`write` grant may perform Repository-scoped mutations; `admin` manages grants.
The Repository list, audit list, Repository/Group lifecycle, and other global
management discovery routes remain administrator-only. A scoped grant is not a
discovery grant: it never enumerates Repository metadata, groups, audit events,
or pagination state. This preserves V1 management behavior and avoids turning
an empty filtered list into an existence oracle.

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
authorization denials, not exposed through an artifact-not-found response or a
principal-specific error message. The fields are bounded policy values, never
tokens, credentials, or principal-derived labels.

`GET /api/v2/audits` is the administrator-only management API for these
records. Its `AuditRecord.authorizationSource` and
`AuditRecord.authorizationReason` fields are optional: they are present only
when a request reached a repository authorization decision. Current values are
bounded policy vocabulary, but API consumers must accept future bounded values
and treat an absent field as "no repository authorization decision". The
legacy `/api/v1/audits` response remains unchanged for V1 consumers.

## Metrics

`artifact_gateway_repository_authorization_denials_total` counts denied
decisions produced by an explicitly managed Repository grant set. It has only
these bounded labels:

- `format`: `management`, `maven`, `oci`, `raw`, or `conan`;
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
