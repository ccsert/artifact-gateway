# Nexus 源发现与原生仓库映射

[English](nexus-source-discovery.md) · [文档索引](README.zh-CN.md)

#381 新增仅管理员可用的 Nexus 3 只读源服务。契约源为
`api/openapi/native-hosted.yaml`，管理服务端与 Console/CLI SDK 类型统一生成。
目前没有对应 `agctl` 命令或迁移 Console 页面。此服务不复制字节、不创建仓库、不调度
任务，也不执行生产迁移或切流。

## 连接与凭据

`POST /api/v2/migration-sources` 接受 `name`、`baseUrl` 和可选
`credentials: {username, password}`。仅接受 HTTPS，拒绝用户信息、查询、片段及
有歧义的上下文路径；支持 `https://nexus.example.org/nexus` 这样的 Nexus 上下文路径。
应使用能读取所需仓库及其配置的专用只读账号。服务不会枚举源用户、哈希、API Key、
安全导出、脚本或任务端点，也不会向源发送任何写请求。

存储凭据时，`GATEWAY_SETTINGS_ENCRYPTION_KEY` 必须解码为 32 字节。AES-GCM
将密文绑定到源 UUID，PostgreSQL 仅保存密文。GET/list 回读只返回
`credentialsConfigured`，不会返回用户名、密码或密文。所有 API 节点都需要密钥；
缺少或错误密钥会明确失败，不会改用匿名读取。

`GET /migration-sources` 支持 `limit`（1–100，默认 20）和不透明 UUID 游标
`cursor`，返回 `items`、`nextCursor`。按 UUID 回读返回当前 `version`。
PUT/DELETE 必须用 `If-Match` 提交这个版本。PUT 省略 `credentials` 保留旧凭据，
替换完整对象进行轮换，或用 `clearCredentials: true` 删除。修改 `baseUrl` 时必须
明确替换或清除已有凭据。过期版本返回 412。DELETE 删除连接与密文，不修改源或目标。

所有操作要求平台管理员且没有待修改密码状态。响应带 `Cache-Control: no-store`，
审计仅保留源 UUID、操作、执行者与结果，不含源 URL、凭据或上游响应正文。审计沿用
已有管理审计接收器，接收器不可用时遵循部署的审计监控与保留策略。

## 出站保护

客户端复用网关 egress 工厂、环境代理策略、TLS 校验和 DNS 地址绑定。拒绝所有
重定向。资产 URL 必须属于配置的 HTTPS 源，并精确匹配
`/repository/<源仓库名>/<资产路径>`；用户信息、查询或片段、路径穿越和
其他源地址都会失败。等价的百分号编码仅在解码路径与安全资产 metadata 精确匹配时接受：
`%2B`/`%2b` 可以表示字面 `+`，返回 URL 根据批准的源地址和 metadata 路径重新构建。
编码的 dot 穿越和双重编码不能满足精确匹配；metadata 路径中的字面 `%`、`#`、`?`
仍不支持。发现只验证地址，不下载内容。

默认拒绝直连私网端点。操作员可配置精确主机名与私网 CIDR 白名单，例如：

```sh
GATEWAY_MIGRATION_SOURCE_PRIVATE_ENDPOINTS=nexus.internal=10.20.30.0/24,10.20.30.7=10.20.30.7/32
```

每条规则只允许该主机名以及其 RFC1918/ULA CIDR 内的地址。拒绝通配符、URL/端口
语法、公网或跨越私网边界的范围、环回、链路本地与元数据地址。仍然绑定解析后的地址，
证书必须由操作系统信任。源管理员不能通过 API 修改此进程配置。若选择环境代理，
既有策略将 DNS/网络保护委托给可信代理。此配置不会关闭 TLS，也不授权重定向或下载。

## 预检与有界发现

`POST /migration-sources/{sourceId}/preflight` 读取凭据可见的仓库及格式专有配置。
`visibility: credential_visible` 不代表完整的全局源清单；空列表不能证明没有无权
读取的仓库。预检最多检查 100 个仓库，源仓库列表本身最多 500 条。

`POST /migration-sources/{sourceId}/discovery` 接受明确的 `repositories`
（1–10 个不重复名称）、`maxPages`（1–100，默认 25）、`maxAssets`
（1–10,000，默认 10,000），对选中的受支持 Hosted 仓库穷尽 Nexus Assets API
continuation token。整个操作最多 128 次源请求、每个解码响应 2 MiB、总响应 16 MiB、
总时间 30 秒，时间包含源查询、DNS、TLS 与分页。支持与不支持的资产都占用资产预算。
不会自动重试。

支持 Raw Hosted 与 Maven RELEASE Hosted。Maven MIXED 仓库返回可辨识的 release
版本路径；SNAPSHOT 目录、共享 `maven-metadata.xml` 和无法分类的路径明确列在
`unsupportedAssets` 并附原因。Release 根据 Maven 的
`group/artifact/version/artifact-version…` 布局辨识。SNAPSHOT 专用仓库、未知格式或
类型、不安全或不可读取配置明确报告不支持。RELEASE 仓库出现 SNAPSHOT 路径会使整个
操作失败。Proxy/Group 仅预览配置和有序成员依赖，不枚举或复制其缓存、聚合资产。

成功结果为 `state: complete`，包含 `sourceVersion`、仓库、资产、不支持资产和
请求/页数/字节统计。这是对请求时可见源的有界观察，不是持久清单，也不保证源并发
发布时的一致快照。分页中断、重复或循环 token、重复路径、不可信下载地址、无效 JSON
或预算耗尽都会失败，不返回部分成功。上游 401/403、TLS 失败及超时不会变为空仓成功。
Problem 只回显有限错误码：`source_unauthorized`、`source_forbidden`、
`source_tls_failed`、`source_timeout`、`source_pagination_cycle`、
`source_download_url_denied`、`source_limit_exceeded` 等。上游失败返回 502，
超时 504，预算/可见性/SNAPSHOT 失败 422，加密或目标查询不可用 503；无效源输入
返回 400，不存在的源返回 404。

## 显式映射预览

`POST /migration-sources/{sourceId}/mapping-preview` 接受 `mappings`，每项包含
`sourceRepository`、原生 `targetName`、`format`（`raw`/`maven`）、
`type`（`hosted`/`proxy`/`group`）及可选有序 `targetMembers`。服务重新读取源配置，
验证原生名称、重复目标、已有目标仓库的兼容性、格式/类型和每个 Group 成员的显式映射。
缺失或不兼容依赖、成员乱序均无效。

源字段逐项列为 `expressible`、`manual` 或 `unsupported`：例如原生仓库名与有序
成员可以表达，存储/缓存策略需手工选择，源认证/安全配置不迁移。不会复制源凭据或权限
模型，也不回显未知字段值及上游凭据值。`ready` 只表示 Hosted 映射形状通过验证；
Proxy/Group 的 `manual` 表示仍需明确配置原生策略/凭据。`invalid` 或 `unsupported`
令 `valid: false`。预览不写源或目标业务资源，实际创建仍使用已有受支持仓库/Group 契约。

数据库迁移为只追加的 `000146_nexus_migration_sources.sql`。合成 Nexus fixture
覆盖分页、release/MIXED/SNAPSHOT、多仓、未知类型、中断/循环、恶意链接、权限、TLS、
超时、预算和操作员私网策略。真实 PostgreSQL 测试覆盖 HTTP 创建/读取/重启/更新/
清空/轮换/删除、密文、旧版本、分页与并发 CAS。CI 同样运行的 `make integration-test`
通过权威 Compose 测试命令，在全新 PostgreSQL/RustFS fixture 中执行 `internal/app`
的源 HTTP 测试及独立 `internal/nexussource/pgstore` 包。契约门禁还检查所有依赖
integration build tag 的包均包含在该命令中。这些是本机 fixture 证据，不代表真实
源迁移、远端 CI 或生产验收。

源 API 依据 Sonatype 官方的
[Repositories API](https://help.sonatype.com/en/repositories-api.html)、
[Assets API](https://help.sonatype.com/en/assets-api.html) 和
[分页说明](https://help.sonatype.com/en/pagination.html)。元数据与字节阶段分离也遵循
[ADR 0009](adr/0009-native-architecture.zh-CN.md) 中的方向。
