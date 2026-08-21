# Webhook 投递契约

[English](webhook-delivery-contract.md) | [文档索引](README.zh-CN.md)

状态：持久运维 Webhook 投递的规范契约。

## 第一阶段

第一阶段发布两个已具备版本化 Repository 本地身份的安全治理事件：`artifact.quarantined` 与 `artifact.released`。

Artifact 隔离状态转换、不可变事件以及每个匹配且启用订阅的一次投递，必须在同一数据库事务中提交。禁止通过轮询审计日志事后重建事件。

每个事件拥有稳定 UUID，并保存 Repository ID/格式、规范坐标与摘要、隔离状态/原因/操作者/版本，以及发生时间。

投递语义为**至少一次**，消费方必须按事件 ID 去重。不同 Artifact 或订阅之间不保证顺序。

## 订阅

Webhook 订阅是全局管理员资源，包含唯一名称、HTTPS 端点、非空事件过滤、启用标记和乐观并发版本。写入时接受 32 至 256 字节签名密钥，使用 `GATEWAY_SETTINGS_ENCRYPTION_KEY` 加密且从不返回；响应仅提供 `secretConfigured`。

端点必须为不含用户信息和 fragment 的 HTTPS URL。生产投递拒绝私网、loopback、link-local、unspecified 和 multicast 目标，并为连接固定验证后的地址；绝不跟随重定向。测试可注入本地 TLS client，但不能削弱生产校验。

禁用订阅会停止创建新投递。已有投递仍可检查和重试；修正端点或订阅后，管理员可显式重放 dead 投递。

## Envelope 与签名

请求使用 `POST application/json`：

```json
{
  "id": "event UUID",
  "type": "artifact.quarantined",
  "occurredAt": "RFC3339 timestamp",
  "data": {}
}
```

请求头包含 `X-Artifact-Gateway-Event-ID`、`X-Artifact-Gateway-Event-Type`、Unix 秒格式的 `X-Artifact-Gateway-Timestamp`，以及 `X-Artifact-Gateway-Signature: v1=<hex HMAC-SHA256>`。

签名输入为 `<timestamp>.<原始请求体>`，密钥为解密后的订阅 secret。2xx 表示完成；其他状态或传输错误使用有界指数退避重试。

每次 claim 持有唯一 fencing token 和未过期租约，以便进程失败后其他 Worker 接管。Worker 仅在发送前 claim 一条投递；提高并发时，必须保证每条已 claim 投递在租约内开始，或排队期间续租。

PostgreSQL 租约、重试、完成和重放时间均使用数据库时钟，避免节点时钟偏移错误延长或提前终止租约。

## 重试与重放

- 最多尝试 8 次；初始延迟 5 秒，最大延迟 1 小时，默认租约 30 秒。
- 响应体被忽略且不持久化；持久错误必须有界且不包含 secret 或响应体。
- 第 8 次失败后进入 `dead`。
- 显式重放只允许把 `dead` 改为 `pending`，清除次数、错误和状态，同时保留事件 ID。

## 管理接口

管理 API 提供仅管理员可用的订阅 list/create/get/CAS update 和投递 list/get/replay。投递响应包含事件身份、状态、次数、下次尝试、HTTP 状态、有界错误和时间戳，但不包含签名密钥或事件中的凭证。

Console Operations 展示订阅和最近投递，并为 dead 投递提供显式重放操作。

专用 Worker 可配置 `GATEWAY_NODE_ROLES=worker GATEWAY_WORKER_KINDS=webhook`。Webhook 是全局任务类型，不受 `GATEWAY_WORKER_FORMATS` 影响。
