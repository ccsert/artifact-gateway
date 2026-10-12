# 制品包含搜索：隔离 PostgreSQL 16 实测

[English](artifact-contains-search-2026-10-12.md) · [搜索契约](../postgresql-capabilities.zh-CN.md)

2026-10-12 在 Mac 的独立 PG16 Docker 数据库实测，代码为 #338 实现，基于 main `fb249812a4e809a1571580abf1b189f6caddc2c2`。仅合成元数据；未操作部署库存、制品字节、生产迁移或生产压测。下表是单次 fixture 观察，不是生产容量或百分位延迟承诺。

测量前固定预算：一个仓库 20,001 条 Raw 路径、一个 Maven 坐标 2,000 个 SNAPSHOT build，完整 SQL 每次最多 5 秒。独立 HTTP fixture 为三个可读仓库、各 20,001 条路径，pageSize 1/50/200，每次最多六个 projection 探测（含后续仓库 lookahead）。授权和仓库枚举还有其他 SQL；探测数不是总 SQL 次数，5 秒请求预算同时涵盖这些读取。测试包并发共享合成数据库，base table 扫描可能读取其他 fixture。

`TestPostgresContainsSearchPlansAndCancellation` 调用公开 store 方法，从 `pg_prepared_statements` 分别取实际 prefix/contains SQL，再用真实八个参数类型和值执行完整 EXPLAIN，不强制 GIN、不切换 planner、不替换成简化 SELECT。设置 `GATEWAY_SEARCH_PLAN_OUTPUT=/tmp/search-plans.json`，运行 `go test -tags=integration ./internal/repository -run TestPostgresContainsSearchPlansAndCancellation -count=1 -v` 可重现全部 JSON 计划；`TEST_DATABASE_URL` 必须指向已迁移、可丢弃、名称以 `_test` 结尾的测试库。


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

高选择性 Raw 片段命中 `native_raw_assets_path_trgm_idx`，前缀使用 repository/path pattern B-tree。热门片段和缺少 trigram 的词走 Seq Scan；类型匹配的 repository UUID 下推并裁剪无关 UNION 分支，但不能让低选择性检索自动变便宜。热门 Raw 查询的排序/window 出现临时块写入。Maven 历史匹配在最终 LIMIT 前产生最多 2,000 次 publication/asset LATERAL 循环。更多历史、仓库、并发和冷页会增加成本；5 秒共用取消预算只限制单次请求，不保证吞吐。

真实表锁测试证明 SQL 读取可被客户端取消中止（100ms deadline、1 秒结束界限）。Memory/PG HTTP 测试独立断言字面 `%`/`_`/反斜线/Unicode、query/mode 游标绑定、旧签名游标、撤权、资源前缀 grant、待审批账号、匿名 Repo/Group 和私有成员排除。HTTP 页限制与后续仓库探测覆盖 1/50/200；独立 SNAPSHOT 分页覆盖仓库、匿名 Group、全局搜索全部 build。

GIN 迁移仅追加，未在生产执行。部署前单独评估建索引锁、空间和写成本；未引入 `tsvector`、相似度评分、外部搜索服务或 view 上持久索引架构。

24 个完整 HTTP fixture 请求实测 23.5–52.2ms、2–3 次 projection 探测，全部在固定 5 秒预算内成功。这是单次观察的最小/最大值，不是 p95/p99。
