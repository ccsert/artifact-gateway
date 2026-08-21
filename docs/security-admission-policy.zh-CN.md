# 安全准入策略

[English](security-admission-policy.md) | [文档索引](README.zh-CN.md)

安全准入策略在不可变 Artifact 晋级到 Hosted Repository 时保护目标。它不改变普通读取、源仓库写入、Proxy 解析或安全情报采集。

Artifact Quarantine 是独立的源侧治理不变量：即使目标准入策略关闭，也会阻止晋级和复制。另一个独立的隔离读取策略可让协议读取对隔离身份失败关闭。

## 策略字段

策略按 Repository 版本化，并使用 `If-Match` 乐观并发。

| 字段 | 含义 |
| --- | --- |
| `enabled` | 对进入此 Repository 的晋级启用准入 |
| `autoScanOnPublish` | 新 Hosted 发布后排入异步 scan |
| `requireSignature` | 至少一个签名 |
| `requireVerifiedSignature` | 至少一个 `verified: true` 签名 |
| `requireSbom` | 至少一个 SBOM 引用 |
| `requireProvenance` | 要求构建 provenance |
| `requireVulnerabilityScan` | 结果不能是 `not_scanned` |
| `maxAllowedSeverity` | 最高允许 `none|low|medium|high|critical`；`unknown` 高于 critical |
| `failOnScanError` | 开启时拒绝 scanner `error` |
| `allowedLicenses` | 可选大小写不敏感 SPDX allowlist；报告的每项 license 都必须在其中 |

默认策略关闭、允许 critical、扫描错误失败关闭且不限制 license。

## 管理 API

通过 `GET/PUT /api/v2/repositories/{repositoryId}/security-policy` 读取或带 `If-Match` 替换。以下接口预演晋级而不创建任务：

```http
POST /api/v2/repositories/{targetRepositoryId}/security-policy:evaluate
Content-Type: application/json

{
  "sourceRepositoryId": "<source repository UUID>",
  "coordinate": "com.example:widget:1.2.3",
  "digest": "sha256:<64 lowercase hex characters>"
}
```

源必须 active、与目标格式一致，并拥有可见 coordinate/digest。调用者需要目标 admin 和源 read。

管理客户端通过 `GET .../artifact-identities?purpose=distribution` 获取可选身份，不能从 browse 投影自行拼接。Conan 只返回 recipe revision，因为 recipe 与 package closure 是原子分发单元。

## 晋级行为

晋级在 enqueue 前立即评估。关闭策略返回 `policy_disabled` 并允许；开启但不满足时返回 `security_policy_denied` 和稳定 reason，审计 `promote.security_policy`，且不创建任务。

reason 包括签名、已验证签名、SBOM、provenance、扫描、扫描错误、license 及各严重度不满足；`policy_disabled` 也是稳定判定值。

## Artifact 隔离

Quarantine 按 Repository、format、coordinate、digest 不可变身份版本化，独立于墓碑和格式状态。通过带 `If-Match` 的 `GET/PUT .../artifact-quarantine` 读取或转换，body 为 `state: quarantined|released` 与操作员 reason。

初次隔离使用 `If-Match: 0`，后续使用当前 `ETag`。过期版本返回 `412 version_conflict`，重复状态返回 `409 invalid_state`。只有现有可见 Artifact 可首次隔离；之后即使生命周期变化仍可 release。

Conan 的隔离坐标必须是 recipe revision（`reference#recipeRevision`），其全部可见 package revision 作为一个分发单元。Package revision 仍可独立扫描、写 intelligence、逻辑删除和保留，但不能单独隔离。

隔离源在创建晋级 job 或 replication plan 前返回 `403 artifact_quarantined`。两类 Worker 在发布目标元数据前、持有共享 identity lock 时再次检查，所以 enqueue 后隔离仍可阻止可见性。

复制 Worker 遇到隔离会 park plan，不发布且不消耗重试。Release 后使用同一 `Idempotency-Key` 重放原请求，以原 plan ID 重新排队。

PyPI 的 `project@version` 可含多文件；隔离任一可见 digest 会阻止整个版本。最终发布时 Worker 锁定 version、重新列出完整成员并检查每个当前 digest。成员与 checkpoint 变化但未隔离时，plan 进入 `replication_snapshot_changed`；同 key 重放会原子刷新完整 checkpoint 并复用 plan ID。

Quarantine 在 idempotency replay 前评估。已接受请求在源被隔离后重放返回 `403`；release 后恢复原 `202` 且不创建第二个 job。历史 plan 若 coordinate 和 digest 均空，新 Worker 对非终态失败关闭。迁移 `000095` 前须按发布就绪文档 drain 并停止旧 Worker，避免绕过。

## 隔离读取强制

每个 Hosted Repository 有独立、版本化且默认关闭的 read policy：

```http
GET /api/v2/repositories/{repositoryId}/quarantine-read-policy
PUT /api/v2/repositories/{repositoryId}/quarantine-read-policy
If-Match: <policy version>
```

开启后，Raw、Maven、npm、PyPI、Conan 的隔离 Artifact 对协议 GET/HEAD 返回 `403 artifact_quarantined`；OCI 返回 Registry V2 `DENIED`。列表与元数据会隐藏隔离身份。

npm/PyPI 按完整 package/project version 执行，Conan 按 recipe revision closure，OCI 阻止 manifest 及其引用的 config/layer/index blob。Group 发现更高优先级隔离身份时不得 fall through。

Release 立即恢复读取；关闭 read policy 恢复旧行为但不删除隔离记录。Go 与 APT 不属于首批读取强制范围。

建议先保持关闭，让扫描器写入 intelligence，在 CI 使用 evaluate 验证覆盖，再逐 Repository 开启。扫描由管理 API 持久排队，发布后 `autoScanOnPublish` 可异步触发；调度失败会审计但不回滚成功发布。

晋级后按不可变身份复制源 intelligence。等价证据视为已同步，不同证据绝不覆盖，而是创建失败的 `intelligence` lifecycle job。临时失败由 follow-up job 重试，不重放 Artifact 发布。

若连证据或 follow-up job 都无法持久化，晋级仍完成并发出有界 `intelligence-copy/deferred` 指标。恢复后可用 `lifecycle-jobs:reconcile-intelligence` 每次最多重排 100 条失败或取消任务。
