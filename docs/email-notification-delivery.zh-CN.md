# 管理员测试邮件交付

[English](email-notification-delivery.md) | [文档索引](README.zh-CN.md)

已批准的小规模部署路线是有限内置通道：Gateway 负责版本化邮件模板和有界
SMTP 发送，PostgreSQL 负责持久收件人快照、测试交付状态、幂等与 Worker fencing。
Prometheus/Alertmanager 可负责外部可用性监测，但增加组件和路由配置。Gateway
或数据库停机时，内置通道不能报告自身失联。决策见
[#211](https://github.com/ccsert/artifact-gateway/issues/211) 与
[#212](https://github.com/ccsert/artifact-gateway/issues/212)。

本批实现**管理员显式发起的合成测试邮件**。容量和 5xx 观测不入邮件队列。
告警评估、路由与 Console 页面仍待后续。
[#229](https://github.com/ccsert/artifact-gateway/issues/229)、
[#230](https://github.com/ccsert/artifact-gateway/issues/230) 的完整范围未交付；
浏览器截图不能代表 Gmail、Outlook 或 Apple Mail 客户端验收。

## 部署契约

未配置文件时默认关闭。`GATEWAY_EMAIL_CONFIG_FILE` 指向运维管理的绝对路径
JSON 文件，启动时离线读取，拒绝未知键。以下仅为合成文档样例：

```json
{
  "enabled": true,
  "host": "smtp.example.test",
  "port": 465,
  "mode": "implicit_tls",
  "from": "gateway@example.test",
  "approvedIPs": ["192.0.2.10"],
  "consoleOrigin": "https://console.example.invalid"
}
```

这些地址不是可用中继。真实 SMTP 与收件人须另行作部署决定。管理 API 不能
提交中继地址、认证、头部或任意正文。

- `mode` 仅允许 `implicit_tls` 或 `starttls_required`。必须 TLS 1.2 及以上，
  验证证书链和主机名，禁止明文回退。
- 每个 DNS 解析结果须属于 `approvedIPs`，直接连接获准 IP，TLS 仍验证 `host`。
  可明确允许私有中继 IP；拒绝 loopback、link-local/metadata、组播和未指定地址。
  DNS 变化须更新配置并重启。
- 可选 `caFile` 为私有 PEM 信任根的绝对路径。可选 `authFile` 为含
  `username`、`password` 的普通 JSON 文件绝对路径，权限限文件所有者
  （0600 或更严格）。仅在 TLS 验证后认证，内容不进入日志或 API。
  原子替换文件可轮换，Worker 每次尝试重新读取。
- 通过现有部署秘密管理配置 `GATEWAY_SETTINGS_ENCRYPTION_KEY`。收件人使用
  AES-GCM 和目标专属关联数据加密；存在排队快照时保持加密密钥稳定。
- 可选 `consoleOrigin` 必须是明确的 HTTPS origin，不能含凭据、查询或 fragment。
  服务端固定追加 `/system?tab=diagnostics`，不用请求 Host 或事件自带 URL。
  未配置则省略按钮。
- API 与 Worker 节点须使用一致的中继、加密密钥与信任配置。独立 Worker 使用
  `GATEWAY_NODE_ROLES=worker`、`GATEWAY_WORKER_KINDS=email`；空 kind 过滤包含邮件。

## 管理员 API

全部端点要求平台管理员，拒绝强制改密 Session，响应为 `Cache-Control: no-store`。

| 端点 | 行为 |
| --- | --- |
| `POST /api/v2/email-notifications:preview` | 固定 `scenario`、`locale` 离线预览，返回 subject/HTML/text/模板版本，从不发送 |
| `GET /api/v2/email-notifications` | 安全就绪状态：`ready`、`email_disabled`、`encryption_key_unavailable` |
| `GET/POST /api/v2/email-targets` | 列出/新建单收件人目标，默认停用，收件人只写 |
| `GET/PUT /api/v2/email-targets/{targetId}` | 查询/更新，更新须 `If-Match`，省略 recipient 保留密文 |
| `POST /api/v2/email-notifications:test` | `targetId`、`scenario`、目标 `If-Match` 与 UUID `Idempotency-Key`；入合成队列后返回 202 |
| `GET /api/v2/email-deliveries` | 最新安全状态，`limit` 1–100，默认 50 |
| `GET /api/v2/email-deliveries/{deliveryId}` | 状态和版本 |
| `POST /api/v2/email-deliveries/{deliveryId}:replay` | 仅 dead，须 `If-Match`、启用且未变更的目标及中继 |

语言为 `en`、`zh-CN`，场景为 `warning`、`critical`、`resolved`。拒绝未知字段
与地址/头部注入。目标只返回 `recipientConfigured`，不返回地址或密文。
状态只返回有限错误码、尝试次数与时间，不含 SMTP 原始回复、主机、凭据或正文。
目标变更、显式测试和重放只以 ID 记审计。

同幂等键、同描述符返回原记录；换目标/版本/场景则 409。PostgreSQL 原子限制
全局滚动一分钟最多五次新测试/重放、最多 1,000 条活动交付；重复请求不多占名额。
版本冲突 412，停用目标或非 dead 重放 409，限流 429，中继停用/无密钥/队列满 503。
重放保留事件 ID、收件人密文、内容描述符和可能重复标记。目标变更须新建显式测试。

## 投递语义

状态为 `pending`、`delivering`、`retrying`、`accepted`、`dead`。每次领取使用
数据库时间、`FOR UPDATE SKIP LOCKED`、新 fencing token 和 30 秒租约。重启生成
新的 Worker Session。过期领取可以重领，旧 token 无法完成。SMTP 单次总时限
10 秒，并受领取调用前取得的本机 monotonic 起点加 25 秒的保守预算限制，避免把
数据库 wall clock 当成本机截止时间。

最多八次尝试，5 秒指数退避，上限一小时。网络失败与 SMTP 4xx 重试；5xx 或
无效内容终止。TLS/认证错误使用固定码和有限次数。发送前发现目标停用或版本变化
则 dead；已经在途的邮件可能完成，停用不能撤回。

DATA 最终正回复仅表示 **SMTP 已接受**，不等于进收件箱或已读。正文发出后丢失
最终回复为 `outcome_unknown`，重试可能重复。过期在途租约也保守标记
`possibleDuplicate`，此标记跨重试和重放保留。不可变版本 1 合成描述符使用稳定
Message-ID、时间与 MIME boundary，并持久快照 From/Console origin，配置变化不改正文；不承诺 exactly-once。

HTML 使用转义语义值、内联关键样式、presentation table、600px 最大宽度以及
窄屏/深色增强。严重级别同时以文字和颜色区分；multipart/alternative 包含等价
UTF-8 纯文本与 HTML。没有外部图片、字体、跟踪像素或附件。版本 1 模板须继续
支持已排队描述符的重放。终态历史保留在 PostgreSQL，归档/保留策略待后续；查询
与活动积压有界，本批不构建通用邮件或监控平台。

## 验证与下一批

测试通过公开 HTTP、Store 和 Sender 边界，使用隔离 PostgreSQL 与自有 TLS SMTP
收集器，收集器从不转发。覆盖重启恢复、并发全局限流、Worker fencing、重试耗尽、
安全重放、TLS 信任/降级失败、实际双语 multipart MIME 和管理员权限。
`make integration-test` 包含这些测试。

下一批另做有限容量评估器，将真实不可变事件与交付在同一事务创建。Repository
配额使用逻辑字节与明确配额；显式本地挂载使用进程可见的可用字节；S3/NAS 池
容量保持 unknown。真实告警启用前须验证持续超限、恢复滞回、陈旧/未知数据、冷却
与共享评估职责。见[运维信号](operational-signals.zh-CN.md)和
[#211](https://github.com/ccsert/artifact-gateway/issues/211)。
