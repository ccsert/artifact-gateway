# PostgreSQL capabilities and operational boundaries

[简体中文](postgresql-capabilities.zh-CN.md) · [Architecture diagrams](architecture-diagrams.md) · [Documentation index](README.md)

The runtime baseline is PostgreSQL 16. Artifact Gateway uses PostgreSQL as its
only database and control-plane coordination service. Repositories,
authorization, artifact metadata, audit records, idempotency keys, background
jobs, leases, locks, and runtime-node state all treat PostgreSQL as the source
of truth.

“Lightweight” does not mean PostgreSQL is the only storage system. Verified
immutable artifact bytes remain in S3-compatible object storage; the local
stack bundles RustFS. Redis, Kafka, Elasticsearch, and a separate message queue
are not required.

## PostgreSQL-native or PostgreSQL-bound capabilities

Some entries below are PostgreSQL-specific; others exist in additional
databases, but Artifact Gateway relies on their PostgreSQL syntax and failure
semantics.

| Capability | Use in Artifact Gateway | Why it matters |
| --- | --- | --- |
| Session and transaction advisory locks | OCI/Raw uploads, object deduplication, cache fills, first Maven/npm publication, user administration, lifecycle serialization | Logical resources do not need a dedicated lock table; locks release with the connection or transaction |
| `hashtextextended` lock keys | Maps repository names, object keys, upload IDs, and package coordinates into one stable 64-bit lock space | Every Gateway instance derives the same lock for the same logical resource |
| `FOR UPDATE SKIP LOCKED` | Claims lifecycle, replication, cache, webhook, audit, upload-expiry, session-cleanup, and scheduled work | Workers claim different rows concurrently without a separate queue or lock convoy |
| `LISTEN/NOTIFY` | Wakes lifecycle, replication, cache, audit-cleanup, and repository-deletion workers | Reduces polling latency while durable tables and leases remain authoritative |
| PL/pgSQL triggers | Emits `pg_notify` when work becomes runnable and enforces selected capacity boundaries | State changes and wake hints share the database transaction boundary |
| `ON CONFLICT`, unique constraints, and `RETURNING` | Idempotent publication, task deduplication, object references, node heartbeats, singleton settings, and tag movement | Existence checks, writes, and state readback remain one concurrency-safe statement |
| JSONB and JSONB operators | npm manifests/tags, OIDC roles, scanner intelligence, webhook payloads, dynamic policy, runtime roles | Keeps relational identity while representing format-specific data with JSON type checks |
| `pg_trgm` and partial GIN indexes | Literal contains search of coordinates, package names and paths | Selective fragments can use GIN; short/common fragments can still scan many rows |
| `text_pattern_ops` B-tree indexes | Literal prefix paging across Maven, OCI, Raw, Conan, npm, PyPI, Go, and APT | Preserves indexed `LIKE 'prefix%'` behavior outside the `C` locale |
| BRIN indexes | Audit, lifecycle, replication-checkpoint, OCI, and Conan timelines | Keeps append-heavy time indexes compact |
| `DISTINCT ON`, `LATERAL`, and views | Builds the format-neutral `artifact_search_projection` and selects latest visible records | Performs format projection and version folding in PostgreSQL while authorization remains in the application |
| `pg_stat_statements` | Observes query calls, total/mean execution time, and returned rows | Tunes from real workload evidence rather than static guesses |
| `gen_random_uuid`, `TIMESTAMPTZ`, intervals | Lease tokens, schedule run IDs, expiry, and retry timing | Creates fencing identity and database-consistent time inside claim statements |

## Coordination model

```mermaid
sequenceDiagram
    participant API as API / Scheduler
    participant PG as PostgreSQL
    participant W as Worker
    participant S3 as S3-compatible storage

    API->>PG: INSERT job ON CONFLICT DO NOTHING
    PG-->>W: NOTIFY wake hint
    loop notification or polling fallback
        W->>PG: SELECT ... FOR UPDATE SKIP LOCKED
        PG-->>W: lease token and fenced job
    end
    W->>S3: verify, copy, or reclaim bytes
    W->>PG: commit checkpoint and terminal state
```

- Losing a notification does not lose work; workers continue polling durable
  task tables.
- `SKIP LOCKED` gives non-blocking claims. Lease tokens and expiry provide
  cross-process recovery and stale-worker fencing.
- Session advisory locks are tied to dedicated connections; transaction locks
  release on commit or rollback.
- PostgreSQL transactions decide metadata visibility. Bytes may be staged in
  object storage first; object intents and reclaim work recover unreferenced
  bytes after a transaction failure.

## Search and index composition

1. Literal prefixes use `text_pattern_ops` B-tree indexes and ordered cursors.
2. `match=contains` uses escaped `ILIKE '%keyword%'` and base-table `pg_trgm` GIN indexes. `%`, `_` and backslash are literal input, not wildcards.
3. Partial indexes exclude states that are not protocol-visible.
4. Append-heavy timelines use BRIN instead of ever-growing B-tree indexes.
5. `artifact_search_projection` uses `UNION ALL`, `DISTINCT ON`, and `LATERAL`
   to normalize format metadata; application code still authorizes results.
6. SHA-256 lookup uses repository-and-digest composite indexes, not fuzzy search.

The main global and repository artifact-search endpoints accept optional
`match=prefix|contains`. Omission preserves the old per-format behaviour (Cargo
name substring; other formats literal prefix). Contains is case insensitive;
explicit Prefix is case sensitive. Existing signed tokens without a mode remain
valid with omitted `match`; a token cannot be reused with a changed query, mode,
format or repository. Complete global SHA-256 queries retain exact digest
semantics and the existing supported-format boundary.

Console global search, ordinary repository lists and selected public-repository
search default to Contains. Public results load the next cursor on demand.
Maven Proxy's management list still searches known cache metadata; it does not
discover the upstream inventory. Conan's management list still has its separate
current-page filter limitation; global and direct API Conan search use the new
mode. Directory navigation, identity selectors and resource-prefix authorization
keep their existing contracts.

Search fields are Maven GAV, OCI name, Conan reference, npm/PyPI/Cargo package
name, Go module path and Raw/APT canonical path. For example `alpha` finds
`org.example:alpha:1.0`, `team/alpha` and `releases/alpha.bin`. Maven asset file
names, README text, publisher and binary contents are not indexed. Raw searches
canonicalize readable input without changing the identity used by an exact deep
link or artifact operation. Contains is neither spelling correction nor natural
language full-text search: `tsvector` matches lexemes, not arbitrary substrings.

One/two-character, punctuation-only and common fragments are accepted without
silently falling back to Prefix. A Contains request has a **five-second shared
budget**, including metadata/authorization reads and later repository probes;
incoming cancellation propagates to SQL. Timeout returns a failed request;
add a longer keyword or choose Prefix. Page sizes stay 1–200 (default 50).
Coordinate/build/digest keyset order and visible-state/version folding remain.
Each page rechecks authorization; pagination is not a persistent inventory
snapshot and has no total count. Inserts before a cursor are absent from later
pages; deletions may reduce a page; revocation prevents further reads.

The ordinary projection view cannot own a persistent GIN index. Migration 145
adds indexes to the remaining native search tables and reuses migration 64's
Maven/OCI/Conan/Raw indexes. Index build time, disk and write amplification need
capacity planning; the migration runner does not use `CREATE INDEX CONCURRENTLY`.
Do not infer production latency or index usage from index existence. The real
PostgreSQL regression `TestPostgresContainsSearchPlansAndCancellation` captures
pgx's actual complete prepared SQL and runs `EXPLAIN (ANALYZE, BUFFERS, VERBOSE)`
against fixed synthetic Raw inventory and Maven build history. Set
`GATEWAY_SEARCH_PLAN_OUTPUT` to save every JSON plan; it never forces an index.
The companion HTTP probe-budget test includes global lookahead across three
readable repositories. [Measured fixture report](reports/artifact-contains-search-2026-10-12.md).

## Operational queries

```sql
SELECT calls, total_exec_time, mean_exec_time, rows, query
FROM pg_stat_statements
WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
ORDER BY total_exec_time DESC
LIMIT 20;
```

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT coordinate
FROM artifact_search_projection
WHERE repository_id = '<repository-id>'
  AND format = 'maven'
  AND coordinate LIKE 'org.example:%'
ORDER BY coordinate, build_number
LIMIT 50;
```

```sql
SELECT pid, wait_event_type, wait_event, state, query
FROM pg_stat_activity
WHERE datname = current_database()
  AND wait_event_type IS NOT NULL
ORDER BY backend_start;
```

## Explicitly not used

- **RLS:** tables do not yet share a tenant ID or consistent session tenant
  context across workers and administrator flows.
- **Table partitioning:** current audit/job volume has not justified monthly
  range partitions.
- **Logical replication as a task bus:** durable tables, leases, and polling
  recover work without WAL/CDC consumers.
- **Large objects or `bytea` artifacts:** object storage owns content-addressed
  bytes; PostgreSQL stores digests, references, object intents, and lifecycle
  state.

## Code evidence

| Area | Primary implementation |
| --- | --- |
| Advisory locks | `internal/cache/coordinator.go`, `internal/repository/postgres_advisory_lock.go`, `postgres_{oci,raw,maven,npm}.go` |
| `SKIP LOCKED` and leases | `internal/repository/postgres_lifecycle.go`, `postgres_replication.go`, `postgres_webhooks.go`, `postgres_scheduled_tasks.go` |
| `LISTEN/NOTIFY` | `internal/database/notification.go`, `migrations/000066_postgres_notification_channels.sql` |
| Extensions and indexes | `migrations/000064_postgres_observability_indexes.sql`, `000069_prefix_pattern_indexes.sql` |
| Cross-format search | `migrations/000065_artifact_search_projection.sql` and later format migrations |
| Capacity serialization | `migrations/000046_repository_capacity_enforcement.sql` and format-specific capacity functions |

`scripts/run-migrations.sh` applies migrations in checksum order. Changes to
startup settings such as `shared_preload_libraries` require a PostgreSQL restart
or container rebuild, never deletion of the `gateway-postgres` data volume.
