# Product

<!-- impeccable:product-schema 1 -->

> 本记录由现有领域合同、运维文档、发布门禁和控制台实现提取；用户已授权本轮按仓库证据直接推进。未被代码或合同证实的市场、客户与生产支持承诺不在此记录。

## Platform

web

## Users

- **平台与制品库管理员（仓库证据推断）**：在 Console 中管理 Repository、Group、身份、授权、保留策略、生命周期任务、审计与运行健康。
- **开发者与发布工程师（仓库证据推断）**：通过原生 OCI、Raw、Maven、Conan 2、npm、PyPI、Go Module 与 APT 客户端发布或解析制品，并使用公共目录、搜索、深链和可复制的客户端配置。
- **安全与治理人员（仓库证据推断）**：检查扫描结果、不可变摘要、Quarantine、Promotion、Replication、匿名读取策略与审计证据。
- **CI 系统和外部应用**：以稳定的 Service Account 主体和可轮换、可撤销、带有效期的凭据访问被授予的 Repository；它们不是人类 Console 用户，但其零停机轮换与最小授权约束管理体验。

## Product Purpose

Artifact Gateway 是多格式制品仓库与治理控制面。它通过各生态的原生协议存储、解析、治理和分发软件制品，并把生命周期元数据、授权、审计与后台任务保存在 PostgreSQL，把经过验证的内容寻址字节保存在 S3 兼容对象存储中。

成功意味着：原生客户端能按明确能力合同发布或读取制品；操作员能从同一事实源理解来源、身份、扫描、隔离、晋级、复制、保留和恢复状态；高风险操作具备可审计、可重试、幂等和恢复证据。

## Positioning

Artifact Gateway 以 `Repository + format + canonical coordinate + SHA-256 digest` 为不可变治理身份，以协议原生读写、显式 Repository/Proxy/Group 模型和数据库持久化的生命周期工作流为核心。它不是透明重写代理、通用对象浏览器或漏洞扫描器；外部扫描器只通过受版本约束的合同提供情报。

## Operating Context

- 人类操作员主要通过 React/Vite Console 和生成自同一 OpenAPI 合同的管理 API 工作。
- 开发与验收环境使用 Docker Compose 或本地 Kubernetes；生产可用同一镜像拆分 API、scheduler 和 worker 节点职责。
- 原生协议客户端包括 Docker/ORAS、Maven/Gradle、Conan 2、npm、pip/twine、Go 和 Debian APT。
- CI、发布自动化、扫描器和第三方应用使用 Service Account；生产人类身份使用 HTTPS RS256 OIDC，静态管理或解析 Token 只保留给本地与 break-glass 场景。
- 发布判断依赖干净 checkout 上的协议、持久化、Console、升级和备份恢复门禁；本地页面可用或单项健康检查不能替代发布证据。

## Capabilities and Constraints

- OCI、Raw、Maven、Conan 2、npm 与 PyPI 具备完整 Hosted 生命周期；Go Module 具备单 ZIP 原子 Hosted 发布、逻辑删除/恢复、保留策略、延迟物理回收以及 Proxy/Group 读取，晋级与复制尚未开放。
- APT 具备 Proxy/Group 读取，并有管理用途的 Hosted 签名快照预览；该预览不属于稳定兼容性承诺，内置签名器也不是生产密钥托管方案。
- Repository 拥有格式、策略、授权和 Hosted 字节或一个受 allowlist 约束的上游；Group 是有序解析视图，不拥有制品字节。
- Artifact 生命周期只有 `staged`、`visible`、`tombstoned`。Quarantine 是附着于不可变身份的 Repository 本地版本化治理决定，不是第四种生命周期状态，也不是删除。
- 匿名读取默认关闭，必须同时通过全局和 Repository/Group 策略；匿名写入和管理操作不受支持，匿名读取必须进入审计。
- Service Account 没有全局角色；Repository Grant 绑定稳定主体，多个凭据可以在轮换期重叠，明文只在创建时返回。
- PostgreSQL 是元数据与协调事实源；S3 兼容存储只保存验证后的内容寻址字节。迁移前向演进，恢复必须配对 PostgreSQL 与对象存储证据。
- 项目仍处于 active development；不得从当前包版本推断稳定公开发布或生产支持承诺。

## Brand Commitments

- 产品名称固定为 **Artifact Gateway**。
- 界面和文档必须使用 `CONTEXT.md` 定义的领域语言：Repository、Hosted Repository、Proxy Repository、Group、Artifact、Asset、Service Account、Publication、Tombstone、Quarantine、Promotion 与 Replication；不得用相近但改变合同含义的词替代。
- 表达保持克制、精确、面向操作员；不伪造容量、健康、扫描或发布数据，不把候选状态包装成已发布承诺。

## Evidence on Hand

- `CONTEXT.md`：领域语言与所有权边界。
- `README.md`、`ARCHITECTURE.md`：运行模型、格式能力、开发与部署入口。
- `docs/artifact-lifecycle-contract.md`：生命周期与 Quarantine 合同。
- `docs/native-hosted-contract.md`、`docs/v2-contract.md`、`docs/protocol-compatibility.md`：协议与管理合同。
- `docs/service-account-operations.md`、`docs/repository-console-experience-roadmap.md`：非人身份、匿名读取和 Console 任务模型。
- `docs/release-readiness.md`：协议、Console、升级与恢复的发布门禁。
- `api/openapi/` 与生成客户端：管理 API 的权威机器合同。
- `console/e2e/`：桌面、移动、主题、深链、身份与制品工作流的浏览器证据。

不存在可用于产品宣称的客户案例、外部基准、定价、SLA 或稳定发布承诺；未来设计不得自行编造。

## Product Principles

1. **协议真相优先。** 界面不得简化到改变原生客户端、格式能力或 Repository/Group 解析语义。
2. **不可变身份贯穿操作。** 扫描、隔离、晋级、复制和恢复都必须能追溯到规范坐标与摘要。
3. **显式治理，默认收敛。** 匿名访问、上游、授权和高风险转换均由显式策略开启，并在不确定时 fail closed。
4. **稳定主体与可轮换凭据分离。** 自动化身份不因凭据轮换而重建授权或丢失审计连续性。
5. **发布结论来自证据矩阵。** 代码、协议、持久化、Console、升级和恢复门禁共同决定候选是否可发布。

## Accessibility & Inclusion

- Console 提供中文与英文界面，领域术语必须在两种语言中保持合同含义一致。
- 管理任务必须支持键盘操作、可见焦点、语义化加载/错误/成功反馈和 `prefers-reduced-motion`。
- 响应式布局必须覆盖窄屏与高倍缩放，不能以隐藏核心治理能力作为移动适配手段。
