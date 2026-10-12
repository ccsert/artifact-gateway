# Artifact contains search: isolated PostgreSQL 16 evidence

[简体中文](artifact-contains-search-2026-10-12.zh-CN.md) · [Search contract](../postgresql-capabilities.md)

Measured on 2026-10-12 against an isolated PostgreSQL 16 Docker database on a Mac, from the #338 implementation based on main `fb249812a4e809a1571580abf1b189f6caddc2c2`. Synthetic metadata only; no deployed inventory, bytes, production migration or load test. These are one-run fixture observations, not production capacity claims or percentile latency.

The budget was fixed before measurement: 20,001 Raw paths in one repository and 2,000 Maven SNAPSHOT builds of one coordinate; five seconds per complete SQL query. The independent HTTP fixture has three readable repositories with 20,001 paths each, pageSize 1/50/200 and a maximum of six projection probes per request (including lookahead). Authorization/enumeration SQL is additional; the probe counter is not a total SQL-call counter. The full query budget includes those reads. Concurrent test packages share the synthetic database, so base-table scans may also read other fixtures.

`TestPostgresContainsSearchPlansAndCancellation` calls the public store method, obtains each exact prefix/contains SQL variant from `pg_prepared_statements`, and explains that statement with its actual eight parameter types/values. No forced GIN, planner switches or simplified SELECT. The full JSON plans are reproducible by setting `GATEWAY_SEARCH_PLAN_OUTPUT=/tmp/search-plans.json` while running `go test -tags=integration ./internal/repository -run TestPostgresContainsSearchPlansAndCancellation -count=1 -v` with `TEST_DATABASE_URL` pointing to a migrated disposable database ending in `_test`.


| Format | Match | Query | SQL execution ms | Root temp write blocks | Max loops |
| --- | --- | --- | ---: | ---: | ---: |
| raw | prefix | `needle` | 0.126 | 0 | 1 |
| raw | prefix | `artifact` | 0.086 | 0 | 1 |
| raw | prefix | `absent-keyword` | 0.103 | 0 | 1 |
| raw | prefix | `x` | 0.105 | 0 | 1 |
| raw | prefix | `ph` | 0.104 | 0 | 1 |
| raw | prefix | `/` | 0.115 | 0 | 1 |
| raw | prefix | `a:b` | 0.105 | 0 | 1 |
| raw | prefix | `1.0` | 0.097 | 0 | 1 |
| raw | prefix | `alpha` | 0.080 | 0 | 1 |
| raw | contains | `needle` | 1.070 | 0 | 1 |
| raw | contains | `artifact` | 23.760 | 421 | 52 |
| raw | contains | `absent-keyword` | 8.527 | 0 | 1 |
| raw | contains | `x` | 7.005 | 0 | 1 |
| raw | contains | `ph` | 8.573 | 0 | 1 |
| raw | contains | `/` | 21.765 | 421 | 52 |
| raw | contains | `a:b` | 22.008 | 421 | 52 |
| raw | contains | `1.0` | 22.351 | 421 | 52 |
| raw | contains | `alpha` | 0.906 | 0 | 1 |
| maven | prefix | `needle` | 0.085 | 0 | 1 |
| maven | prefix | `artifact` | 0.071 | 0 | 1 |
| maven | prefix | `absent-keyword` | 0.072 | 0 | 1 |
| maven | prefix | `x` | 0.078 | 0 | 1 |
| maven | prefix | `ph` | 0.084 | 0 | 1 |
| maven | prefix | `/` | 0.067 | 0 | 1 |
| maven | prefix | `a:b` | 0.070 | 0 | 1 |
| maven | prefix | `1.0` | 0.086 | 0 | 1 |
| maven | prefix | `alpha` | 0.072 | 0 | 1 |
| maven | contains | `needle` | 0.849 | 0 | 1 |
| maven | contains | `artifact` | 0.865 | 0 | 1 |
| maven | contains | `absent-keyword` | 0.850 | 0 | 1 |
| maven | contains | `x` | 186.867 | 0 | 2000 |
| maven | contains | `ph` | 188.681 | 0 | 2000 |
| maven | contains | `/` | 0.826 | 0 | 1 |
| maven | contains | `a:b` | 0.786 | 0 | 1 |
| maven | contains | `1.0` | 189.326 | 0 | 2000 |
| maven | contains | `alpha` | 189.709 | 0 | 2000 |

Selective Raw fragments use `native_raw_assets_path_trgm_idx`; Prefix uses the repository/path pattern B-tree. Common and non-trigram fragments use Seq Scan: predicate pushdown retains typed repository UUID and prunes unrelated UNION branches, but cannot turn a low-selectivity search into cheap work. Hot Raw fragments spill temporary sort/window blocks. Maven build-history matches perform up to 2,000 lateral publication/asset loops before the final limit. Increasing history, cross-repository scope, concurrent traffic or cold pages can materially raise cost. The shared five-second cancellation budget bounds a request; it is not a throughput guarantee.

A table lock regression confirms that incoming cancellation interrupts the blocked real SQL read (100 ms deadline, one-second cleanup bound). Memory and PostgreSQL HTTP tests independently assert literal `%`/`_`/backslash/Unicode, query/mode cursor binding, old signed cursors, revoked permissions, scoped grants, pending users, anonymous repository/group and private-member exclusion. HTTP lookahead and page limits are checked at 1/50/200; separate SNAPSHOT tests retain every build across repository, anonymous group and global pages.

The GIN migration is append-only. No production migration was applied. Review index-build locks, disk and write cost separately before deployment; no `tsvector`, similarity scoring, external search service or index-on-view architecture was introduced.

The 24 complete HTTP fixture requests measured 23.5–52.2 ms and 2–3 projection probes; every response succeeded within the fixed five-second budget. These are observed single-run minima/maxima, not p95/p99.

Review regressions additionally lock private schema copies of repository metadata and API keys. Authenticated, anonymous and credential reads each terminate near the shared five-second deadline, without locking other integration packages. Credential/account/OIDC validation receives the request context; existing protocol authentication entrypoints retain their default behaviour.
