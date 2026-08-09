# Artifact Format Extension Guide

Artifact Gateway admits a format only as a complete repository capability, not
as an enum value or a Console option. The server-owned catalog in
`internal/repository/format_profiles.go` is the admission point, and
`GET /api/v2/formats` is its generated management projection.

## Admission Gate

A format profile may be added only when the same change set has an owned plan
for every declared capability:

1. Define canonical package, version, asset, and immutable digest identities.
2. Add forward-only PostgreSQL migrations and in-memory/PostgreSQL store parity.
3. Implement native Hosted publication and reads using protocol-compatible
   clients, including integrity validation and atomic visibility. A format
   whose ecosystem has no publication protocol may instead follow
   `docs/adr/0003-protocol-only-formats.md` and declare only Proxy and Group.
4. Implement Proxy caching and Group resolution with format-specific cache keys,
   negative caching, upstream protection, and ordered member behavior.
5. Add management browse/search projections and stable artifact/version deep
   links. Global search must be able to return a directly addressable result.
6. Implement logical deletion, tombstones, restore, retention, delayed reclaim,
   promotion, and checkpointed replication before declaring those operations.
   Protocol-only formats must omit unsupported lifecycle operations rather than
   exposing placeholders.
7. Apply repository grants, anonymous-read gates, audit fields, bounded metrics,
   worker-format filtering, capacity, quota, backup, and recovery behavior.
8. Extend OpenAPI, generated Go/TypeScript clients, Console selectors, protocol
   compatibility documentation, unit/integration tests, and a black-box client
   fixture.

Do not add a format to `Format`, `FormatProfile`, or the Console while one of
these requirements is represented only by a placeholder handler. A development
branch may stage the work, but the admitted profile must describe behavior that
is executable in the same revision.

## Capability Rules

- `RepositoryTypes` controls which repository kinds can be created.
- `GroupSupported` controls Group creation; it does not imply that arbitrary
  repositories can be mixed. Every member must still be active and match the
  Group format.
- `AnonymousRead` means the protocol can honor the global and repository/Group
  anonymous policy. It does not enable anonymous access by default.
- `HostedOperations` and `ProxyOperations` must match executable management and
  protocol behavior. `GET /repositories/{id}/capabilities` is derived from the
  same profile and must not maintain a separate list.

The profile helpers return defensive copies. Consumers should use
`SupportedFormatProfiles`, `SupportedFormats`, `FormatProfileFor`, and the
capability predicates instead of introducing another format list.

## Required Verification

At minimum, a format addition runs:

```sh
make openapi-check
go test ./...
go vet ./...
make console-typecheck
make console-check
make console-test
make console-build
```

It also adds a protocol-native end-to-end fixture and persistent PostgreSQL/S3
integration coverage for publish, resolve, delete, restore, retention, reclaim,
promotion, replication, and upgrade/rollback behavior.
