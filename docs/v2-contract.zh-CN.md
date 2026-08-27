# ADR：V2 匿名读取、Raw 与 Conan 2 契约

[English](v2-contract.md) | [文档索引](README.zh-CN.md)

状态：历史 V2 契约，用于迁移和兼容背景。当前协议清单由[协议兼容性](protocol-compatibility.zh-CN.md)、OpenAPI overlay 和可执行测试共同拥有；如本文路由陈述过期，以这些来源为准。

## 术语与路由

| 术语 | 含义 |
| --- | --- |
| Repository | 一个可寻址格式端点与策略边界，不是存储 bucket |
| Hosted | 原生 PostgreSQL/Object Store 仓库 |
| Proxy | 从一个明确允许的外部上游获取的 member |
| Group | 同格式成员的有序只读虚拟仓库 |

名称使用小写 DNS label 与 `-`，在格式内唯一。OCI `/v2/<group>/...`、Maven `/maven/<group>/...`、Raw `/raw/<group>/<path>`、Conan `/conan/v2/<group>/...` 彼此不交叉。

`/api/v1`、`/metrics`、`/livez`、`/readyz`、`/api/v1/operations` 等静态路由优先。`api`、`metrics`、`livez`、`readyz`、`operations`、`v2`、`maven`、`raw`、`conan` 为保留名称。

匿名读取要求全局、Group、最终 Repository/member 的门禁全部允许。授权在缓存或上游访问前执行；Group 不能通过后续成员绕过高优先级策略。所有策略判定进入有界审计与指标。

## 读取访问与审计

认证建立 principal，Repository Grant 与兼容静态策略决定读取。未认证且门禁关闭时返回该协议现有 challenge，开启时只允许 GET/HEAD；写入和管理始终认证。

Audit 记录 actor、group/repository/member、format、outcome、authorization source/reason 和时间。匿名 actor 固定为 `anonymous`。敏感路径、Token、上游凭证不得写入指标 label 或错误正文。

## Raw 协议

Raw 路由接受编码后最多 4096 字节的规范多段路径，单次 decode 后拒绝空段、`.`、`..`、反斜杠、控制/双向格式化字符和任何编码形式的分隔符绕过。Hosted 提供认证 PUT/DELETE 与 GET/HEAD；path 是可原子替换的可变引用，`path + digest` 是不可变快照身份。Proxy/Group 提供 GET/HEAD；文件与 checksum 响应使用 `Content-Disposition` 建议可读文件名。

响应支持稳定 ETag、`Digest`、Content-Type、条件请求与单 byte range。校验和 sidecar 从已验证主对象派生，不信任客户端 sidecar。缺失对象可负缓存，但授权拒绝、上游 401/403/429/5xx、传输错误或校验失败不得负缓存。

Proxy 只能访问 HTTPS 且 host 在 Repository allowlist 的上游；重定向重新验证 scheme/host/address。Direct 模式对已验证 DNS 结果 pinning，防止 rebinding。凭证不转发给非目标主机。

大对象 cold miss 使用有界临时 staging 与流式 SHA-256、对象发布和响应，不把完整对象保存在 Go `[]byte`。上游 `HEAD` 必须真实发送 HEAD，不下载 body。每请求 single-flight lock 可续租；每 Gateway staging 并发满时在访问上游前返回 `503`、`Retry-After: 1` 并增加有界指标。

Raw/OCI resumable upload 把每个 offset range 保存为不可变 chunk，PATCH 不重读/重写历史。完成时验证连续性并只组装一次，再原子发布；升级期间仍可读取旧 cumulative prefix 完成。持久回收清理完成、取消、过期 session 的遗留 chunk，保留 PostgreSQL 轨迹。

## Conan 2 协议

`CONTRACT: conan2-only`：仅支持 Conan 2.x v2 REST resolution，包括原生 Hosted metadata/file。Conan 1、remote-to-remote copy、上游索引聚合和服务端 recipe 生成不在范围。

Remote URL 必须包含 Group：`/conan/v2/<group>/conans/...`。唯一例外是 Basic login handshake `GET /conan/<group>/v2/users/authenticate`，它服从同一资源策略且不是通用 User API。Conan 1 auth 和非 GET auth 返回 `404`；Proxy 不接收客户端凭证。

Recipe 坐标为 `name/version/user/channel#rrev`，package revision 增加 `/package_id#prev`。每段独立存储，只严格 decode 一次；revision 一旦观察即不可变。省略 rrev/prev 只在 metadata TTL 内缓存选择结果，不创建永久 alias。

支持 revision list、recipe search handshake、recipe files、package revision list/latest、package files 及各文件 GET/HEAD。每个字段恰占一个 path segment，拒绝空值、dot segment、斜杠、反斜杠、NUL、编码 slash 和 raw/encoded `#`。坐标绝不重建为 `#` 分隔路径。

Recipe/package manifest 与文件必须按 Conan metadata checksum 验证后才能缓存；不匹配返回 `502`、审计 `upstream_error` 且不缓存。Hosted-first 解析，metadata 默认 TTL 1 分钟、body 15 分钟、terminal 404 负缓存 1 分钟；其他错误不负缓存。Key 包含完整坐标、representation、member 和 endpoint，quota 计入 Group。

Conan Proxy 使用与 Raw 相同的 HTTPS、allowlist、redirect 与地址限制，但 allowlist 按 Repository 且独立于 OCI/Maven。

## 配置、迁移与运维

V2 以增量字段为 Group/member 增加 `anonymous`，Proxy member 单独持久化 `allowed_hosts`。Group 拥有 `cache.quota_bytes`，member 拥有 allowlist。迁移仅向前、事务化，不原地重写 OCI/Maven 行。

Legacy OCI/Maven query 列保持可读，新策略列默认 false，保留默认拒绝。V2 Audit 字段为 nullable 增量，V1 响应不变。回滚是应用回滚加前向补偿迁移，不执行破坏性 down migration。

关键设置：匿名默认 false；allowlist 非空；artifact TTL 15m、metadata/negative TTL 1m；Group quota 为正且不删除其他 Group 缓存；每响应 object 上限为正；Raw staging 默认每 Gateway 4 个并发。

Metric 只使用固定 format/operation/outcome/cache/member type、HTTP class/status family 和固定 PostgreSQL pool/state/reason。禁止 path、coordinate、actor、upstream URL、checksum、Repository name 等高基数 label。

## Adapter 边界与兼容矩阵

Format Adapter 拥有路由解析、响应形状、条件/Range、坐标校验和缓存键；Resolver 拥有 Group 顺序、策略调用、审计、有界指标和 Proxy 顺序。Native Hosted 直接使用 PostgreSQL 元数据和已验证对象字节，不通过外部 upstream client。

Raw 黑盒必须覆盖规范/拒绝路径、GET/HEAD/Range/checksum/content type、缓存命中、负缓存、allowlist、匿名门禁和审计。Conan 夹具必须使用 Conan 2.x 解析带 revision 的 recipe/package，并覆盖 checksum failure、缓存、allowlist、匿名和审计。

升级测试必须在已有 OCI/Maven 数据上应用增量迁移，证明旧 endpoint 与 Audit 可读，并证明回滚应用二进制不依赖 V2 行。
