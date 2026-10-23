# NuGet Hosted 持久复制预览

[English](nuget-replication-preview.md) · [文档索引](README.zh-CN.md)

NuGet Hosted→Hosted 复制需要显式开启。它在同一 Gateway 安装内复制完整校验的包，
生成独立目标发布。本功能不代表 NuGet 正式准入，不包含 Nexus NuGet 源适配器、晋升
策略、Proxy/Group 复制、保留、物理 GC、跨实例联邦或 Console/CLI 操作。剩余准入工作
继续由 [NuGet 路线图](nuget-roadmap.zh-CN.md) 跟踪。

## 开关与节点角色

`GATEWAY_NUGET_PREVIEW_ENABLED=true` 启用已有预览路由及本 API，默认值为 false，
不增加安装预置。源和目标必须是 ID 不同、状态有效的 NuGet **Hosted** 仓库；公开格式
目录和正式 Console 创建入口保持现有准入边界。

API 节点只创建和查询持久任务。worker 节点仅在 `GATEWAY_WORKER_FORMATS` 选择 `nuget`
且 `GATEWAY_WORKER_KINDS` 选择 `replication` 时执行任务。standalone 包含这两个角色。
仅 API 节点不会执行复制，仅 worker 节点不会暴露管理或原生协议路由。升级节点启动前
需要应用 migration `000184`。

## 冻结并提交有界计划

对每个包，通过源预览 package 端点读取规范包 ID/版本、完整 SHA-256、大小及
`governanceVersion`，固定这五个值。每个计划包含 1–100 个不同身份、一个源仓 ID 和一个
目标仓 ID。服务端按 NuGet 原生规则规范包身份，并排序后计算不可变计划摘要。规范后
重复的坐标会被拒绝，不会静默去重。

```http
POST /api/v2/previews/nuget/replication-jobs
Authorization: Bearer <current-credential>
Idempotency-Key: release-2026-10-23
Content-Type: application/json

{
  "sourceRepositoryId": "11111111-1111-4111-8111-111111111111",
  "targetRepositoryId": "22222222-2222-4222-8222-222222222222",
  "items": [{
    "packageId": "Acme.App",
    "version": "1.0.0",
    "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "size": 12345,
    "sourceGovernanceVersion": "2"
  }]
}
```

创建返回 `202` 及指向持久任务的 `Location`。同键精确回放返回同一任务；复用键却改变
计划或服务凭据身份会冲突。创建和重试需要对每个包具有当前源仓读取、目标仓写入权限，
包前缀授权规则与原生读取/发布一致。任务不接受或保存源 objectKey、bearer。无效仓库、
跨格式或 Proxy/Group 目标、过期源身份、受治理限制的源及目标容量不足会在入队前拒绝。

持久执行支持当前配置的静态管理员和 resolver、已持久化且有效的本地/OIDC 用户、有效
独立 API key，以及带原非秘密 credential ID 的有效服务账户凭据。执行时重新读取当前
角色、账户/密码状态及仓库授权。未持久化的外部 OIDC 主体，以及缺少可撤销凭据身份的
派生服务账户 token，不能提交持久任务。替换静态配置主体不会让旧主体的排队授权继续
生效。

## 逐包提交与恢复

`GET /api/v2/previews/nuget/replication-jobs/{jobId}` 要求当前调用者对清单中每项具有源、
目标两仓读取权限。返回固定计划、操作者、任务版本/状态、逐项尝试次数、目标 session ID、
结果和安全失败分类，不返回物理 objectKey、credential ID 或秘密。

任务由 `queued` 进入 `running`，最终为 `succeeded` 或 `failed`；逐项状态为 `pending`、
`running`、`succeeded`、`failed`。数据库租约和递增 fence 保护进度及最终发布事务。过期
worker 不能提交、把新 owner 的项目置失败或结束其任务。进程/数据库重启后，新 worker
接管过期租约，保留已完成项，对未完成包重新进行全量校验。这里不声称支持字节范围续传。

每个新目标发布拥有独立 session 和仓库范围对象键。worker 完整读取源字节，验证
nupkg/nuspec 身份、SHA-256、大小、依赖元数据及 package hash，写入目标对象后，再次
打开目标实际字节进行完整校验。对象存储即使错误确认了损坏或截断上传，包也不会可见。
最终数据库事务锁住当前主体/凭据、仓库授权集及仓库，校验租约、源治理、目标治理和
容量，把目标发布、逐项检查点及审计一起提交。凭据在最终事务边界过期会回滚发布。

同坐标同内容的现有目标仅在其字节与治理验证通过后幂等成功。不同字节、墓碑、隔离或
不一致的目标上下架状态会冲突；复制不会替现有目标恢复、解除隔离或重新上架。源墓碑、
任何隔离或治理版本变化都会拒绝未完成项，即使隔离读取策略允许普通下载。新目标保留
源当前上下架状态和依赖元数据；未上架版本仍能按精确版本恢复。

原子单位是**一个包**。失败批次可保留之前完成的包，不会回滚或隐藏这些发布。保留任务
引用原始仓库，不作为仓库删除或 GC 流程使用。

## 精确重试失败项

只有原持久操作者、认证类型及原服务凭据身份能重试失败任务。提供任务当前 `version`、
非空且不重复的失败项目下标，以及新的重试幂等键：

```http
POST /api/v2/previews/nuget/replication-jobs/{jobId}/retry
Authorization: Bearer <original-actor-current-credential>
Idempotency-Key: retry-one
Content-Type: application/json

{"expectedVersion":"7","itemIndexes":[1,2]}
```

成功重试返回 `202`。同键、同载荷精确回放不会再次调度；同键改变版本/项目集合、使用
旧版本、对运行中任务或已完成项重试均返回 `409`。计划和源固定身份不能修改。源治理
改变后，应重新审阅并提交新计划，不能重试过期 pin。已撤销凭据或移除授权不能借重试
恢复权限。已完成项保留原目标 session、发布时间和尝试次数。

## 验收证据及限制

公开 HTTP/Memory 测试覆盖规范化、幂等、授权、容量、不可变身份冲突、源/目标治理、
部分完成和精确重试。PostgreSQL 测试覆盖并发 worker、租约 fence、事务审计回滚及真实
授权/主体变更互锁。PostgreSQL/S3 测试覆盖目标上传后、提交回复丢失两个崩溃窗口以及
重开恢复。原生门禁要求固定官方 .NET SDK `10.0.100`，在不同独占空缓存中仅配置目标
仓，停止源/worker、删除测试源对象并重启 Gateway、PostgreSQL、S3 后，恢复并执行
三层传递依赖图。

这些独占本地 fixture 和 CI，与生产部署、真实业务源迁移、发布准入分别记录。本功能
继续保持预览，不增加历史兼容别名或切流流程。
