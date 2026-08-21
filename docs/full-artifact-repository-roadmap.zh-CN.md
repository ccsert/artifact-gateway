# 完整制品库路线图

[English](full-artifact-repository-roadmap.md) | [文档索引](README.zh-CN.md)

权威交付目标和完成标准见[完整制品库 V1 目标](full-artifact-repository-goal.zh-CN.md)。本文记录架构顺序和实现状态。

## 产品目标

Artifact Gateway 正在成为面向 OCI、Maven、Raw 和 Conan 的完整制品库。一个格式只有同时具备 Hosted、Proxy、Group，安全发布与获取，浏览搜索，逻辑删除与保留，晋级复制，以及 Repository 级授权、配额、审计和恢复证据，才算完成。

这从战略上取代只读网关边界。现有协议和 `/api/v1`、`/api/v2` 契约仍是兼容承诺；新的生命周期管理进入版本化管理 API 和增量 schema 迁移。预检和证据收集仍属于发布基础设施，而不是产品目标本身。

## 完成规则

Artifact 只能通过格式自己的 **Publication** 边界变为可见。字节在发布前按内容寻址并验证。Artifact 不得原地覆盖：可变协议引用可以移动，但必须始终指向不可变 Artifact。

删除创建 **Tombstone**；**Orphan Collector** 只在宽限期结束并重新检查引用后回收字节。晋级和复制创建可审计的目标 Artifact，且绝不修改源 Artifact。

## 交付顺序

1. **生命周期基础。** 建立统一能力元数据、artifact/asset 状态转换、持久异步任务、幂等、墓碑和保留谓词；在窄模块后收口生命周期所有权，同时保持现有 Raw/OCI/Maven 行为兼容。
2. **补齐当前格式。** 完成 Conan Hosted 发布/删除/搜索，Raw 列表/校验和/可恢复上传，OCI catalog/referrers，Maven 浏览/搜索和受支持的发布 companion。每种格式均需黑盒客户端覆盖发布、解析、删除、保留和恢复。
3. **仓库体验 API。** 增加版本化管理 API，支持制品浏览、坐标搜索、墓碑检查、保留执行和有界异步任务状态，不向 V1/V2 接口回填不兼容行为。
4. **分发工作流。** 先增加策略门禁的 Hosted Repository 间晋级，再实现带检查点、完整性校验、重试和目标授权的可恢复复制。
5. **生产规模。** 增加容量核算、Repository 配额、并发限制、复制可观测性、覆盖所有生命周期状态的备份恢复，以及与部署 CI 集成的发布证据。

## 当前状态

OCI、Maven、Raw 和 Conan 的五个阶段均已实现。各格式具备 Hosted、Proxy、Group、原生生命周期、适用的管理浏览、晋级、断点复制和后端完成清单中的运维覆盖。

本文保留为架构实施顺序；当前发布就绪度由可执行门禁和证据记录决定，而非新的实现切片。
