# 运行日志与请求关联

[English](runtime-logging.md) | [后续产品设计提案](runtime-log-product-design.zh-CN.md) | [文档索引](README.zh-CN.md)

## 可选文件输出

stdout 继续启用。将 `GATEWAY_LOG_FILE_DIRECTORY` 设为绝对且可写的部署目录，可同时保存完全相同的脱敏 NDJSON。默认不设置，不创建文件。显式启用后如文件出口不能初始化，会在打开业务资源之前使启动失败。

| 变量 | 默认值 | 可接受值 |
| --- | --- | --- |
| `GATEWAY_LOG_FILE_MAX_BYTES` | `104857600`（100 MiB） | 1–1073741824 字节 |
| `GATEWAY_LOG_FILE_MAX_BACKUPS` | `10` | 0–100 个封闭段 |
| `GATEWAY_LOG_FILE_MAX_AGE` | `168h`（7 天） | 正的 Go duration 或整数秒，最多 365 天 |

这些可配置默认值是保守起点，未经生产容量校准。每个输出器创建独立的 `gateway-*` session 目录（0700），包含 `active.ndjson` 和编号的封闭段（0600）。完整事件加入后会超过字节上限时，先轮转；单条事件本身超限则拒绝，不切碎事件。每条成功写入调用 Sync；轮转先同步并关闭旧文件，再 rename/reopen；正常退出在运行资源清理之后 flush/close。失败的部分写入会回滚，回滚失败后停止向该段写入。Sync 缩小丢失窗口，但不承诺掉电或 SIGKILL 零丢失，也不保证文件系统崩溃后目录项持久性。

备份数和期限**仅适用于本次输出 session 创建的封闭段**。写入及显式 flush/close 时清理，保留活动文件，拒绝被替换的文件，不扫描、接管或删除之前 session 或用户文件。之前 session 保留，另做保留/历史阶段；配置父目录因此没有跨重启的总大小或总期限上限。落盘不增加 Console 历史、跨 session 游标或集群检索，现有仅管理员可访问的 `scope=local` API 仍查询内存。

文件写入和 Sync 是同步操作，可能延迟日志调用方；没有队列或磁盘 I/O deadline。现有 stdout/buffer 目的地及文件都会尝试，任一失败不跳过另一个。运行中文件错误保留默认目的地、向 handler 返回错误，并通过独立 stderr 每分钟最多报告一次脱敏失败及计数；关闭错误也独立报告。失败记录不持久重试，Gateway 不因这些可选出口运行错误改变原退出码。部署负责卷权限、容量监控和旧 session 保留；只读 nonroot Kubernetes 镜像需要显式提供可写挂载，`/tmp` 不提供持久性。本批不配置卷或凭据，也不部署。

## 可选 OTLP Logs 输出

设置 `GATEWAY_OTLP_LOGS_ENDPOINT` 可将相同脱敏事件额外输出为标准 **OTLP/HTTP protobuf Logs**。默认不设置，与 trace 配置 `GATEWAY_OTLP_HTTP_ENDPOINT` 独立。文件与 OTLP 可分别或同时启用，stdout 继续启用。本批不配置接收端、日志数据库、卷或凭据。

| 变量 | 默认值 | 可接受值 |
| --- | --- | --- |
| `GATEWAY_OTLP_LOGS_ENDPOINT` | 未设置（关闭） | 无用户凭据、query 或 fragment 的 HTTP(S) URL；空路径或根路径使用 `/v1/logs`，保留自定义路径 |
| `GATEWAY_OTLP_LOGS_HEADERS` | 空 | 字符串 header 值组成的 JSON 对象；单值最多 4096 字节，传输/content header 为保留项 |
| `GATEWAY_OTLP_LOGS_QUEUE_SIZE` | `256` | 1–1024 条未确认记录，包含排队及在途记录 |
| `GATEWAY_OTLP_LOGS_TIMEOUT` | `3s` | Go duration 或整数秒，每次含重试的导出为 1 ms–30 s |
| `GATEWAY_OTLP_LOGS_SHUTDOWN_TIMEOUT` | `5s` | Go duration 或整数秒，每次 flush 与 shutdown 尝试为 1 ms–10 s |

这些可配置值是起点，未经部署容量测量。私有 Logs provider 使用官方 exporter，每批最多 64 条，通常每秒发送一次；不修改全局 tracing/logging provider，也不继承 `OTEL_EXPORTER_OTLP*` exporter 配置。共用的 `OTEL_RESOURCE_ATTRIBUTES` 元数据采用相同敏感名称脱敏策略，保留普通属性；语法或编码无效时，在 SDK 诊断之前以安全错误终止初始化。固定 Gateway service/instance/session 身份优先。HTTPS 通过私有 HTTP 客户端的系统信任验证证书；私有 CA 部署须在进程使用的系统证书配置中提供信任。没有不安全 TLS 模式，也不跟随重定向。认证 header 只进入请求 header，不进入日志字段或 fallback 诊断。

时间、级别、正文及有效 trace ID 映射为 OTLP 原生字段；普通关联字段与嵌套 group 保留为属性。缺失或无效 trace ID 保留为属性，不伪造 span ID。有符号 int64 保持精确，更宽整数或超出 float64 的数值保留为文本。OTLP 接纳单个完整 NDJSON 对象，最多 64 KiB、属性嵌套最多 32 层；拒绝超限或无效事件，不截断。这些限制不改变 stdout、文件或本地 buffer 的接纳规则。

写入成功表示接纳，**不表示交付或接收端持久存储**。接纳不等待网络交付：容量耗尽时拒绝新事件并提供安全计数，保留已接纳事件。显式协议清理期间的直接写入也立即拒绝，防止 SDK 清理锁阻塞接纳或关闭；运行出口本身已将写入与清理串行执行。官方 exporter 在导出时限内有限重试临时故障，遵守 `Retry-After` 的整数秒或 HTTP 日期；要求的等待超出预算时不重试。永久错误、无效响应和部分成功记为失败批次，部分成功保守地将整批计为未确认且不重试。非空成功响应必须使用 protobuf，允许空的序列化成功响应。失败批次释放容量，但不持久重排；后续成功不会清除之前失败的证据。退出预算耗尽时取消在途请求，将所有剩余预留保守地结算为未确认，包括 SDK 未导出便丢弃的记录。SIGKILL 和接收端中断也可留下未确认记录；没有持久重试日志或零丢失承诺。

所有已配置目的地都会尝试，一项失败不跳过其他项。OTLP 通过独立脱敏 stderr 每分钟最多诊断一次，包含导出失败、未确认、拒绝、容量拒绝、待确认及清理失败计数，排除原始接收端错误、正文和凭据。初始化失败在打开业务资源之前终止启动，运行交付或清理失败保留原退出码。退出在既有业务资源清理后先 flush、再 shutdown Logs provider，并释放其私有闲置 HTTP 连接。每次协议清理有独立预算，默认最多 5 秒 + 5 秒；这不是整个进程的退出 deadline，同步 stdout/文件 I/O 仍分别没有时限。

OTLP 是输出协议，不是日志查询 API。接收端存储、保留和检索需要部署配置及独立设计的 Gateway 查询适配器。`GET /api/v2/runtime/logs` 仍按既有管理员/改密门禁只查当前进程内存；合成接收端测试及 CI 不代替多节点、重启与出口中断部署验收。

## 共用事件与本地查询

独立 session 目录由对应输出实例独占管理，管理员及同 UID/root 工具不能并发替换其路径。身份检查会拒绝轮转或保留操作之前观察到的替换，但不是抵御有权限并发路径变更的文件系统事务。

Gateway 运行事件以每行一个 JSON 对象写入 stdout。每条记录包含 `time`、`level`、`msg`、`instanceId`、`sessionId`、`component`、`operation`、`requestId` 和 `traceId`。没有请求上下文的进程事件，其关联 ID 为空。HTTP 访问事件另含方法、路由模板、请求类别、状态码和毫秒耗时。访问日志不会记录原始 URL 路径、查询串、请求体、`Authorization` 或 `Cookie`。名称涉及密钥、凭据、token、请求体、URL、查询串或错误的属性在输出前遮蔽。

运行日志具有显式的输出生命周期：写入、flush 和 close 串行执行；即使 flush 失败，close 仍会尝试两个清理回调各一次。默认 stdout 与内存 buffer 为借用输出，close 为 no-op，不等待正在写入的记录，也不会 flush、sync 或关闭 stdout。配置完成后的运行错误和优雅退出先完成既有 HTTP 与资源清理，再关闭日志输出。Worker 仍通过 context 取消；后续拥有资源的输出需要明确各自的清理时限和交付行为。

每个 HTTP 响应都带 `X-Request-ID` 和 `X-Trace-ID`。安全的客户端请求 ID 可以沿用；缺失或不安全的 ID 由服务端生成。请求期间写入的审计记录（包括管理操作）使用同一组 ID。审计行仍留在 PostgreSQL，承担操作证据；运行 JSON 不写入业务表。生命周期任务的领取、完成和失败事件使用任务 ID 作为 `requestId`；Webhook 投递尝试使用投递 ID。运维人员可从任务或投递记录定位对应 worker 事件。

默认 `GATEWAY_ACCESS_LOG=limited`：只记录 5xx 和超过 `GATEWAY_ACCESS_LOG_SLOW_MS`（默认 `1000` 毫秒）的请求。设为 `GATEWAY_ACCESS_LOG=full` 才记录全部请求。慢请求阈值必须是 1 到 60000 的整数。此策略限制运行日志量，不影响审计记录。

管理界面的“系统运行 → 运行日志”通过 `GET /api/v2/runtime/logs` 查询当前 Gateway 进程的内存缓冲区。默认保留最近 1000 行，可用 `GATEWAY_LOG_BUFFER_LINES` 设为 0 至 5000；0 禁用查询，API 返回 503。仅平台管理员可以访问。查询支持最多 24 小时的时间窗、最多 100 条一页、时间、实例、级别、组件、Request ID、Trace ID 和关键词筛选；请求超时为 2 秒。审计日志详情的关联 ID 可跳转到此页。响应明确标注 `scope=local`、`instanceId` 和 `sessionId`。请求其他实例时返回 503，不把本进程结果冒充集群结果。进程重启后缓冲区消失。文件落盘和协议输出不改变此端点；文件历史与跨节点查询需要独立设计的读取器或检索适配器，见[输出与历史设计](runtime-log-product-design.zh-CN.md)。

仓库自带的 Compose 部署使用 Docker `json-file` 日志驱动；`GATEWAY_LOG_MAX_SIZE` 默认为 `10m`，`GATEWAY_LOG_MAX_FILES` 默认为 `5`。这些限制按容器生效，重建容器可能移除本地历史。集群部署应从每个 API、scheduler 和 worker Pod 收集 stdout/stderr，在 Gateway 外配置采集器的持久存储与保留策略，并保留可检索的 `instanceId`、`sessionId`、`requestId`、`traceId` 字段；不应在 Loki 等以标签建索引的后端将这些高基数字段直接作为索引标签。仅靠 Kubernetes 节点日志轮转，无法保证 Pod 更换后仍可查询。采集器凭据不能进入浏览器或日志字段。验收时应在 Pod 重启后用同一请求 ID 查询，并从另一节点查询 worker 事件。
