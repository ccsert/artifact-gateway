# 运行日志与请求关联

[English](runtime-logging.md) | [文档索引](README.zh-CN.md)

Gateway 运行事件以每行一个 JSON 对象写入 stdout。每条记录包含 `time`、`level`、`msg`、`instanceId`、`sessionId`、`component`、`operation`、`requestId` 和 `traceId`。没有请求上下文的进程事件，其关联 ID 为空。HTTP 访问事件另含方法、路由模板、请求类别、状态码和毫秒耗时。访问日志不会记录原始 URL 路径、查询串、请求体、`Authorization` 或 `Cookie`。名称涉及密钥、凭据、token、请求体、URL、查询串或错误的属性在输出前遮蔽。

每个 HTTP 响应都带 `X-Request-ID` 和 `X-Trace-ID`。安全的客户端请求 ID 可以沿用；缺失或不安全的 ID 由服务端生成。请求期间写入的审计记录（包括管理操作）使用同一组 ID。审计行仍留在 PostgreSQL，承担操作证据；运行 JSON 不写入业务表。生命周期任务的领取、完成和失败事件使用任务 ID 作为 `requestId`；Webhook 投递尝试使用投递 ID。运维人员可从任务或投递记录定位对应 worker 事件。

默认 `GATEWAY_ACCESS_LOG=limited`：只记录 5xx 和超过 `GATEWAY_ACCESS_LOG_SLOW_MS`（默认 `1000` 毫秒）的请求。设为 `GATEWAY_ACCESS_LOG=full` 才记录全部请求。慢请求阈值必须是 1 到 60000 的整数。此策略限制运行日志量，不影响审计记录。

管理界面的“系统运行 → 运行日志”通过 `GET /api/v2/runtime/logs` 查询当前 Gateway 进程的内存缓冲区。默认保留最近 1000 行，可用 `GATEWAY_LOG_BUFFER_LINES` 设为 0 至 5000；0 禁用查询，API 返回 503。仅平台管理员可以访问。查询支持最多 24 小时的时间窗、最多 100 条一页、时间、实例、级别、组件、Request ID、Trace ID 和关键词筛选；请求超时为 2 秒。审计日志详情的关联 ID 可跳转到此页。响应明确标注 `scope=local`、`instanceId` 和 `sessionId`。请求其他实例时返回 503，不把本进程结果冒充集群结果。进程重启后缓冲区消失；跨节点和历史查询仍需外部持久采集后端。

仓库自带的 Compose 部署使用 Docker `json-file` 日志驱动；`GATEWAY_LOG_MAX_SIZE` 默认为 `10m`，`GATEWAY_LOG_MAX_FILES` 默认为 `5`。这些限制按容器生效，重建容器可能移除本地历史。集群部署应从每个 API、scheduler 和 worker Pod 收集 stdout/stderr，在 Gateway 外配置采集器的持久存储与保留策略，并索引 `instanceId`、`sessionId`、`requestId`、`traceId`。仅靠 Kubernetes 节点日志轮转，无法保证 Pod 更换后仍可查询。采集器凭据不能进入浏览器或日志字段。验收时应在 Pod 重启后用同一请求 ID 查询，并从另一节点查询 worker 事件。
