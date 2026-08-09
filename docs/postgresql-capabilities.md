# PostgreSQL 能力与运维边界

当前运行基线为 PostgreSQL 16。数据库负责制品元数据、引用关系、授权相关状态和后台任务协调；制品字节仍由 MinIO/S3 保存。

## 已启用能力

- ACID 事务、`FOR UPDATE`、`FOR UPDATE SKIP LOCKED` 和 CTE 原子领取任务。
- 会话级与事务级 advisory lock，用于对象去重、上传并发和仓库生命周期协调。
- `ON CONFLICT` 幂等写入、`RETURNING` 状态回读，以及 JSONB/数组字段。
- `LISTEN/NOTIFY` 唤醒缓存、生命周期、复制、审计清理和仓库删除 worker；任务表仍是事实来源，定时轮询保留为兜底。
- `pg_stat_statements` 用于慢查询和调用量观测。
- `pg_trgm` GIN 索引支持坐标、镜像名、Conan reference 和 Raw 路径的模糊搜索。
- `text_pattern_ops` B-tree 索引支持四种格式的字面前缀分页；应用层会转义 `%`、`_` 和反斜杠，避免 LIKE 改变搜索语义。
- BRIN 索引覆盖审计、生命周期、复制检查点和制品创建时间线，控制追加型表的索引体积。
- `artifact_search_projection` 统一暴露 Maven、OCI、Conan、Raw 的可搜索元数据，应用层仍负责仓库可见性和授权；坐标前缀查询按制品折叠版本，SHA-256 查询保留历史可见版本。
- 四种格式均有 repository + digest 复合索引；全局管理搜索会自动识别 `sha256:<64 hex>` 或裸 64 位十六进制并执行精确检索，分页游标同时绑定匹配模式和制品位置。

## 观测查询

```sql
SELECT calls, total_exec_time, mean_exec_time, rows, query
FROM pg_stat_statements
WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
ORDER BY total_exec_time DESC
LIMIT 20;

EXPLAIN (ANALYZE, BUFFERS)
SELECT coordinate
FROM artifact_search_projection
WHERE repository_id = '<repository-id>'
  AND format = 'maven'
  AND coordinate LIKE 'org.example:%'
ORDER BY coordinate, build_number
LIMIT 50;
```

## 尚未直接启用的能力

- **RLS**：当前表没有统一 `tenant_id`，后台 worker、连接池和管理员流程也没有会话租户上下文。贸然启用会造成跨流程误拒绝。确定租户模型后，应在连接入口设置 `SET LOCAL app.tenant_id`，再逐表启用 `FORCE ROW LEVEL SECURITY` 并补充 worker 的显式系统角色。
- **审计分区**：现有审计量尚未达到需要分区的规模。达到百万级且保留策略按时间删除时，再按月对 `resolver_audit_log.occurred_at` 做 RANGE 分区，并为清理 worker 增加分区级 detach/drop 流程。
- **大字段存储**：不把制品内容放进 `bytea`；共享内容寻址对象继续由对象存储管理，数据库仅保存 digest、引用和 intent。

迁移由 `scripts/run-migrations.sh` 按文件校验和顺序执行。修改 PostgreSQL 启动参数后需要重建 PostgreSQL 容器，但不要删除 `gateway-postgres` 数据卷。
