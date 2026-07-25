# ADR 0002: Conan Hosted V3 Boundary

## Status

Accepted.

## Context

`/conan/v2/<group>/...` is an established Conan 2 Group/Proxy read-through
contract. Its routes, authentication handshake, caching, and error behavior
must remain compatible. Native Conan lifecycle metadata now exists in the
repository layer, but exposing publication through that V2 surface would make
the existing Group route ambiguous and alter its documented read-only meaning.

## Decision

Native Conan Hosted operations use `/conan/v3/<repository>/...`. The repository
name resolves only an active `format: conan` Hosted Repository; it never falls
back to a Conan Group or Proxy member.

The V3 implementation is staged:

1. Read immutable recipe/package revision metadata and files from native
   metadata/object storage.
2. Add an explicit publication session that stages verified objects before
   atomically making a recipe or package revision visible.
3. Add logical deletion, tombstone inspection, and repository-scoped reclaim
   jobs.

V3 authorization uses existing repository grants: read for resolution, write
for staging/publication/deletion. It does not forward client credentials.

## Consequences

- Existing `/conan/v2` client behavior remains unchanged.
- Native Conan publication is an additive API with an explicit compatibility
  boundary, rather than an implicit extension of a proxy protocol.
- Conan client interoperability is established through V3 black-box fixtures
  before claiming standard Conan remote upload compatibility.
