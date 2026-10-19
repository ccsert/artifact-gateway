# Memory Stores as Explicit Contract-Test Fixtures

[简体中文](0010-memory-store-contracts.zh-CN.md) · [Documentation index](../README.md)

Status: accepted; Raw tracer implemented (#278)

## Context and alternatives

Gateway maintains memory and PostgreSQL implementations of its storage
interfaces. Memory is useful for isolated protocol and HTTP fixtures, but its
passing tests cannot prove SQL transactions, row locks, leases, or recovery.
The existing fast repository suite took 3.342 seconds at baseline `b30a4651`.
Replacing every fixture with a database before separating the domains would
couple unrelated refactors and make each format's migration harder to assess.

Two alternatives were considered:

1. Delete memory now and require PostgreSQL for every protocol/HTTP test. This
   removes duplicate implementations but imposes database setup and isolation
   on pure protocol tests and all existing fixture commands at once.
2. Retain memory as an explicitly injected test fixture and require the same
   public store contract to run against PostgreSQL. This keeps fast tests while
   making semantic differences visible; PostgreSQL integration remains required
   for transaction, locking, lease, multi-instance and restart evidence.

We choose option 2, beginning with Raw. This is a test strategy decision, not a
compatibility promise: APIs and store interfaces may still change under ADR
0009. Memory is not a production persistence mode, and the explicit composition
work in #279 removes hidden fallback construction.

## Contract and Raw tracer

`raw_store_contract_test.go` defines one suite through `NativeRawStore`, with no
map access, SQL queries or per-backend expected results. Normal tests run it
against a fresh MemoryStore; the integration build runs the same function
against PostgreSQL, using a fresh repository and immutable identities per case.
`compose.integration.yml` already runs the tagged repository package in CI.

The tracer covers bounded sorted path pages, immutable object metadata,
conditional-write concurrency, upload completion/condition atomicity,
cancel/expiry, tombstone/restore/reclaim and cancellation of contended upload
and object locks. It does not claim every SQL failure mode is modelled in
memory, nor cover all native-store operations yet. Default read/reclaim limits
are 100; collected bytes cannot be restored (`ErrNotFound`). A rejected
conditional completion leaves the upload open and the previous path visible.

The suite exposed four classes of memory-only divergence before fixing them:
unbounded default asset pages, an empty default reclaim batch, a different
restore error after bytes were collected, and lock waiters ignoring cancellation.
The new suite failed on each class, then passed on memory and real PostgreSQL.
Keyed memory locks now wait through the request context; they do not emulate
PostgreSQL session, transaction or pool semantics.

Run:

```sh
go test ./internal/repository -run TestRawMemoryStoreContract -count=1
go test -tags=integration ./internal/repository -run 'TestRaw(Memory|Postgres)StoreContract' -count=1
make integration-test
```

The second command requires `TEST_DATABASE_URL` pointing to an isolated migrated
test database. Do not substitute a production database. With an isolated
PostgreSQL 16 fixture, three consecutive race-enabled runs took 0.05 seconds on memory
and 0.44–0.46 seconds on PostgreSQL (warm build cache; database creation/migration
excluded). These are local observations, not latency or CI guarantees. The
normal repository-suite baseline above includes existing tests and is not a
like-for-like comparison with the tracer timings. A separate paired run of
`go test -count=1 -json ./internal/repository` on the exact baseline archive and
the changed checkout, on Go 1.26.9 / darwin / arm64, measured 3.342 seconds before
and 3.382 seconds after: +0.040 seconds (+1.2%) for the normal suite. Both used
the same warmed build cache and host; these single-run test durations measure
the added fixture cost, not a statistically significant performance change.

## Follow-through

Require shared public-boundary contracts when changing a covered store. If a
new capability cannot be modelled honestly in memory, narrow the fixture or
use a PostgreSQL-only test instead of making memory's success authoritative.
Move remaining contracts domain by domain alongside #280–#282:

- [x] Raw initial publication/upload/lifecycle/lock tracer.
- [ ] Raw remaining methods and negative persistence/constraint cases.
- [ ] Maven publication, historical identity and snapshot takeover.
- [ ] OCI upload/blob/manifest/index lifecycle and concurrent locking.
- [ ] npm and PyPI publication, mutable metadata and lifecycle.
- [ ] Go and Cargo identity reservation, publication and lifecycle.
- [ ] Conan recipe/package closure and publication.
- [ ] APT publication/snapshot/signing references.
- [ ] Shared identity, access, governance, jobs and operations stores.

Memory and PostgreSQL must run the same expectations for every migrated
contract. Retire memory per domain when its maintenance cost outweighs the
fixture benefit; record that decision and update callers in the same slice.
No new production-only behavior belongs in memory merely to avoid a database
test. Existing append-only schema, immutable bytes and lease/fencing rules stay
in force.
