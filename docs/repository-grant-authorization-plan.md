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
- operation: `read`, `write`, or `admin`;
- policy: repository grant set and legacy static reader/writer patterns.

An administrator is always allowed. This preserves the bootstrap and recovery
path provided by `GATEWAY_ADMIN_TOKEN` and OIDC administrator subjects.

For non-administrators, a managed grant set is authoritative for its target
repository. `repositories:admin` includes write and read; `repositories:write`
includes read; `repositories:read` permits only read. Grants are exact
principal matches. A grant never grants access to another repository.

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
| Admin | `repositories:admin` | Repository grant replacement and future repository-scoped administrative mutations |

The existing control-plane administrator requirement remains in place for V2
repository/group lifecycle, retention, artifact, and publish-session API
routes in the first rollout. Those APIs become grant-aware only after their
per-resource visibility and error behavior are separately specified. A grant
therefore cannot be used to escalate into control-plane administration during
the protocol rollout.

## Groups and Proxies

Native protocol routes identify a repository name, while group and proxy routes
may resolve to several members. Authorization is evaluated against the actual
hosted member before data is returned or modified. A request may use a group
only if the principal is authorized for the selected member. The resolver must
continue to use its existing anonymous-member filtering before authorization.

For a group with multiple eligible members, an unauthorized member is skipped;
the resolver may continue to a later authorized eligible member. A request that
has no authorized eligible member is denied, not reported as a missing
artifact. This prevents group membership from exposing an unauthorized
repository through fallback behavior.

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
