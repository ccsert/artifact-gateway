# Nexus 源发现与原生仓库映射

[English](nexus-source-discovery.md) · [文档索引](README.zh-CN.md)

#381 新增仅管理员可用的 Nexus 3 只读源服务。契约源为
`api/openapi/native-hosted.yaml`，管理服务端与 Console/CLI SDK 类型统一生成。
目前没有对应 `agctl` 命令；Console 源工作流见下文。此服务不复制字节、不创建仓库、不调度
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

默认 Compose 部署会将环境文件中的此项传入 Gateway 及继承它的后台角色；
省略该配置时，白名单保持为空。

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

支持 Raw、Maven RELEASE、OCI（源格式为 `docker`）与 Conan Hosted。Maven MIXED 仓库返回可辨识的 release
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
`sourceRepository`、原生 `targetName`、`format`（`raw`/`maven`/`oci`/`conan`）、
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

## Console 源工作流

打开 **运维 → 迁移源**，或使用
`/operations?tab=migrations&source=<源 UUID>` 直达已保存的连接。
页面、深链及 API 均要求平台管理员。Console 使用生成的管理客户端完成源分页列表/
详情、创建/更新/删除、预检、发现和映射预览。

凭据是仅写表单输入：保留时省略字段，替换时提交新凭据对，清除时提交
`clearCredentials: true`。每次提交（包括失败）、关闭编辑器或切出页签后清空输入。
凭据不进入浏览器存储、URL、查询键或查询/变更缓存。更新携带 `If-Match` 连接版本；
冲突时要求先读取最新版本，不静默覆盖。错误文案由有限错误码选择，不回显传输或上游
响应正文。

预检明确标注凭据可见范围。最多选择十个仓库，逐个显式填写原生目标名。只有选中的
受支持 Hosted 仓库进入资产发现；Proxy/Group 仅预览配置。Group 目标成员遵循源声明
的顺序，且必须逐个显式映射。发现结果区分受支持资产和未支持路径，资产大小合计明确
标为源元数据，不当作已传输或已校验字节。映射结果区分形状验证、手动配置、未支持字段
和冲突。受支持 Hosted 映射可继续进入[批次表单](nexus-migration-console.zh-CN.md)，
显式确认已有目标 ID 和更新的容量证据后才会创建持久批次。

刷新失败保留上次成功观察并显示错误。切换源、连接版本、选定仓库或目标输入会使依赖
它们的观察失效；迟到响应不会替换另一源的结果。重新打开页面会读取已保存连接，
不会虚构持久发现快照。源视图不复制字节；独立批次视图负责持久执行和如实记录验收
观察，target-only 冷客户端证据在针对该批次取得之前保持未验证。

状态测试覆盖首次/刷新失败、旧数据保留、分页、源/版本竞争、有序映射和凭据生命周期。
真实路由 Playwright 布局测试覆盖 1440px 和 390px、双语/主题、16/24px 有界间距、
列布局、长路径、溢出及普通用户深链拒绝。
`console/e2e/migration-sources-live.spec.ts` 另通过真实 Gateway/PostgreSQL 和
`console/e2e/support/nexusSourceFixture.mjs` 验证创建/回读、预检、多仓发现/预览、
页面重开、凭据轮换/清除、读取拒绝与删除。对隔离 Gateway 设置
`GATEWAY_ADMIN_TOKEN`、`GATEWAY_RESOLVER_TOKEN` 和
`MIGRATION_SOURCE_FIXTURE_URL` 后运行。TLS fixture 需要 Gateway 信任的测试证书及
操作员明确设置的主机名/私网地址白名单；没有绕过 TLS 或私网保护。此 fixture 门禁
不证明真实 Nexus 库存完整、字节迁移、远端 CI 或生产就绪。

源 API 依据 Sonatype 官方的
[Repositories API](https://help.sonatype.com/en/repositories-api.html)、
[Assets API](https://help.sonatype.com/en/assets-api.html) 和
[分页说明](https://help.sonatype.com/en/pagination.html)。元数据与字节阶段分离也遵循
[ADR 0009](adr/0009-native-architecture.zh-CN.md) 中的方向。

源 storage path 与实际下载路由按格式严格区分：Raw/Maven 为原始资产路径，OCI 为
受限的 `v2/<name>/manifests|blobs/<reference>`，Conan 为
`v2/conans/.../files/<file>`。未知形状明确不支持，不跟随任意资产 URL；持久批次规划
另行读取必要的图与 manifest 正文，源发现本身只验证元数据。
