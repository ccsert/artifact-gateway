# Nexus 差距分析

[English](nexus-gap-analysis.md) | [文档索引](README.zh-CN.md)

状态：Artifact Gateway 与 Sonatype Nexus Repository Manager 的能力和体验对比。本文维护跨产品差距。

具体协议以[兼容矩阵](protocol-compatibility.zh-CN.md)为准，V1 目标见[完整制品库目标](full-artifact-repository-goal.zh-CN.md)，Console 改进见[Repository Console 路线图](repository-console-experience-roadmap.zh-CN.md)。

## 范围

Artifact Gateway 已覆盖 OCI、Maven、Raw、Conan、npm、PyPI 的 Hosted/Proxy/Group，并支持 Go 的完整 Hosted/Proxy/Group 路径。Nexus 是覆盖二十余生态的成熟通用制品库。

本文刻意不把“格式数量差异”列为一般差距，而比较任意成熟制品库都应具有的横向能力；共同格式的深度差异单列。Gateway 基线为后端完成清单与当前 React 19/Ant Design 6 Console。

## 客户端迁移边界

Artifact Gateway 现已为 Maven、npm、PyPI、Raw、Go 的 Hosted/Proxy/Group 接受 Nexus
风格 `/repository/<name>/...` 根路径。普通 Maven/Gradle、npm、twine/pip、Raw HTTP 和
GOPROXY 迁移不再需要改写 Base Path；Go Hosted 还接受 Nexus 3.93+ 的版本 ZIP 上传，
并从归档推导经过授权的模块身份，以上流程都有真实客户端门禁覆盖。`maven` 这个精确
名称仍由旧 canonical Maven 前缀保留，迁移时必须改名。这不负责导入 Nexus
元数据，也不模拟 Nexus 管理 API。OCI 仍属于有条件迁移：Registry V2 把 `/v2/` 固定
在 Host 根路径，而 Nexus 常用 Connector 端口或虚拟主机选择 Docker Repository；部署
时需要把外部端点映射到 Gateway Repository 名称前缀，或对镜像重新命名。精确路径与
证据见[协议兼容性矩阵](protocol-compatibility.zh-CN.md#nexus-风格-repository-根路径)。

## 差距摘要

| 区域 | Artifact Gateway 现状 | 剩余差距 | 严重度 |
| --- | --- | --- | --- |
| 身份与 RBAC | 本地用户、API Key、Service Account、授权角色/模板、Grant、effective access、OIDC role mapping | 多角色、LDAP/SAML、更丰富 selector | 中 |
| 登录与 SSO | 本地/Bearer、数据库 OIDC Code+PKCE、加密 secret、session inventory/revoke | back-channel 与 IdP 发起 logout | 低 |
| 全局搜索 | 服务端跨 Repository 坐标/路径/摘要搜索、权限过滤、深链接 | class-name、saved query | 中 |
| 上传 UI | Maven wizard、Raw upload；OCI/Conan 用原生 client | 更多格式 UI | 中 |
| Repository 编辑 | Proxy endpoint/allowlist 乐观并发编辑 | name/format/type 设计上不可变 | 低 |
| 安全管理 | User/API Key/Service Account/Role/Grant/OIDC | LDAP/SAML、soft delete | 中 |
| 调度 | 固定周期 retention、手动 dispatch、history | cron 与更多 task type | 低 |
| 存储 | 单 RustFS/S3 与引用安全回收 | 多 blob store、compaction UI | 中 |
| 安全扫描 | 外部多 asset scanner、持久任务、finding、准入、隔离、健康/新鲜度 | 合规报告、自动策略扩展 | 中 |
| Dashboard | 格式容量和本地 sample trend | 服务端时序、throughput、top-N | 低 |
| 分发控制 | cancel/retry/run-now、Jobs、intelligence reconcile | 通用 scheduler | 低 |
| 通知 | HMAC Webhook、retry/dead-letter/replay | email 和更多事件 | 低 |

## 功能差距

### 身份与授权

Nexus 以 User→Role→Privilege→Repository/path/content selector 建模，并集成 LDAP/SAML/Crowd/OIDC。Gateway 已提供 break-glass Token、本地用户、API Key、Service Account、运行时 OIDC、全局角色与 Repository Grant。

本地账户记录档案、登录/改密、失败锁定、强制改密、session version 和有界 session metadata，保护最后 active admin。OIDC 身份可显式或通过受控 JIT/verified email 绑定本地账户。管理员可查看/撤销单个或全部 session。

Grant 可按 canonical resource prefix 限定，effective-access 使用同一运行时 evaluator 解释 actor、role、Repository、resource 的 source/reason。可复用 Authorization Role 与 Template 在应用时复制 scope snapshot，避免后续角色编辑静默改变既有权限。

剩余差距是多角色、selector 组合、LDAP/SAML 同步、OIDC back-channel/IdP logout、soft delete/restore 和密码复杂度/过期策略。匿名仍是显式全局+Repository 策略，不作为通用 role。

### 分发与集成

Gateway 的不可变晋级、断点复制、重试恢复和 SHA-256 校验较强。差距包括：Nexus Maven Staging 类工作流、更广 Webhook 事件、邮件/SMTP、按 path routing rule，以及复制到外部 blob destination。

### 存储后端

Nexus 支持 file/S3/Azure、多 blob store/group 和 compaction。Gateway 只使用一个 S3-compatible RustFS，Orphan Collector 的 Tombstone、宽限期和引用复查比普通 trash 更严格，但无多 store、compaction 或 blob 管理 UI。

### 任务调度

Gateway 已有管理员 task catalog，覆盖 Repository/Audit retention、固定间隔、启停、手动 dispatch 和历史；PostgreSQL `SKIP LOCKED` 防止多 scheduler 重复，停机恢复只发一次而非 catch-up storm。Cron、index rebuild、compaction、export 和更多类型仍缺失；不接受任意命令或 SQL。

### 安全与质量扫描

Gateway 以独立 `repositories:intelligence` 保存 signature、SBOM、provenance、license、vulnerability，准入策略在晋级前评估并向目标传播不可变证据，不覆盖目标自有记录。

外部 scanner、数据库新鲜度、finding、Quarantine 和默认关闭的协议读取强制已交付。剩余差距为合规报告、组件热度/下载统计及更自动化处置。

### 共同格式深度

Raw 已有 PUT/DELETE、prefix list、checksum、resumable upload、GET/HEAD/单 Range；缺少条件写与非 HTTP 工具。Conan 已有 Conan 2 Hosted lifecycle/晋级/复制；不支持 Conan 1、remote copy、通用 index aggregation。OCI/Conan 仍缺少 Nexus 风格的 GC 和 upload recovery 运维视图。

## Console 与体验差距

Console 是桌面优先的 React 19 + Ant Design 6 SPA，支持分组可折叠导航、全局搜索、公开浏览、双语与明暗主题。剩余重点是 intelligence 与运维工具，不是基本导航。

### 登录、导航与 Dashboard

`/login` 支持本地、Bearer 和动态 OIDC；OIDC 保存、测试、callback、role mapping、logout 无需重启。手输 Bearer 仍在 localStorage，生产应使用 HTTPS 并优先 HttpOnly OIDC。

导航按 Runtime/Governance/Management 分组，Header 提供权限过滤搜索、主题/语言与凭证操作；缺少 command palette 和更丰富 session menu。Dashboard 的 donut 与 sparkline 基于现有数据/浏览器 sample，服务端时序与 top-N 仍缺失。

### 搜索、浏览与发布

`/search` 使用权限过滤的服务端 cursor，可直接链接精确 Artifact，包括 Maven SNAPSHOT build。Artifact Tab 提供格式版本选择和 metadata；缺少 class-name、saved query、热度排序和更丰富 Group resolution。

Maven 有三步 publish wizard，Raw 有认证上传；OCI/Conan 刻意使用原生客户端，避免在 Console 重复大文件/resumable 协议。

### Repository 与安全运维

Repository 可创建、查看、编辑可变 Proxy 字段和删除，使用 `If-Match`；name/format/type 不可变。安全区提供 Users、API Keys、Service Accounts、Access Control、Grant、Anonymous Policy、Authorization Role 和 OIDC mapping。复杂 selector 与外部目录仍缺失。

Operations 提供固定调度、后台任务和脱敏诊断 JSON；缺少 Blob Store、Routing Rule、Email/SMTP、HTTP/SSL、Capability、Feature Flag 与可下载 support bundle。

单 Artifact 删除 UI 主要在 OCI tag；其他格式行缺少统一 delete/download/tag/star/favorite 和丰富 checksum/size/download 属性。Tombstone 可恢复但不会提供违背生命周期契约的“立即 hard purge”。

Replication 可 create/inspect/cancel pending/retry/run-now，checkpoint 受 lease fence；Promotion/Retention 为 lifecycle job。缺少 promotion/replication schedule、cron 和任意 Nexus task。

Webhook 订阅与失败在 Operations 可见，但无全局 Toast/job indicator/email；Jobs Tab 只在打开时每 10 秒刷新。Audit 支持 Repository、Group、Outcome、Format、Actor、Operation 和时间范围，使用签名 cursor 并导出当前服务端页；缺少 saved query/preset。

移动端不是当前发布要求，但双语、Ant locale、日期数字与明暗主题已支持。

## Gateway 的相对优势

- Tombstone、宽限期、引用复查和 Orphan Collector 形成更严格生命周期。
- Promotion 快照不可变源身份并复查后创建目标，不修改源。
- Replication 持久 checkpoint、恢复重试和 SHA-256 校验。
- OpenAPI 生成 Go/Console contract，`make openapi-check` 拒绝漂移。
- 创建与分发操作有 `Idempotency-Key`。
- 前端采用现代 React 19、Vite、Tailwind 4 技术栈。

## 已交付进展

已落地：服务端跨 Repository 搜索和深链接；Audit cursor/CSV；Dashboard donut/sparkline；有界 API Key role/expiry/last-used；Replication cancel；Raw upload；本地/Bearer/OIDC Login；集中 Access Control、Role/Template/evaluator；本地 User 管理；Proxy hot edit。

后续补充了自动 Artifact security evidence、Scanner、健康新鲜度、finding、Quarantine 与六类格式默认关闭的 read policy，以及脱敏 system diagnostics。

明确约束：不提供绕过 Tombstone/宽限期/引用复查的 hard purge；API Key 当前 RBAC 已有 role/expiry/revoke/last-used，任意 privilege expression 延期；Hosted lifecycle 已覆盖 Maven、OCI、Raw、Conan、npm、PyPI，Replication 使用 fenced checkpoint。

## 优先 Backlog

1. 扩展 Grant Template 的 selector 组合，保留 effective-access 预演。
2. 在现有诊断上增加脱敏可下载日志与有界数据库证据。
3. 增加服务端时序、throughput、cache hit、storage growth 与 top-N。
4. 扩大 scheduled task 类型、blob store 管理和系统设置。
5. 将 Webhook 扩展到 lifecycle/scan，增加 email 和格式广度；hard purge 继续不在范围。

本列表为建议；权威目标仍是[完整制品库目标](full-artifact-repository-goal.zh-CN.md)。
