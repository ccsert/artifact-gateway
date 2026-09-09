# APT 使用目标签名的晋级与复制

[English](apt-hosted-distribution.md) | [APT 路线图](apt-hosted-roadmap.zh-CN.md)

APT Hosted 分发属于操作员预览，接入现有晋级任务和复制计划。源与目标必须是活跃的 APT Hosted 仓库，调用者需要两个仓库的管理权限，API 与相关 Worker 节点都需要配置 signer。公开格式能力与 Console 创建选项仍保持 Proxy/Group；本次验收不代表生产私钥保管或 APT Group 聚合完成。

## 请求

带 `Idempotency-Key` 调用以下任一接口：

- `POST /api/v2/repositories/{sourceId}/promotions`
- `POST /api/v2/repositories/{sourceId}/replications`

```json
{
  "targetRepositoryId": "00000000-0000-4000-8000-000000000002",
  "coordinate": "pool/main/w/widget/widget_1.0-1_amd64.deb",
  "digest": "sha256:0000000000000000000000000000000000000000000000000000000000000000",
  "aptTargetSuite": "candidate"
}
```

将示例 ID、路径和摘要替换为实际可见包的身份。coordinate 是仓库级 pool 路径，保留原 component，不能加 suite 前缀；包必须至少在一个源 suite 中可见，staged 和仅被退休快照引用的包不能发起分发。相同幂等键修改目标 suite 返回冲突；其他格式拒绝 `aptTargetSuite`。

接口返回 `202`。晋级状态在目标仓库生命周期任务中查看，复制状态在任一仓库的复制列表或详情中查看。复制响应会保留 `aptTargetSuite`。受理表示已排队，需确认任务 `completed` 与目标 `/apt/signing-state` 后再使用。晋级沿用入队时的目标准入安全策略检查。

## 发布与恢复

- 晋级验证包字节并建立目标自己的 package metadata；复制先通过现有带心跳和重试的 Worker 完成单个不可变 `.deb` 检查点。两条路径都不复制源 `Release`、`InRelease`、`Release.gpg`、`Packages` 或 by-hash 元数据。
- Worker 保留目标当前 suite 的包与索引范围，加入本次包，并使用目标 repository ID 请求签名。实际密钥由配置的 signer 选择；每个仓库使用不同密钥需要操作员配置 signer 路由和客户端公钥信任。
- 签名前与最终事务中复核源包可见性和隔离状态；最终事务还校验目标隔离、pool 不可变性、删除屏障、配额、目标基准快照和有效 Worker 租约。并发目标发布会产生冲突，不会丢弃其他任务的成员。外部签名请求期间不持有源或目标数据库行锁。
- signer 失败保留目标旧视图。已验证的复制检查点跨重试保留，恢复时会重新计算包摘要。已提交命令的回执存入 APT 生命周期结果表；重放不会重新发布旧快照，也不会撤销后续删除。
- 发布审计记录源仓库、pool 路径、摘要、目标基准、命令 ID 与目标签名证据。源制品情报沿用现有延迟复制机制；配置发布扫描后，目标按普通发布流程排入扫描任务。

Worker 配置使用 `apt` 格式与现有 `promotion`、`replication` 类型。缺少 signer 时不启动这两类 APT Worker。迁移 `000118_apt_target_signed_distribution.sql` 将 APT 纳入复制格式约束并持久化目标 suite；旧版本无法安全执行 APT 分发任务，降级必须恢复升级前备份。

## 验证

Memory 和 PostgreSQL/RustFS 运行同一组契约：保留目标原成员、目标签名身份、删除后重放、signer 故障恢复、签名期间隔离或删除源包、目标并发修改、租约失效、已验证检查点恢复和 suite 幂等冲突。

`make native-apt-e2e` 先从源安装包，再晋级和复制到两个目标仓库，使用显式公钥信任在新的 Debian 客户端中分别对 `candidate` suite 执行 `apt-get update` 和安装，随后继续隔离、签名生命周期和归档恢复验证。这些都是隔离验证环境，不是生产部署。
