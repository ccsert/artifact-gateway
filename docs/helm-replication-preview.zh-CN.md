# Helm Hosted 持久复制预览

[English](helm-replication-preview.md) · [文档索引](README.zh-CN.md)

Helm Hosted→Hosted 持久复制在同一安装内复制完整 chart 和可选 provenance，生成拥有独立对象的目标发布。它继续使用默认关闭的 `GATEWAY_HELM_PREVIEW_ENABLED` 开关，属于传统 HTTP Helm 预览。启用前应用 migration `000188`。API 节点创建与读取任务；worker 只有在 `GATEWAY_WORKER_FORMATS` 选择 `helm` 且 `GATEWAY_WORKER_KINDS` 选择 `replication` 时执行任务。

## 固定复制计划

源和目标必须是不同且有效的 Helm Hosted 仓库。源仓读取权限可通过 `GET /api/v2/previews/helm/repositories/{id}/charts/{name}/{version}` 获取 chart 的 digest、size、provenanceDigest、provenanceSize 和 governanceVersion。将这些值原样固定到计划中；名称和版本必须符合已有 Helm 严格名称及 SemVer 规则。未附 provenance 时提交空摘要及大小 0，不允许复制时丢弃已有 provenance。

每个计划包含 1–100 个不同 chart 身份，服务端排序后计算不可变摘要。创建需要当前主体对每个 chart 具有源仓读取、目标仓写入权限；chart 名称前缀授权与发布和下载相同。独立 API key 可以没有全局角色，只要具有这两个仓库授权且当前凭据有效。

```http
POST /api/v2/previews/helm/replication-jobs
Authorization: Bearer <current-credential>
Idempotency-Key: release-2026-10-24
Content-Type: application/json

{
  "sourceRepositoryId": "11111111-1111-4111-8111-111111111111",
  "targetRepositoryId": "22222222-2222-4222-8222-222222222222",
  "items": [{
    "name": "service",
    "version": "1.2.3",
    "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "size": 12345,
    "provenanceDigest": "",
    "provenanceSize": 0,
    "sourceGovernanceVersion": "1"
  }]
}
```

创建返回 `202`、持久任务及 `Location`。相同操作者、认证类型和幂等键回放同一计划返回同一任务；改变计划或原服务账户凭据身份返回冲突。任务不接受或保存源对象键、bearer 或签名私钥。支持当前配置的静态主体、有效的持久本地/OIDC 用户、独立 API key 及具备非秘密 credential ID 的服务账户凭据；未持久化的 OIDC 主体和无可撤销凭据身份的派生 token 不支持持久复制。

`GET /api/v2/previews/helm/replication-jobs/{jobId}` 要求对每项具备源和目标两仓读取权限，返回冻结计划、操作者、任务版本/状态、尝试次数、目标 session ID 和安全失败分类，不暴露物理对象键或凭据 ID。

## 完整闭包与恢复

worker 完整读取并校验源 chart 和 provenance 的摘要、大小、元数据及 chart/provenance 绑定，保留原始归档和签名。目标获得独立的仓库范围对象键及 session；写入后重新打开目标实际字节复验，损坏、截断、缺少 provenance 都不能发布。校验 provenance 的结构和 chart 摘要绑定不代表信任签名者，消费者仍使用自己的公钥环执行 `helm pull --verify`。

复制在目标发布锁内预留、上传两份对象、复验及最终提交，与普通上传及未完成上传清理使用相同锁。最终数据库事务锁住当前主体/凭据、两仓授权及仓库，重验源治理版本、目标身份与治理、整个闭包的容量和租约；目标发布、逐项检查点和审计一起提交。撤权、禁用、密码重置或凭据到期会阻止尚未完成的发布。旧 worker 的过期租约不能发布或修改新 owner 的检查点。

源墓碑、任何隔离或治理版本改变均拒绝，即使隔离策略允许普通读取。目标已有相同 chart/provenance 只有完整字节及治理一致时可幂等成功；不同闭包、墓碑或隔离目标不会被复制自动覆盖或恢复。容量包含 chart 与 provenance，也保留已隐藏发布的引用。

任务从 `queued` 到 `running`，最终为 `succeeded` 或 `failed`；逐项为 `pending`、`running`、`succeeded`、`failed`。进程或数据库连接重开后，新 worker 接管过期租约，保留已完成项并完整重验未完成闭包。原子单位是一个 chart：失败任务可保留之前完成的 chart。这里的恢复是整项重试，不提供字节范围续传。

## 精确重试

仅原操作者、认证类型及原服务凭据身份能重试失败任务。使用当前任务版本、非空且不重复的失败项下标和新的重试幂等键：

```http
POST /api/v2/previews/helm/replication-jobs/{jobId}/retry
Authorization: Bearer <original-actor-current-credential>
Idempotency-Key: retry-one
Content-Type: application/json

{"expectedVersion":"7","itemIndexes":[1,2]}
```

成功返回 `202`。同键精确回放不会重新调度；同键改变载荷、旧版本、运行中任务或已完成项返回 `409`。已完成项的 session、时间和尝试次数保持不变。计划不能修改，源治理变化后应提交重新审阅的新计划。重试继续校验当前权限，不能恢复已撤销的授权。

## 验收与边界

专项测试覆盖 Memory/PostgreSQL 租约、并发 claim、当前主体和双仓授权变更、审计失败回滚、目标完整闭包、部分成功与精确重试，以及上传后/提交响应丢失后的重开恢复。固定官方 Helm 3.19.0 的独占客户端场景仅配置目标仓，在空缓存中执行 repo update、pull --verify 和 template；移除独占测试源对象并重开 Gateway/存储连接后再次冷消费目标。

本预览不包含 Proxy/Group 复制、跨实例联邦、晋升、OCI Helm、Nexus 源适配器、Console/CLI 或安装预置。外部依赖仓库不会被自动遍历，归档内的原始依赖内容按原字节保留。正式准入和其余能力由父路线图 #292 继续跟踪；独占 fixture、CI、真实业务迁移及生产部署分别验收。
