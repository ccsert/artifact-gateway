# RubyGems Hosted 持久复制预览

[English](rubygems-replication-preview.md) · [文档索引](README.zh-CN.md)

此预览在同一安装内将原始 `.gem` 包从一个 Hosted 仓库复制到另一个 Hosted
仓库，目标使用独立对象。它沿用默认关闭的 `GATEWAY_RUBYGEMS_PREVIEW_ENABLED`。
启用前执行正常的追加式迁移，包括 `000190_native_rubygems_replication.sql`。
API 节点创建、读取任务；只有 worker 节点在 `GATEWAY_WORKER_FORMATS` 中选择
`rubygems`，并在 `GATEWAY_WORKER_KINDS` 中选择 `replication` 才执行复制。本切片不加入正式格式目录、Console、agctl 或安装预设。

## 固定服务端精确身份

源、目标必须是两个不同且处于 active 状态的 RubyGems Hosted 仓库。使用源包读取权限获取：

```text
GET /api/v2/previews/rubygems/repositories/{sourceId}/packages/{name}/{version}/{platform}
```

响应包含 `name`、包内原始 `version`、规范 `platform`、完整 SHA-256 `digest`、
`size` 和 `sourceGovernanceVersion`。原样提交这些服务端值。名称区分大小写；版本按
RubyGems 规范等价性防止重复，同时保留原包拼写。`ruby` 与 `java` 是独立变体。
同一规范名称、版本、平台的重复项会被拒绝。yank 或隔离的源不能用于复制，与隔离读取策略无关。

```text
POST /api/v2/previews/rubygems/replication-jobs
Authorization: Bearer <当前凭据>
Idempotency-Key: <稳定请求标识>
Content-Type: application/json

{
  "sourceRepositoryId": "<源仓库 UUID>",
  "targetRepositoryId": "<目标仓库 UUID>",
  "items": [{
    "name": "owned_top",
    "version": "1.2.0",
    "platform": "ruby",
    "digest": "sha256:<64 位小写十六进制>",
    "size": 4096,
    "sourceGovernanceVersion": "1"
  }]
}
```

计划包含 1–100 个精确变体，每项最多 64 MiB。必须显式列出所需依赖变体，worker
不会遍历外部依赖源。202 回执包含稳定任务 ID、规范计划摘要、版本、状态和逐项检查点。
相同提交者、请求标识及原始凭据身份的完全重放返回同一任务；改变计划或凭据身份返回冲突。
回执不暴露对象键、凭据或会话 ID、密钥和租约令牌。

`GET /api/v2/previews/rubygems/replication-jobs/{jobId}` 要求对两个仓库中每个包
具备当前读取权限。创建和执行要求每项源读取、目标写入权限。最终发布事务持锁后重新读取
原用户会话、会话版本和到期时间，或原 API key、服务账号凭据。过期或被撤销的原始凭据
不能通过重放任务刷新；更换凭据身份后应使用新请求标识创建新任务。

## 原子发布与恢复

worker 完整校验源字节及包内元数据，持有与原生 push 相同的目标发布锁，预留容量，写入
独立目标对象并完整读回。目标可见化、发布审计和成功检查点在同一事务提交，最终核对租约
所有者、令牌、代数和数据库时间。等待锁或审计期间到期的旧所有者不能发布。重启的 worker
只接管到期租约；成功项保留，批次失败不会回滚成功项。

源治理版本任何变化都会使原固定身份失效，包括先 yank 再 restore、先隔离再 release。
已有目标只有完整原始字节、元数据均一致且未被 yank、隔离时才算幂等成功。复制不会恢复、
释放目标，不会覆盖冲突或损坏字节。隐藏身份继续占用容量并保留身份保护。

失败任务可由原提交者使用当前权限、当前任务版本选择失败项重试：

```text
POST /api/v2/previews/rubygems/replication-jobs/{jobId}/retry
Idempotency-Key: <稳定重试标识>
Content-Type: application/json

{"expectedVersion":"<当前任务版本>","itemIndexes":[1]}
```

仅重新排队显式选择的失败项，成功项与源固定身份不变。相同请求的重放不会增加执行次数。
安全错误码区分 `access_denied`、`source_changed`、`target_conflict`、
`identity_conflict`、`quota_exceeded` 和 `transfer_failed`，回执不包含内部存储错误。

## 验证范围

`make native-rubygems-e2e` 执行共用 Memory/PostgreSQL 存储契约、独占
PostgreSQL/RustFS 事务测试，以及固定的官方 Ruby 3.4.4、RubyGems 3.6.7、Bundler 2.6.7
客户端。真实 Gateway 先在仅 API 节点持久化任务，重启后由替换进程的 worker 执行，再用
空缓存、仅目标仓源完成传递依赖的 `gem install`、compact-index 和
`bundle install --full-index`。完整下载的摘要与依赖元数据必须与原包一致，复制后在独占 fixture 中显式模拟移除源对象，并真正重启 PostgreSQL、RustFS 和 Gateway 后
再次冷安装，仍仅使用目标源。官方 MRI 上的 `gem --platform` 显式读取并区分同名同版本
`ruby`/`java` 原包，只执行 `ruby` 变体，不声明 JRuby 运行时验证；同时记录资源所有权与清理回执。

本切片不覆盖 Proxy/Group、promotion、跨安装复制、正式准入、扫描/GC、备份准入、Nexus
迁移或生产部署。[父项 #293](https://github.com/ccsert/artifact-gateway/issues/293) 保持开放，
[子项 #472](https://github.com/ccsert/artifact-gateway/issues/472) 仅交付此预览。本地
fixture、CI、业务迁移和生产验收分开记录。原生客户端配置见
[Hosted 预览指南](rubygems-hosted-preview.zh-CN.md)。
