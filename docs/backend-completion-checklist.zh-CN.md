# 后端完成清单

[English](backend-completion-checklist.md) | [文档索引](README.zh-CN.md)

本清单是 **完整制品库 V1** 的后端工作基线，明确不包含前端工作。

## 立即规则

- 不得增加新的 `/conan/v3` 路由族。Conan Hosted 应扩展现有 Conan 后端并明确区分 Hosted 与 Group 解析，同时保持当前 read-through 行为。

## 生命周期基础

- [x] 共享 Artifact 状态模型和生命周期任务 Store。
- [x] OCI manifest 墓碑与回收 Worker。
- [x] Maven 墓碑/保留、可恢复删除和回收 Worker。
- [x] Raw 回收 Worker。
- [x] Conan 原生 recipe/package revision 状态模型。
- [x] Conan HTTP Hosted 发布、解析、删除和回收 Worker。
- [x] Go Hosted 保留、可恢复墓碑、延迟回收和容量释放。

## 协议完成度

- [x] OCI catalog 分页与 referrers 接口、浏览/搜索投影。
- [x] Maven 浏览/搜索投影及发布 companion 加固和黑盒夹具。
- [x] Raw 对象列表、校验和元数据/sidecar 和可恢复上传。
- [x] Conan Hosted publish/session、元数据/文件读取、逻辑删除/恢复和搜索投影。

## 管理 API

- [x] 按格式与类型返回 Repository 能力。
- [x] 跨格式 Artifact 浏览/搜索。
- [x] 墓碑检查和生命周期任务状态。
- [x] Maven 保留执行、dry-run 报告和受支持 Artifact 的恢复接口。

## 分发

- [x] Maven 不可变晋级 API/Worker，包含 HTTP 重试和 PostgreSQL/RustFS 证据。
- [x] OCI、Raw、Conan 不可变晋级，包含授权、幂等、审计、重试、HTTP 黑盒和持久化证据。
- [x] 复制计划模型及带持久检查点、重试、恢复和 SHA-256 校验的 Worker。
- [x] 晋级/复制授权和审计事件。
- [x] Go 三表示不可变晋级与断点复制，包含隔离复查和跨实例证据。

## 运维

- [x] OCI、Maven、Raw、Conan Hosted 的 Repository 配额核算。
- [x] 生命周期任务的每仓库并发限制，以及任务、墓碑、晋级、复制指标。
- [x] 持久扫描任务、不可变身份状态、发布幂等和缺失扫描补偿。
- [x] 脱敏扫描器健康信息及 Gateway 强制的漏洞库新鲜度诊断。
- [x] 事务运维事件与 HMAC 签名 Webhook，覆盖隔离/释放、租约恢复、有界重试、死信重放和管理可见性。
- [x] 晋级复制状态的备份恢复，生命周期发布预检和证据覆盖。
- [x] OCI、Maven、Raw、Conan 发布、解析、删除、保留和恢复黑盒测试。

## 当前下一阶段

1. 在不削弱 Hosted 保证的前提下关闭 Go 兼容缺口：默认关闭的隔离读取强制、认证 Proxy 上游和校验和数据库镜像。
2. 不扩大公开格式档案地完成 APT H3。轮换重叠、外部 HTTPS/client 演练、备份恢复验证、指标和操作员状态已完成；托管 KMS/HSM、恢复、快照导出和部署告警仍待完成。
3. APT H3 生产签名门禁之后继续 Cargo C0。发布 framing、`.crate`/manifest 身份、稀疏索引转换、fuzz 边界和官方客户端契约测试已完成；C1 或格式枚举前仍需非公开持久碰撞预留与 Memory/PostgreSQL 一致性。
4. 只有当网络策略、持久 SBOM、资源限制和真实扫描 smoke test 同步进入一个切片时，才向 Kubernetes overlay 增加可选扫描 Workload。H3 替换前，本地 APT 参考 signer 继续限制在 loopback sidecar 和专用 key volume。
5. NuGet 保持延期；维持有界解析器测试，但在 Cargo C0-C1 完成或客户需求改变优先级前不实现发布持久化。
6. 扩大任何公开能力前，运行发布门禁并把输出保存在发布记录中。
