# Artifact Gateway 文档

[English](README.md) · [项目中文 README](../README.zh-CN.md)

Artifact Gateway 是轻量级、协议原生的制品库。控制面只依赖 PostgreSQL 作为数据库与协调
依赖，不可变字节保存在 S3 兼容对象存储中。

本索引遵循 [`site-map.json`](site-map.json) 的框架中立导航契约。能力声明只有同时符合
可执行测试与[协议兼容性基线](protocol-compatibility.zh-CN.md)时，才代表当前状态。

## 快速开始

- [项目概览](../README.zh-CN.md) — 定位、能力、快速启动与轻量部署边界。
- [文档站接入指南](documentation-site-guide.zh-CN.md) — locale、路由、导航与站点生成契约。
- [快速入门](getting-started.zh-CN.md) — 启动完整本地栈并创建第一个 Repository。
- [产品方向](../PRODUCT.zh-CN.md) — 用户、场景、产品边界和优先级。
- [参与开发](../CONTRIBUTING.zh-CN.md) — 变更流程、文档约定与必需检查。
- [格式扩展指南](format-extension-guide.zh-CN.md) — 新包生态的完整准入要求。

## 架构与设计

- [整体架构](../ARCHITECTURE.zh-CN.md) — 运行角色、存储、一致性与代码职责。
- [架构图](architecture-diagrams.zh-CN.md) — 系统、部署、发布与 Worker 流程。
- [设计系统](../DESIGN.zh-CN.md) — Console 视觉语言、组件、布局与交互规则。
- [领域词汇](../CONTEXT.zh-CN.md) — Repository、Artifact、生命周期和分发的规范术语。
- [PostgreSQL 能力](postgresql-capabilities.zh-CN.md) — 锁、租约、队列、通知、JSONB、搜索与可观测性。
- [Native Hosted 契约](native-hosted-contract.zh-CN.md) — 元数据权威、对象生命周期、事务与幂等。
- [V2 契约](v2-contract.zh-CN.md) — Raw、Conan 2、匿名策略与迁移的历史背景。
- [制品生命周期契约](artifact-lifecycle-contract.zh-CN.md) — 可见性、墓碑、恢复、回收、晋级与复制。
- [Repository 删除契约](repository-deletion-contract.zh-CN.md) — 安全逻辑删除和恢复语义。
- [分布式部署](distributed-deployment.zh-CN.md) — API、Scheduler、Worker、PostgreSQL 与对象存储拓扑。
- [ADR 0001：完整制品库](adr/0001-full-artifact-repository.zh-CN.md)
- [ADR 0002：晋级快照](adr/0002-promotion-snapshots.zh-CN.md)
- [ADR 0003：仅协议格式](adr/0003-protocol-only-formats.zh-CN.md)
- [ADR 0004：Go Hosted 发布](adr/0004-go-hosted-publication.zh-CN.md)
- [ADR 0005：Console 语义主题系统](adr/0005-console-semantic-theme-system.zh-CN.md)

## 协议与仓库格式

- [协议兼容性](protocol-compatibility.zh-CN.md) — 各格式已实现行为和明确限制。
- [Maven Hosted 发布](maven-hosted-publication.zh-CN.md) — 默认 Nexus 兼容直接模式与严格可选提交。
- [APT Proxy 与 Group](apt-proxy.zh-CN.md) — 字节保持读取、缓存、授权与限制。
- [APT Hosted 签名](apt-hosted-signing.zh-CN.md) — H2 预览、外部 signer、H3 轮换和生产边界。
- [APT Hosted 路线图](apt-hosted-roadmap.zh-CN.md) — H1-H4 有序验收门禁。
- [Cargo 仓库研究](cargo-repository-research.zh-CN.md) — Sparse Registry 建议与 C0-C4 路线图。
- [NuGet 路线图](nuget-roadmap.zh-CN.md) — 已延期的协议与生命周期计划。
- [旧 Group 迁移](legacy-group-migration.zh-CN.md) — 兼容行为与迁移指引。
- [Gitea 后端参考](gitea-backend-reference.zh-CN.md) — Preflight 和证据闭环设计参考。

## 运维与安全

- [Kubernetes 部署](kubernetes-deployment.zh-CN.md) — 可执行本地基线与生产要求。
- [恢复手册](recovery-runbook.zh-CN.md) — 备份、恢复、RPO/RTO 证据与回滚。
- [匿名访问运维](anonymous-access-operations.zh-CN.md) — 默认拒绝的全局、Group、Repository 门禁。
- [本地用户治理](user-governance.zh-CN.md) — 账户、Session、锁定、OIDC 关联与审计。
- [OIDC 浏览器 SSO](oidc-sso.zh-CN.md) — Code+PKCE、运行时配置、角色映射和 Session。
- [Kubernetes 上的 Keycloak](oidc-keycloak-k8s.zh-CN.md) — 真实浏览器 callback 验收。
- [Repository Grant 授权](repository-grant-authorization-plan.zh-CN.md) — Scoped 运行时判定和推进方式。
- [Service Account 运维](service-account-operations.zh-CN.md) — 稳定机器身份和零停机轮换。
- [安全准入策略](security-admission-policy.zh-CN.md) — 晋级证据、隔离和可选读取强制。
- [Artifact 扫描器契约](artifact-scanner-contract.zh-CN.md) — 有界外部扫描、健康、证据与补偿。
- [Proxy 出站设计](proxy-egress-design.zh-CN.md) — 每 Repository 的直连、环境、HTTP 与 SOCKS5。
- [Webhook 投递契约](webhook-delivery-contract.zh-CN.md) — 事务事件、HMAC、重试与重放。
- [安全策略](../SECURITY.zh-CN.md) — 私密报告和部署安全基线。

## 质量、性能与发布

- [性能基线](performance-baseline.zh-CN.md) — 二进制/镜像体积、静默内存、并发与大对象证据。
- [项目质量评估](project-quality-assessment.zh-CN.md) — 当前强项、风险与优化优先级。
- [后端完成清单](backend-completion-checklist.zh-CN.md) — V1 实现状态与下一阶段。
- [发布就绪](release-readiness.zh-CN.md) — 协议、集成、升级与恢复的可执行门禁。
- [发布记录模板](release-record-template.zh-CN.md) — 证据、批准与回滚字段。
- [准备记录：2026-08-11](release-records/2026-08-11-d738d4ed.zh-CN.md) — 历史本地候选证据，不是生产批准。
- [变更记录](../CHANGELOG.zh-CN.md) — `Unreleased` 下的用户可见变化。

## 研究、路线图与参考

- [完整制品库目标](full-artifact-repository-goal.zh-CN.md) — V1 完成定义。
- [完整制品库路线图](full-artifact-repository-roadmap.zh-CN.md) — 架构顺序和当前状态。
- [Nexus 差距分析](nexus-gap-analysis.zh-CN.md) — 当前跨产品能力与体验对比。
- [Nexus 差距复核（2026-08）](nexus-gap-review-2026-08.zh-CN.md) — 历史时点审查。
- [Nexus Maven 发布研究](nexus-maven-publication-research.zh-CN.md) — 直接模式与 Staging 的一手证据。
- [Repository Console 路线图](repository-console-experience-roadmap.zh-CN.md) — 浏览、容量、Proxy 运维与策略体验。
- [OpenAPI 治理](openapi-governance-plan.zh-CN.md) — 源、生成、运行时与评审边界。

## 文档规则

- 英文使用无后缀 `.md`，简体中文使用 `.zh-CN.md`。
- 每对页面互相链接，并以本地化标题在 `site-map.json` 中恰好出现一次。
- 两种语言都保留命令、路由、状态码、兼容限制、安全边界和交付状态。
- 研究、预览、历史证据和路线图必须显式标识，不能写成已经交付。
- 提交文档变更前运行 `make docs-check`。
