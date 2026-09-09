# APT Hosted 快照生命周期

[English](apt-hosted-lifecycle.md) | [文档索引](README.zh-CN.md)

APT Hosted 操作员预览提供专用的包删除、恢复、保留预览与快照清理 API。每次删除、恢复或保留操作都重新生成完整 Packages、Release 和两份签名；只有对象持久化、签名验证及数据库事务全部成功，才替换当前快照。当前公开格式能力仍为 Proxy/Group，生产 KMS/HSM 与晋级/复制分发仍是独立门禁。

## 查看与预览

以下路径均以 `/api/v2/repositories/{repositoryId}` 为前缀，要求 Repository 管理员权限：

| 方法与路径 | 用途 |
| --- | --- |
| `GET /apt/lifecycle?suite=stable` | 已发布快照及 `prunableAfter`、当前包的 publication session ID、删除记录及恢复期限 |
| `POST /apt/lifecycle/preview` | 只读计算删除、恢复或保留的成员变化；signer 未配置时仍可使用 |
| `POST /apt/lifecycle` | 带 `Idempotency-Key` 执行并生成新签名快照 |
| `POST /apt/snapshots/prune` | 清理指定旧快照引用，并清扫到期且无引用的删除记录 |

删除示例：

```json
{
  "suite": "stable",
  "expectedSnapshotId": "当前可见快照 UUID",
  "action": "delete",
  "publicationSessionIds": ["从 lifecycle 查询取得的当前包 session UUID"]
}
```

恢复使用 `action: restore` 和 `deletionIds`，替代 `publicationSessionIds`。它把选定包加入当前完整成员集并重新签名，不重新激活旧快照。普通发布和新归档导入均不能绕过未恢复的删除记录。

保留预览使用 `action: retention`、`keepLatest: 1`、`olderThanDays: 30`。按 component、包名、architecture 分组，仅选择“超过保留数量且上传时间早于阈值”的版本。这里的最新指原始上传时间，不使用字符串模拟 Debian 版本排序，也不按恢复时间重置包的上传年龄。执行时把预览的 `removeSessionIds` 原样放入请求的 `publicationSessionIds`；若期间候选集或当前快照变化，返回 409，须重新预览。空计划无需执行，提交空计划返回 409。

所有变更绑定 `expectedSnapshotId`。成功结果与幂等键、操作者和请求摘要在同一事务持久化；同键同请求返回原结果，即使该结果随后已 retired/pruned。同键不同请求返回 409。签名或对象写入失败保持旧快照；同键可重试，重试创建新的构建 ID 与递增序号，序号允许有空洞。

## 恢复窗口与回收

- 包删除后有 7 天恢复窗口。窗口结束后，恢复 API 拒绝恢复，即使物理对象尚在。
- 快照从退休时刻起至少保留 24 小时。历史迁移无法推导真实退休时间，因此为升级前的 retired 快照从迁移时刻重新起算完整宽限期。
- 当前 Release/Packages 只读 visible 快照。不可变 pool 与 by-hash 路径在 visible 或 retired 快照仍有引用时继续可读，使已缓存旧 Release 的客户端完成下载。启用隔离读取策略时，隔离检查优先于该可用性宽限。
- 删除最后一个包仍保留原 component/architecture 的空 Packages、gzip 与 by-hash 索引。空 Packages 为零字节内容，客户端仍能正常验签和更新。
- 清理请求为 `{"snapshotIds":["已退休且超过宽限期的 UUID"]}`，最多 100 个。整批验证后原子移除成员和资产引用，保留 pruned 快照身份及签名证据。当前快照不可清理；重复清理同一 pruned ID 安全。
- `{"snapshotIds":[]}` 只清扫到期删除记录。每个包还必须没有 visible/retired/building/signed 快照成员引用，没有其他未到期删除记录保护，也没有最近 24 小时创建的 staging session，才可移除 revision。新归档 preparing 对象意图始终阻止物理回收。
- 对象回收使用现有持久队列、对象锁、全局引用重查与失败重试。清理成功表示数据库引用已处理，不代表后台物理删除已经完成。共享对象和其他 suite 引用继续保留。
- 容量在包 revision 真正移除后才释放；当前生成索引按去重对象计费。恢复窗口内的包仍占用容量。

已回收包的删除屏障和历史证据继续保留，不能用同身份的重新上传绕过。超过恢复窗口后，应从数据库与对象存储的一致备份恢复到独立验证环境，或发布新的包版本。单个快照归档不包含删除记录或队列状态，不能替代完整生命周期备份。

本阶段使用显式管理 API 执行保留和清理，尚未把 APT 接入通用 retention policy 定时执行器、Console 生命周期表单或晋级/复制流程。

## 升级、备份与兼容边界

追加迁移 `000117_native_apt_lifecycle.sql`，不重写历史迁移。升级前保存 PostgreSQL 与 RustFS 的一致备份，停止旧版本写入节点及后台 Worker，再迁移并启动所有新节点；不得混跑不认识删除屏障的旧二进制。

新版本读取既有非空 v1 快照归档，且保留原始字节。空快照仍使用 v1 归档结构，但旧版本验证器会拒绝空包集合；旧二进制也不认识 pruned 状态和删除屏障。因此本迁移明确拒绝向下迁移，回滚须恢复升级前的数据库、对象及匹配二进制，不能只换回旧镜像。

验收入口：`go test ./internal/aptpublication ./internal/app ./internal/repository`、隔离的 `make integration-test` 和 `make native-apt-e2e`。后者实际验证 signer 停止后的失败及重试、空签名仓库、旧 pool 下载、保留及恢复后的 Debian 安装，并继续验证一致备份恢复与无 signer 的可信归档恢复。

## 扫描与隔离

APT Hosted 扫描使用当前 visible 快照中的 `.deb`，身份为 `Repository + apt + pool 路径 + SHA-256`。同一 pool 路径在所有 suite 中共享治理状态；staged 包、只剩 retired 引用的包、Release/Packages 和 suite 别名均不作为扫描身份。隔离包仍可用于复扫调查。扫描器接收经过摘要和大小校验的完整 `.deb` 字节；扫描结果通过已有 Worker 合并到 intelligence，保留发布者签名和来源。`autoScanOnPublish` 会在新快照提交后入队，也支持显式扫描及 reconciliation；扫描排队失败会记录审计，可由 reconciliation 补偿。

隔离使用已有 `PUT /artifact-quarantine?coordinate=<编码后的 pool 路径>&digest=<SHA-256>`，带 `If-Match: 0` 创建，后续状态变化使用当前版本，正文包含 `state` 和 `reason`。读取策略 `PUT /quarantine-read-policy` 默认为关闭；启用示例为 `If-Match: 1` 加 `{"version":"1","enabled":true}`。这些操作要求仓库管理员权限，并保留版本和审计。

- 默认关闭时，隔离不改变已有签名快照的读取行为。新发布、生命周期恢复和新归档导入都会拒绝隔离成员；精确归档重放不会重新激活旧快照。
- 开启策略后，隔离包的 pool GET/HEAD 返回 403。含有该包的完整签名元数据视图也返回 403，包括 Packages、gzip、Release、两份签名及旧 by-hash 索引。条件请求和 Range 也经过同一检查。其他包的 pool 下载仍可用。
- 这是完整签名视图的拒绝策略：不在读请求中动态过滤 Packages，也不因 signer 离线而推迟隔离。若需继续向客户端提供其余包，可通过生命周期删除受隔离成员，生成新的完整签名快照。旧索引仍受隔离检查约束。
- 释放只恢复现有生命周期引用的读取，不撤销删除，不切换当前快照，也不更改原有签名字节。已删除包还需要在恢复窗口内显式恢复。
- 发布在解析包时和调用 signer 前检查隔离；最终事务与隔离变更锁定同一仓库记录，再次检查全部 pool 身份。签名期间发生的隔离会使发布返回 `409 artifact_quarantined`，旧可见性保持不变。

扫描需显式配置能分析 `.deb` 的外部服务及 `GATEWAY_SCANNER_FORMATS=apt`。内置参考 Trivy 文件系统扫描器仍拒绝 APT，不能把未分析的压缩包当成零漏洞结论；详见[扫描器契约](artifact-scanner-contract.zh-CN.md)。APT Group 仍仅接受 Proxy 成员，不允许 Hosted 通过 Group 绕过策略。聚合签名、目标签名晋级/复制和 KMS/HSM 不属于本阶段。

本变更不增加数据库字段；仍须停止旧二进制节点后整体升级，旧节点不会执行新的 APT 隔离检查。验证包含内存/PostgreSQL/RustFS 的签名期间隔离、旧快照读取和删除/释放/恢复，以及真实 Debian 使用已缓存索引安装时的阻断与释放后安装。
