# 完整制品库 V1 目标

[English](full-artifact-repository-goal.md) | [文档索引](README.zh-CN.md)

## 目标

将 Artifact Gateway V1 交付为面向 OCI、Maven、Raw 和 Conan 的完整制品库。只有当每种受支持格式都能参与 Hosted、Proxy、Group 工作流，具备安全生命周期，并能像仓库一样运维和恢复，而不只是上游读取网关，版本才算完成。

本目标取代此前“读取路径完成”或“只处理生产就绪度”的目标。每个阶段仍必须包含预检、证据收集和恢复演练。

## 完成定义

- **生命周期：** staged、visible、tombstoned 在各格式间一致；发布原子化；物理回收延迟执行、重查引用并通过持久任务完成。
- **格式一致性：** OCI、Maven、Raw、Conan 均支持 Hosted、Proxy、Group；原生发布、解析、浏览搜索、逻辑删除、保留和恢复由黑盒协议测试覆盖。
- **管理：** 版本化 API 通过明确契约暴露仓库能力、制品浏览搜索、墓碑检查、保留执行和任务状态，服务端与消费方同步修改。
- **分发：** 管理员可在 Hosted Repository 间晋级不可变 Artifact；复制可恢复、有完整性校验、授权、可观测且幂等。
- **运维：** Repository 配额、容量、并发限制、审计、指标、备份恢复、升级回滚、发布预检和生产证据覆盖所有生命周期与后台操作。
- **原生契约：** 原生包协议遵循标准并接受回归验证；历史管理 API、Nexus URL、认证模型和内部接口均不再作为兼容要求，见 [ADR 0009](adr/0009-native-architecture.zh-CN.md)。

## 有序工作包

1. **完成生命周期基础。** 在现有墓碑和任务 Store 上增加回收 Worker；把 Raw/Maven 删除与回收迁入统一契约；定义 Conan 原生状态模型。
2. **补齐原生格式。** 完成 OCI catalog/referrers/浏览，Raw 列表/校验和/可恢复上传，Maven 浏览搜索/发布 companion，以及 Conan Hosted 发布、删除、元数据、搜索和生命周期。
3. **交付仓库体验 API。** 提供制品、墓碑、保留、能力和任务状态的版本化管理接口。
4. **交付分发工作流。** 先实现晋级，再实现含验证与重试的断点复制。
5. **完成运维就绪。** 将配额和并发控制应用到仓库操作，并把备份恢复、可观测、预检、证据和发布门禁扩展到所有新状态转换。

## 当前检查点

初始四格式 V1 后端工作包已完成，生命周期、格式行为、管理 API、分发和运维控制均记录在[后端完成清单](backend-completion-checklist.zh-CN.md)。此检查点不关闭更广的格式、私有上游和迁移 backlog；当前可执行能力与限制以[协议矩阵](protocol-compatibility.zh-CN.md)为准，每个交付切片分别记录[发布就绪](release-readiness.zh-CN.md)门禁结果。

## 约束

- 不用通用上传抽象替代协议原生发布流程。
- 任何物理删除都不得绕过 Tombstone、宽限期和引用检查。
- 新生态必须先有生命周期与协议契约负责人、迁移计划和黑盒客户端测试计划；执行清单见[格式扩展指南](format-extension-guide.zh-CN.md)。
- 允许破坏历史管理接口；同步修改 OpenAPI 源、服务端、Console 和 agctl，说明新契约并删除旧实现。标准包协议正确性与字节完整性仍必须保证。

## 下一计划格式：APT Hosted

APT Proxy 和有序 Group 已按当前只读范围通过准入。APT Hosted 分四个明确里程碑交付，而不是提前扩大格式枚举：

1. 发布契约、Debian 包身份、持久化与签名边界；
2. 原子生成 `Packages` 和 `Release` 快照并通过真实 APT 客户端测试；
3. 可信签名、密钥轮换、备份和运维恢复；
4. 生命周期、`.deb` 扫描、隔离、晋级与复制一致性。

在最低 Hosted 和生产签名门禁完成前，能力 API 公开 APT Proxy 和 Group 读取。详见 [APT Hosted 路线图](apt-hosted-roadmap.zh-CN.md)。

## 当前格式扩展

Cargo 已公开支持 Hosted、Proxy 和 Group，当前范围与限制见[协议矩阵](protocol-compatibility.zh-CN.md)；旧的 Cargo 候选顺序不再作为实施安排。

NuGet Hosted 发布及原生 V3 读取已通过 #287 开始实施，随后由 #288 接 Proxy/Group、#289 接治理与公开准入。复用既有有界解析器；准入门禁完成前仍不进入正式能力目录。详见 [NuGet 路线图](nuget-roadmap.zh-CN.md)。
