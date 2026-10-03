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

Console 默认展示关键词、时间范围、级别、精确组件和 Request ID，节点和 Trace ID 放在“更多筛选”。最近 15 分钟、1 小时、24 小时使用滚动时长；`windowSeconds` 接受 1–86400 秒，与 `from`、`to` 互斥，省略时间边界仍默认最近一小时。每次查询和跟随都由同一个加锁快照计算两个滚动边界，长时间停留或暂停恢复不会把跨度越滚越长。游标绑定时长；时间范围或其他筛选变化时清除游标、暂停跟随并获取新快照。

自定义边界为固定快照。Console 将精度统一到选择器可见的秒，在提交时校验顺序、24 小时跨度和未来 5 分钟上限，再序列化为 UTC ISO 日期。跟随新事件需选择最近范围；固定快照不会声称包含结束时间之后的记录。客户端校验不替代服务端限制，非法 API 窗口仍返回 `invalid_time_window` 且没有记录或游标，Console 显示恢复提示并保留诊断错误码。复制和导出仍只包含已加载结果。

### 安全诊断字段投影

本地 API 只返回已经脱敏的核心字段及有界顶层白名单：HTTP `status`（100–599）、
`durationMs`（0–86400000）、标准 HTTP `method`、已知 `requestClass`、最多 128
字节的 ASCII 不透明 `jobId`，以及 `attempt`（0–1000000）。缺失、类型错误、越界、
已遮蔽或嵌套的诊断值不返回，但不丢弃整条事件。不开放原始属性、路由/URL、header、
请求体或错误 payload。敏感名称及祖先 group 脱敏先于 stdout、buffer 搜索和本次投影。

每个成功页面包含 `source`：INFO 采集下限、实际 limited/full 访问日志策略与慢请求
阈值、buffer 容量、16 KiB 行接纳上限，以及当前实例/session 保留记录中观察到的最多
100 个准确组件名；`componentsTruncated` 表示列表被限长。名称是观察结果，不代表
通配符筛选或跨节点 worker 聚合。筛选结果为空不能推断事件没有输出的原因；选择
DEBUG 不会开启 DEBUG 采集。关闭 buffer 返回 `log_buffer_unavailable`（503），缺失
运行身份返回 `log_source_identity_unavailable`（503）。管理员与强制改密门禁先于
来源可用性或游标校验，响应使用 `Cache-Control: no-store`。

### 绑定会话的增量查询

默认查询及 `beforeSequence` 仍按序号倒序返回历史。初始/历史页面提供绑定加锁快照
尾部的 `afterCursor`，用于跟随该快照之后的事件。跟随请求发送 `afterCursor`，按
序号正序收到记录；`hasMore` 为 true 时必须继续使用返回游标。满页只推进已经扫描
的位置，不会在还有匹配未读页面时跳到最新序号；短页也消费扫描到的不匹配位置。
快照之后的新记录留待下一次读取。省略的时间边界在每次快照滚动为最近一小时；
显式边界保持固定，并继续遵守最多 24 小时的查询范围。取得读锁后的快照会再次
验证顺序及范围；若等待写入使滚动边界无效，返回 `400 invalid_time_window`，
不返回记录或游标。
OpenAPI 的 Problem code 枚举包含日志查询的参数验证、缓冲禁用、来源标识、
远程范围、会话变化、超时及必须改密错误，客户端可区分这些已声明响应。

不透明游标使用 buffer 的临时密钥签名，绑定实例、session 及筛选条件。连续跟随时
保留相同筛选，可调整页大小；筛选变化、畸形或修改过的游标返回 `invalid_cursor`
（400）。不能同时发送 `beforeSequence` 与 `afterCursor`。其他实例或重启前 session
的游标返回 `log_cursor_scope_changed`（409），需要重新查询；显式请求其他实例仍
返回 503。不要用更早历史页的尾游标替换正在使用的跟随游标。

`retention.earliestSequence` 与 `latestSequence` 描述整个内存 ring 的保留范围，
不随筛选变化，空缓冲时都为 0。只有前向读取前未读位置已被覆盖时，
`retention.gap` 才为 true；它不宣称被覆盖事件匹配筛选。返回记录因时间、级别或
组件筛选产生的序号间隔不是丢失；重启/会话变化另以游标范围错误报告。两秒预算也
约束等待竞争写锁。这些游标不增加文件历史、持久检索、其他节点、stdout 读取器、
worker 聚合或零丢失交付承诺。

仓库自带的 Compose 部署使用 Docker `json-file` 日志驱动；`GATEWAY_LOG_MAX_SIZE` 默认为 `10m`，`GATEWAY_LOG_MAX_FILES` 默认为 `5`。这些限制按容器生效，重建容器可能移除本地历史。集群部署应从每个 API、scheduler 和 worker Pod 收集 stdout/stderr，在 Gateway 外配置采集器的持久存储与保留策略，并保留可检索的 `instanceId`、`sessionId`、`requestId`、`traceId` 字段；不应在 Loki 等以标签建索引的后端将这些高基数字段直接作为索引标签。仅靠 Kubernetes 节点日志轮转，无法保证 Pod 更换后仍可查询。采集器凭据不能进入浏览器或日志字段。验收时应在 Pod 重启后用同一请求 ID 查询，并从另一节点查询 worker 事件。

### Console 阅读、跟随与有限导出

系统运行 → 运行日志以等宽纯文本逐行显示时间、级别文字、进程/session、准确
component/operation、消息及白名单请求/任务详情。深浅主题都保留级别文字；关闭
折行后，可在消息字段内横向查看。ANSI、控制和双向字符转成可见转义文本，不执行
HTML。每个显示字段最多 4096 字符，缩短处用省略号标明。

查询同时应用时间、实例、级别、精确组件与关联筛选；清除筛选也清除审计链接的
Request/Trace 条件。组件建议来自 API 观察名称，不是通配分组，`worker` 不查询
全部 worker。来源行显示实际 INFO 下限、limited/full 策略、慢请求阈值、ring
容量及保留序号。DEBUG 筛选不会开启 DEBUG；空结果不能判定没有输出的原因。
其他进程、文件及 OTLP 历史仍不在此视图范围。

跟随新日志从快照增量游标开始；加载更早日志保留独立的跟随锚点。请求串行执行，
通常间隔三秒；未读页最多每秒读取一次，每次最多 100 条，浏览器请求预算为五秒。
暂停保留已消费游标，隐藏标签页暂停轮询；恢复后从该游标继续。新匹配事件可能已被
服务器 ring 覆盖，页面以服务器保留警告说明。离底阅读保留滚动位置，未读数只计
已读取的新唯一记录；回到底部滚动至最新已加载记录并继续跟随，不估算尚未读取的
事件数量。

筛选变化重新加载快照及游标。实例/session 变化清空旧记录并要求新快照；普通刷新
失败保留已加载内容并显示错误。身份/必须改密失败清除受保护记录。重启不混入旧
session 数据。

客户端只保留最新连续尾部，最多 300 条唯一记录及 1,048,576 UTF-8 字节的投影
NDJSON（含行分隔符）。客户端容量移除有独立提示，不宣称服务器丢失；达到客户端
容量边界后停止加载更早历史。单行复制使用安全 JSON 投影；复制已加载和下载
NDJSON 仅导出当前筛选已加载记录，使用相同条数/字节上限，不读取全部历史，也不
导出未知响应属性。复制选择完整保留最多 1 MiB UTF-8 的可见选择文本；超限明确
拒绝并提示缩小选择，不静默截断。显示缩短和客户端容量移除不能恢复界限之外的
记录。
替换快照、筛选/session 变化及授权清空也使选择失效；复制时复核当前原生选择仍在
日志流内，过期选择不能导出旧记录。
