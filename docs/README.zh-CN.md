# Artifact Gateway 文档

[English](README.md) · [项目中文 README](../README.zh-CN.md)

本索引将当前有效的契约与运维手册和路线图材料分开。能力声明只有在同时符合可执行测试
与[协议兼容性基线](protocol-compatibility.zh-CN.md)时，才代表当前状态。

语言说明：未标记的条目提供中文正文；标为“仅英文”的条目仍是当前权威资料，但尚未
完成中文化。中文入口不会把英文正文伪装成已经翻译的内容。

## 从这里开始

- [快速入门](getting-started.zh-CN.md) — 启动完整本地栈并创建第一个仓库。
- [整体架构](../ARCHITECTURE.zh-CN.md) — 运行角色、存储、一致性与代码职责。
- [架构图](architecture-diagrams.zh-CN.md) — 系统、部署、发布与后台任务流程。
- [协议兼容性](protocol-compatibility.zh-CN.md) — 各格式已实现行为和明确限制。
- [参与开发](../CONTRIBUTING.zh-CN.md) — 变更流程与必需检查。
- [变更记录](../CHANGELOG.md)（仅英文）— 当前开发阶段的用户可见变化。
- [项目质量评估](project-quality-assessment.zh-CN.md) — 当前强项、风险与优化顺序。
- [性能基线](performance-baseline.zh-CN.md) — 二进制与镜像体积、静默内存、本地并发和边界。

## 核心契约

- [Native Hosted 契约](native-hosted-contract.md)（仅英文）
- [制品生命周期契约](artifact-lifecycle-contract.md)（仅英文）
- [仓库删除契约](repository-deletion-contract.md)（仅英文）
- [V2 管理契约](v2-contract.md)（仅英文）
- [PostgreSQL 能力](postgresql-capabilities.md)
- [格式扩展指南](format-extension-guide.md)（仅英文）
- [OpenAPI 治理计划](openapi-governance-plan.md)（仅英文）
- [完整制品库目标](full-artifact-repository-goal.md)（仅英文）
- [完整制品库路线图](full-artifact-repository-roadmap.md)（仅英文）

### 架构决策

- [ADR 0001：完整制品库](adr/0001-full-artifact-repository.md)（仅英文）
- [ADR 0002：晋升快照](adr/0002-promotion-snapshots.md)（仅英文）
- [ADR 0003：仅协议格式](adr/0003-protocol-only-formats.md)（仅英文）
- [ADR 0004：Go Hosted 发布](adr/0004-go-hosted-publication.md)（仅英文）

## 格式与上游

- [APT Proxy](apt-proxy.md)（仅英文）
- [APT Hosted 路线图](apt-hosted-roadmap.md)（仅英文）
- [APT Hosted 签名](apt-hosted-signing.md)（仅英文）
- [Cargo 仓库研究](cargo-repository-research.md)（仅英文）
- [NuGet 路线图](nuget-roadmap.md)（仅英文）
- [旧 Group 迁移](legacy-group-migration.md)（仅英文）
- [Gitea 后端参考](gitea-backend-reference.md)（仅英文）

## 身份、策略与集成

- [匿名访问运维](anonymous-access-operations.md)（仅英文）
- [本地用户治理](user-governance.md)（仅英文）
- [OIDC SSO](oidc-sso.md)（仅英文）
- [Kubernetes 上的 Keycloak](oidc-keycloak-k8s.md)（仅英文）
- [仓库授权计划](repository-grant-authorization-plan.md)（仅英文）
- [Service Account 运维](service-account-operations.md)（仅英文）
- [安全准入策略](security-admission-policy.md)（仅英文）
- [制品扫描器契约](artifact-scanner-contract.md)（仅英文）
- [出网代理设计](proxy-egress-design.md)（仅英文）
- [Webhook 投递契约](webhook-delivery-contract.md)（仅英文）

## 部署与运维

- [性能基线](performance-baseline.zh-CN.md)
- [分布式部署](distributed-deployment.md)
- [Kubernetes 部署](kubernetes-deployment.md)（仅英文）
- [恢复手册](recovery-runbook.zh-CN.md)
- [后端完成度检查表](backend-completion-checklist.md)（仅英文）

## 产品差距与准备证据

以下材料记录内部准备工作和路线图证据，不代表公开发布、正式分发记录或支持承诺。

- [Nexus 差距分析](nexus-gap-analysis.md)（仅英文）
- [Nexus 差距复核（2026-08）](nexus-gap-review-2026-08.md)
- [仓库 Console 体验路线图](repository-console-experience-roadmap.md)（仅英文）
- [发布准备工作清单](release-readiness.md)（仅英文）
- [内部记录模板](release-record-template.md)（仅英文）
- [内部准备记录（2026-08-11）](release-records/2026-08-11-d738d4ed.md)（仅英文）

## 文档规则

- 英文、简体中文 README 与快速入门入口必须互相链接，并保持行为说明一致。
- 具体协议能力同步维护在 `protocol-compatibility.md` 与
  `protocol-compatibility.zh-CN.md`，不要把长篇格式契约复制进项目 README。
- 新增索引条目时必须指向对应语言正文；只有英文内容时显式标记“仅英文”。
- 预览、研究和路线图工作必须显式标注，不能写成已经交付的能力。
- 当变更引入运维决策、恢复步骤或新的公开契约时，新增或更新聚焦的专题文档。
- 提交文档变更前执行 `make docs-check`。
