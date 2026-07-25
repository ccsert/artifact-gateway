---
title: Repository/runtime modularization
label: wayfinder:map
tracker: local-markdown
---

## Notes

长期目标：将 Repository 与协议运行时重构为按领域组织的深模块，在不改变导出
`repository.Store` interface、数据库 schema、OpenAPI、V1/V2 路由、认证授权和协议响应
的前提下，拆分 Memory/Postgres adapter、运行时装配与协议共享授权逻辑，并用全量 CI、
集成测试和协议 E2E 证明行为等价。

每个会话先读取本地图、当前 frontier ticket，以及 `docs/repository-grant-authorization-plan.md`
中与授权语义相关的约束。遵循 `codebase-design` 术语：优先形成深 Module，让 caller
看到的小 Interface 不扩张；只在真实变化点放 Seam；MemoryStore/PostgresStore 是
`repository.Store` seam 下的 Adapter，不新增浅 pass-through 层。

硬约束：

- 不手工拆分或编辑 `internal/admin/openapi/generated.go`。
- 不改数据库 migration、表结构、OpenAPI 契约、认证/授权语义、V1/V2 路由和协议错误形态。
- 不提交用户本地 `.vscode/`。
- 每个 ticket 必须包含格式化和受影响测试；跨协议或 adapter 改动再补集成/E2E。

## Decisions so far

- [拆分 Repository 包核心模型与 Memory adapter](tickets/01-repository-package-core-split.md) —
  Repository 包核心已按 `model.go`、`store.go` 和 Memory adapter 领域文件拆分；
  外部 `Store` interfaces、导出模型和 adapter 名称保持不变，受影响测试通过。
- [按领域拆分 PostgresStore adapter implementation](tickets/02-postgres-adapter-domain-split.md) —
  `PostgresStore` 保持单一 adapter/core，SQL implementation 已按 Hosted/grants/retention、
  OCI、Raw、Maven、Groups 和 Audit 文件拆分；集成测试和协议 E2E 通过。
- [收窄 repository_api runtime composition root](tickets/03-repository-api-composition-root.md) —
  `repository_api.go` 已收窄为 Gateway runtime composition root；auth、metrics、resolver、
  cache/operations/audit API 和 legacy V1 group/cache handlers 已移入私有 Module。
- [提炼协议运行时共享授权与缓存复核 Module](tickets/04-protocol-shared-authorization-cache.md) —
  已新增私有 group member runtime Module，复用 Maven/OCI/Conan 的 managed member 授权筛选和
  Maven/OCI cache source 复核；Raw 保留其不同的 legacy 终端拒绝语义，完整门禁和协议 E2E 通过。
- [按行为域拆分测试并完成全量验证](tickets/05-test-boundary-split-and-full-verification.md) —
integration、Conan 和 Raw 测试已按行为域分文件；拆分同时修复了 Maven tombstone 测试的
顺序依赖，CI、集成测试与所有协议 E2E 均通过。
- [提炼 Raw 协议与 Hosted 生命周期 package](tickets/06-raw-protocol-hosted-package-slice.md) —
Raw cache、HTTP handler 与共享运行时依赖已迁至 protocol/cache/objectstore/authorization
module；app 仅负责 transport、runtime adapter 与路由装配，完整门禁通过。

## Fog

- OCI、Maven、Conan 和通用 cache/maintenance 的后续迁移顺序需要按 Raw 切片验证出的
  runtime interface 与依赖方向重新确定。
