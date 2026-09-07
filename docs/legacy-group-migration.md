# Legacy Group Migration

[简体中文](legacy-group-migration.zh-CN.md) · [Documentation index](README.md)

Legacy OCI, Maven, Raw, and Conan Groups remain protocol-specific compatibility
surfaces. They are not rewritten in place. Migrate by creating a V2 Group over
explicit Hosted and Proxy Repositories, then switch clients after validation.

## Procedure

1. Inventory the Legacy Group's ordered members, upstream endpoints, host
   allowlists, anonymous settings, and consumer URLs.
2. Create one V2 Hosted or Proxy Repository for each member. Preserve format,
   endpoint, allowed hosts, and Repository-specific authorization grants.
3. Create a V2 Group with the same format and member order. Bind every member
   by `repositoryId`; explicit bindings are required for managed grant checks.
4. Keep anonymous access disabled while validating authenticated reads. If
   public reads are required, enable the global policy, Group policy, and each
   member Repository policy deliberately.
5. Compare protocol reads, cache behavior, audit records, and ordering against
   the Legacy Group. Switch client URLs only after those checks pass.
6. Retain the Legacy Group until all clients have moved. Deleting a V2 Group
   never deletes its member Repositories or Artifact bytes.

## Boundaries

### Maven resolution and cache compatibility

Maven V2 Groups try all Hosted members first, then the eligible Proxy members
in configured order. A Proxy 404 continues to the next member for GET and HEAD.
The existing Maven retry and failure policy still applies: an upstream failure
can fall back to a later member, but a request with unresolved failures does not
become a cached Group-wide 404.

New positive and negative entries carry a versioned resolution-scope digest for
the ordered authorized candidates, repository bindings, endpoints, allowlists,
anonymous eligibility, and egress configuration. Every cache read still checks
the source member. A changed scope invalidates the index and retries resolution;
the underlying immutable objects follow normal reclamation. A successful
fallback after an earlier failure or proxy denial is served without caching an
aggregate result, so recovery can restore the earlier member's precedence.

Older entries without a scope remain usable for a single authorized Proxy with
a matching allowed source. They cannot prove a multi-member Group result and
are refreshed on the first read. PostgreSQL JSON indexes preserve the scope
without a schema migration; cache keys and management invalidation stay stable.
Readers with identical eligible candidates can share the result, while changed
grants or anonymous eligibility trigger resolution against the new candidates.

`make native-maven-e2e` verifies Maven release and Gradle SNAPSHOT reads through
a two-Proxy Group whose first upstream returns 404, including metadata and
checksum sidecars. The HTTP and PostgreSQL suites cover cache compatibility,
candidate changes, Range/conditional reads, failure recovery, and grant isolation.

### Ownership and migration

- A V2 Group is an ordered view and owns no Artifact or cache bytes.
- Capacity is reported as member contributions, not Group-owned storage.
- Repository grants apply to explicitly bound V2 members. Unbound Legacy
  members retain their legacy static-policy behavior until migrated.
- Do not migrate by copying cache objects or altering a Legacy Group's rows.
  Proxy cache warms naturally from client reads and orphan collection reclaims
  unreferenced bytes on its normal schedule.
