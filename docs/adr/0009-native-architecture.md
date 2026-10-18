# Native Management Architecture and Migration Boundaries

[简体中文](0009-native-architecture.zh-CN.md) · [Documentation index](../README.md)

Status: accepted direction; implementation incremental (#277, #285, #380)

## Context and alternatives

The management adapter and aggregate store make new migration and format work
cross unrelated domains. Keeping Nexus aliases and freezing management APIs
would preserve that coupling. Two options were considered: preserve existing
interfaces and split files mechanically, or allow breaking management changes
and migrate domain by domain. We choose the second, following the product
decision in #380. Native package protocols remain standards compliant.

## Decision

- `cmd/gateway` composes explicit dependencies; production requires PostgreSQL
  and object storage. Domain services consume narrow interfaces.
- Management handlers live by domain, own HTTP/auth/audit orchestration, and
  use generated OpenAPI types only at the HTTP boundary. OpenAPI sources change
  first; server, Console and agctl move together in each breaking slice.
- Format modules own coordinates and wire behavior. They may consume shared
  protocol identity helpers and domain interfaces, but cannot depend on another
  format, the HTTP composition root, or PostgreSQL implementations.
- Domain records and store interfaces separate from SQL implementations under
  `internal/repository/postgres` or domain `pgstore` packages (#282). Until that
  extraction, importing `internal/repository` still brings mixed persistence;
  the guard does not claim SQL isolation is already achieved.
- Memory persistence is undecided until #278 compares deletion with shared
  contract tests and measures a Raw tracer. This ADR does not authorize replacing
  transaction, locking, lease, or object reference semantics with approximations.

No Nexus management API, historical Gateway management API, authentication
model, or historical URL alias is a compatibility requirement. Existing aliases
remain present until their own implementation slice removes them; this decision
does not itself change routing. Document removed operations, regenerate clients,
and change consumer configuration in the same release. No dual API is required.
Database migration files stay append-only; durable identities, digests,
authorization, idempotency, leases/fencing and reclamation safety remain required.

## Migration execution order

1. Establish this ADR and dependency gate (#277), decide test persistence (#278),
   then explicit composition (#279), domain handlers (#280), format modules
   (#281), persistence separation (#282), and shared HTTP behavior (#283).
2. In parallel with that ordering, scope #269 into source discovery/preflight and
   explicit repository mapping, durable manifests/checkpoints (#205), verified
   bytes (#204), then Console controls. Shared files merge sequentially.
3. Borrow kkRepo's metadata/byte phase separation and incremental task model
   from its [migration guide](https://github.com/klboke/kkRepo/blob/main/docs/en/nexus-migration-guide.md).
   Gateway instead uses read-only source discovery, explicit unsupported results,
   and native target identities. Nexus scripts, credential/hash migration and
   URL compatibility are not prerequisites. Maven release/Raw is the initial
   #269 scope; OCI closure (#206) and SNAPSHOT handling retain separate gates.
4. Validate real Maven/OCI consumers, final inventory and cold downloads before
   deployment/cutover. #321 was fixed by #324 and released in v0.8.1; production
   business acceptance remains distinct from that closed code issue. Source
   deletion and production routing are separate operations.

## Executable guard and baseline

`make architecture-check` parses all non-test Go sources below `internal`,
including inactive build-tag files, and follows the internal import graph.
It rejects new direct or transitive protocol dependencies on `internal/app`,
PostgreSQL implementations, and other format modules. Shared
`internal/protocol/identity` is not a format. Existing violations are individually
listed in `tools/architecture/baseline.json`; remove resolved entries in the same
change. New `GatewayStore` fields or methods fail by identity, even if the member
count stays flat. The gate runs through `make lint` in local and CI quality jobs.
Reviewers must reject additions to the baseline; it records debt, not permission
to introduce dependencies. No automatic baseline regeneration command is provided.

`make architecture-metrics` reports reproducible AST/source metrics. At base
`e9f53084`: 151 app production files / 39,096 lines, 158 repository production
files / 31,916 lines, 231 management adapter methods, 49 aggregate store members,
and 140 app switch cases containing a `Format*` selector. Lines include comments
and blanks; counts exclude tests and are not comparable with older all-file
counts. These are progress indicators, not arbitrary growth limits.

The initial five transitive cross-format exceptions arise through the mixed
repository package's Cargo dependency (Conan, Maven, npm, OCI and Raw sources,
including the applicable subpackages). Resolve them during #281/#282. This guard
covers source imports and aggregate members; it does not prove runtime semantics,
inspect dynamically loaded dependencies, or replace integration/client tests.
