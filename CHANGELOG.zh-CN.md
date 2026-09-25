# 变更记录

[English](CHANGELOG.md) | [文档索引](docs/README.zh-CN.md)

本文件记录 Artifact Gateway 的用户可见变化。项目遵循语义化版本；1.0 之前已经是可用
分发版本，但契约仍可能演进。所有变化先归入 `Unreleased`，发布时移动到带日期的版本
标题，且不改写其含义。

## Unreleased

- 定时任务现在跟随它所作用的仓库。仓库保留任务由该仓库的管理员或平台管理员创建、读取、更新、手动派发与删除；目录只列出调用方自己能管理的任务；把任务从一个仓库改到另一个仓库，需要同时具备"离开"和"到达"两侧的权限。没有目标仓库的任务（审计保留）仍属平台操作。

- 两项仓库管理操作改归**该仓库的管理员**，而不再只属于平台管理员。测试仓库的 egress 代理、以及把授权模板应用到某仓库的 grants，二者都恰好作用于一个仓库，因此在该仓库上持有 `repositories:admin` 的 `member` 可以执行；同一调用方在任何其他仓库上都会被拒绝，并留下审计记录。授权模板目录、仓库创建以及其余平台面仍要求 `admin` 等级。同一轮里 `GET /formats` 去掉了它的管理员要求，改为契约本就写明的"已认证可读"：该格式表不携带任何仓库状态。剩余的平台/仓库两档拆分留作后续工作。

- Hosted 群组现在跟随它所引用的仓库。创建群组、整体替换、变更成员与删除群组，现在允许**本次变更所触及的每个仓库的管理员**执行——包括被保留或移除的成员，以及新增的成员——平台管理员同样允许。拒绝时会指明是哪个成员越界，并写入一条管理审计。未绑定仓库的成员归平台档，因为没有任何信息能把它归到某个仓库。该结论不做缓存：移除成员、删除仓库或收回授权后，下一次请求该群组就不再属于该仓库管理员。群组浏览不受影响。

- 跨仓库管理视图现在与其描述的操作同一档。仓库管理员现在只能看到自己管理的仓库的授权、容量快照与生命周期作业，其他仓库不贡献任何行，因而与"该仓库不存在"不可区分；平台管理员仍看到全部。**审计日志有意保持不变**：它跨越所有仓库与凭据，而"按仓库范围向仓库管理员开放审计"是需要单独决策的事。

- 所有 `401` 现在都带 `WWW-Authenticate: Bearer` challenge，缺凭据被拒的调用方可以据此得知应使用的认证方式，而不再需要猜测。协议处理器保留各自的 challenge 且不会被覆盖：OCI 与 Raw 发布协议 realm，Conan 发布 `Basic`。

- effective-access 接口不再向对该仓库无权限的调用方泄露仓库是否存在。此前它对任何已知标识符都返回完整的权限矩阵，使任何已认证调用方都能确认仓库存在并读取其权限构成。现在它只对调用者在所请求资源上具备读或 intelligence 权限的仓库作答，其他情况一律返回与"仓库不存在"完全一致的响应。intelligence 权限也计入，因为扫描凭据具备 intelligence 但无读权限；管理员不受影响，仍可评估任意仓库。该接口的 OpenAPI 描述、匿名访问文档与 Nexus 差距文档已同步更新，替换了"不支持模拟"与"仅管理员可用"这两处均不成立的表述。

- 未配置任何 reader 模式的部署现在会拒绝未匹配的已认证调用方，而不再放行。在 0.3.1 及更早版本中，只要 `GATEWAY_REPOSITORY_READERS` 为空，任何已认证调用方都能静默读取旧仓库，且不经过任何已配置策略；该回退已移除，拒绝在 legacy Maven、OCI、Raw 与 Conan 路径上表现为普通的 `403`。`GATEWAY_LEGACY_READ_DEFAULT=allow` 可恢复旧姿态并在设置期间打印启动告警，以便分阶段升级，但它是迁移辅助手段而非应长期保留的姿态：盘点没有授权却读取的主体，为每个主体在 `GATEWAY_REPOSITORY_READERS` 中配置精确仓库名或 `prefix/*` 模式（或改用按仓库授权），然后取消该变量。该机制没有通配一切的模式，因此每个主体需要的仓库都必须逐一列出；无法识别的取值按拒绝处理。管理员、已配置的 reader 模式、仓库自身的授权、匿名访问，以及待审批与待改密拦截均不受影响。升级与回滚步骤见旧 Group 迁移指南。

- 既有的 `reader` 与 `writer` 账号在升级时被转换为 `member` 等级，并在**升级时已存在**的每个仓库上获得等价的按仓库授权，因此在原有仓库上无人失去或获得访问权。升级之后新建的仓库**刻意不被覆盖**：对新仓库的访问是显式授权，而不是旧角色继承来的副作用。该迁移不把任何 grant set 标记为托管，因此各仓库继续按原样服务于其 legacy 静态读写主体；同时 `member` 在数据库层面已被接受为 OIDC JIT 默认角色。

- 命名了某主体的 grant set 现在对该主体直接生效，即使该集合未被标记为托管。此前显式的单主体授权在管理员整体替换该仓库授权集合之前会被忽略，导致精准授权静默失效；未被该集合命名的主体仍走 legacy 静态策略，因此不会放宽任何访问。

- 新增 `member`（成员）账号角色，其本身不附带任何仓库能力。把 SSO 账户批准为 `member`、或创建 `member` 的本地账户，都不会带来任何仓库访问权；其可访问范围完全来自按仓库授权，因此一次角色分配不再可能把全部仓库交给某个用户。该角色对用户、API 密钥、OIDC 角色映射与 OIDC JIT 默认角色均可用，在 Console 的角色选择器中排在首位并带明确说明，有效访问解释器也会标注它。它所取代的 `reader` 与 `writer` 角色在同一版本被移除，因此不再有任何部署保留"覆盖全部仓库"的全局角色。

- `reader` 与 `writer` 账号等级已被移除。给用户或 API 密钥分配这两个值，现在在创建与更新时都会返回 `400`，而不是被静默降级；Console 的角色选择器、用户列表筛选与 effective-access 模拟只提供 `none`、`member`、`admin`；OIDC 角色映射设置从 reader 列表与 writer 列表合并为一份 member 列表，因为映射的作用已从"授予仓库能力"变为"审批账号"；`users.role` 与 `api_keys.roles` 现在带数据库约束，因此数据库也会拒绝这两个被移除的取值。原先列在任一旧映射列表中的外部 realm 角色仍保持映射，现在映射为 `member`。仍命名了被移除等级的账号与 API 密钥在升级时被收敛为 `member`——即它们原本就已解析到的等级，因为未被识别的等级本来不授予任何权限——同时，若某个密钥另外还命名了 `admin` 则保留 `admin`，完全没有等级的密钥仍保持无等级。OIDC JIT 默认角色的默认值从 `reader` 变为 `member`，已存储的 `reader`/`writer` 默认值按同样方式迁移。由于该迁移替换（而非保留）了 reader 与 writer 两列设置，本版本之后的回滚需要恢复升级前的数据库并换回匹配的二进制，而不能只切换镜像。

- 仓库配置变更与删除现在要求仓库 `admin` 作用域，而不再是 `write`。修改仓库的 endpoint、允许主机、出口代理、上游凭据、匿名读与严格发布设置，以及停用整个仓库，都会改变该仓库对所有客户端的行为，因此它们现在与授权、保留、安全准入、生命周期操作同属一档，而不再与"发布/删除制品"同档。仅持 `write` 作用域的主体会被拒绝并返回 `403`。Console 中这些操作的界面此前已是管理员专属。

- 必须改密的账号不再能触达仓库面。该拦截在授权器内部执行，因此管理路由、制品浏览、全局搜索、effective-access、发布会话、协议读写与 Group 成员解析口径一致，而不再只有管理员门禁的路由生效。受影响的请求返回 `403 password_change_required` 而非通用拒绝；改密本身仍可访问，因为它走独立的认证路径。legacy 静态读写策略路径也应用同一拦截，因此宽松的 legacy 配置无法放行此类账号。

- 仅管理员可用的管理端点现在对“已认证但非管理员”的调用返回 `403 access_denied`，而不是 `401`。缺少或无效凭据仍返回 `401`，必须改密的会话返回 `403 password_change_required`。客户端现在能区分“需要重新登录”与“已登录但无权限”，此前两者无法分辨。

- 授权变更现在会被审计。替换仓库授权、创建/更新/删除授权角色或授权模板、以及把模板应用到仓库，都会写入一条管理审计记录，包含操作者身份与被变更的资源。此前这些变更不留任何痕迹，导致 `SECURITY.md` 要求运维审查的权限变更从未被记录；被拒绝或版本冲突的请求仍不会被记录为变更。

## 0.3.1 - 2026-09-24

- 已授权的 `reader` 和 `writer` 现在可在 Console 查看有读取权限的仓库与制品。`writer` 可按仓库有效写入权限使用 Raw 上传、Maven 发布向导，以及 OCI、npm、PyPI 发布指引；OCI 的 Docker/Podman 发布使用管理员签发并绑定仓库写入授权的服务账号凭据。`reader` 不显示上传与发布入口。仓库列表在服务端逐项按读取权限筛选，待授权用户仍不可访问；仓库配置、访问授权和用户管理继续只对管理员开放。

## 0.3.0 - 2026-09-24

- OIDC 首次登录自动建用户现可选择 `none` 待授权角色。首次登录会创建无本地密码的用户及稳定的 issuer/subject 身份绑定，在 Console“用户”页面显示为“待授权”；即使旧的默认读取规则或仓库授权记录存在，也不会获得仓库权限。管理员可在此分配或撤销全局只读、读写和管理角色；待授权用户看到等待页面并可检查授权状态。后续登录会同步安全的 SSO 资料，不覆盖 Gateway 中已分配的角色；停用账户或撤销会话在下一请求生效。为兼容已有部署，JIT 默认角色仍是 reader，启用这一开通策略时需明确选择 `none`。

## 0.2.0 - 2026-09-24

- 将间接依赖 gRPC 更新至 v1.83.1，修复 GO-2026-6348。

- 新增制品生命周期下载统计。经由 Gateway 解析的每次成功内容下载——Hosted、Proxy 或 Group，覆盖 Maven、OCI、Raw、Conan、npm、PyPI、Go 与 APT——都会在唯一审计写入点折叠进耐久的按制品聚合（`artifact_usage_stats`），计数在制品整个生命周期内持续累计且不受审计日志保留期影响：下载次数、累计流量、首末下载时间与最近使用者，按客户端解析的制品地址记账（Group 下载计入 Group 地址）。管理 API 提供 `GET /api/v2/repositories/{repositoryId}/artifact-usage`，每个仓库的 Console 新增“使用统计”Tab，展示生命周期累计与逐制品明细。HEAD 探测、304 重验证、发布与管理操作不计入。仓库清理策略现在消费同一聚合作为清理依据：dry-run JSON 与 CSV 导出会附带每个候选的下载次数与最近下载时间；新增 `keepDownloadedDays` 策略字段（默认 0，保持现有行为），窗口内被下载过的清理单位无论命中期限还是版本数原因都豁免本轮清理，npm、PyPI、Go、Maven、Raw、OCI manifest 拉取与 Conan v2 revision 均有按格式匹配。


- 新增 Go Proxy 校验和数据库镜像，关闭最后一个已知 Go 兼容缺口。在 egress 允许列表中列出校验和数据库主机的 Go Proxy 仓库，现在可以应答 go 命令的 `/go/<repository>/sumdb/<name>/supported` 探测，并转发 `/latest`、`/lookup/` 与 `/tile/` 请求。签名字节、状态码与内容类型原样透传且永不缓存；不附带上游凭据；镜像请求遵循仓库读取策略，因此 go 命令可以针对只能经由 Gateway 到达的校验和数据库验证模块下载。原生 Go E2E 门禁现在会证明真实 go 客户端能通过镜像完成模块校验。

- 新增 npm Registry 身份端点。`GET /npm/<repository>/-/whoami`（Hosted、Proxy 与 Group）向已认证调用方返回 `{"username": <Gateway 主体标识>}`，对匿名请求返回 `401` 质询；它不经过仓库授权，因此 `npm whoami` 和 CI 凭据预检在客户端可达的任意 npm 仓库上都可用。原生 npm E2E 门禁现在会通过 Nexus 兼容根驱动真实 npm CLI 的 `whoami` 与匿名拒绝断言。

- 新增 Go Proxy 仓库的上游认证凭据。Go Proxy 仓库可以保存 `none`、`basic` 或 `bearer` 凭据；密钥以 `repository-upstream-auth` 用途由 `GATEWAY_SETTINGS_ENCRYPTION_KEY` 封装，管理 API 永不返回其明文，且只应用于 Go Proxy 的上游抓取。`basic` 与 `bearer` 必须提供密钥，切换为 `none` 会删除已存凭据，跨主机重定向时凭据被剥离；其他格式或仓库类型会以明确的 `400` 拒绝 `upstreamAuth`，而不是静默忽略。

- 新增 APT 签名快照的 Console 灾备界面。仓库管理员可以把任一 visible 或 retired 快照导出为可携带归档，并在同一步获得 `sha256:` 回执以便独立保存；恢复归档时只接受该独立保存的回执。恢复绝不会根据上传的字节现算回执。APT Hosted 仍是操作员预览。

- Console 现在展示 Gateway 已实现的全部 Go 生命周期界面：Go Hosted 仓库会出现保留策略、安全准入、晋升/复制、生命周期任务与墓碑页签，计划任务接受 Go 目标仓库，保留与晋升表单使用 Go 模块术语，分布式部署与授权指标文档也在接受 Go 的位置列出 Go。

- Go Hosted 仓库开始执行默认关闭的隔离读策略。管理员启用该策略后，被隔离的模块版本从 `@v/list` 消失、不会被 `/@latest` 选中，且 `info`、`mod`、`zip` 三个表示均返回 `403`；有序 Go Group 不会越过更高优先级成员中已隔离的坐标向下回落。Hosted 读取默认保持兼容，管理员现在可以像其他格式一样通过同一套版本化管理 API 与 Console 界面管理 Go 策略，每次拒绝都会记录标准的 `quarantine_read_policy` 审计证据。

- 新增管理员专用的 APT 归档精确恢复：独立备份摘要与公钥信任、双签名及规范包索引校验、原子元数据可见性、配额预留、并发重放和失败尝试的持久化对象回收。恢复无需签名服务，APT Hosted 保持操作员预览。

- 新增管理员专用的确定性 APT 签名快照导出和离线归档完整性校验，覆盖已发布/已退役快照及导出字节的 Debian 安装。APT Hosted 仍是操作员预览，可信归档恢复使用独立备份摘要与公钥策略。

- 升级与恢复演练支持固定并验证镜像身份，默认从 v0.1.0 升级，并验证恢复后的 Group 来源与读取权限。

- 新增 Maven/Raw Group 目录与逐成员来源追溯，保留同路径冲突、精确 SNAPSHOT 构建及经过解析范围校验的 Group 专属 Maven 缓存。配置、授权或顺序变化后旧导航失效；共享目录树改进层级、来源链接、证据分组、全部收起和窄屏文件名显示。

- Maven Group 在 Hosted 未命中后继续尝试全部可用 Proxy，正负缓存绑定有序授权候选及上游配置。成员、权限、允许列表或出口代理变化会触发重新解析；旧多成员缓存会刷新，上游故障后的临时回退不会被缓存为完整 Group 解析结果。
- 隔离集成测试、迁移检查及清理命令使用的 Compose 项目，防止工作区 `.env` 或外部项目名把测试清理指向正在运行的本地服务。
- Raw Group 在 Proxy 返回 404、410 或命中成员自身负缓存后，继续尝试后续 Proxy；授权、上游和校验错误仍会终止。分组页面新增服务器候选解析顺序视图，明确 Hosted 优先，并区分配置位置与逐制品实际命中结果。
- 新增 Maven、Raw Proxy 目录树：直接查询 PostgreSQL 缓存索引，按仓库及当前上游隔离，显示实时缓存证据，区分时间戳 SNAPSHOT，并在深链接中保留文件路径和构建号。目录加载支持原位重试；Raw Proxy 详情不再显示 Hosted 删除操作。
- 新增 Console 语义主题系统：内置四套主题，文本选择保持中性，强调色按预算使用；主题切换
  以选择器为圆心径向揭幕，并在 reduced motion 下原子生效。管理员无需重建 Gateway 即可
  严格校验、预览、安装、替换、启用和删除 PostgreSQL 托管主题，版本校验、删除保护和审计
  记录共同约束其生命周期。
- 以紧凑语义图标、可执行说明和响应式布局替代装饰性空状态贴图。
- 为 Maven、Raw Hosted Repository 增加服务端所有、按需加载的目录树；签名节点 ID 与游标
  绑定 Repository 和 principal，Maven SNAPSHOT 节点保留精确构建，Raw 路径保持可读展示，
  复制及操作继续使用规范值。
- 优化登录、公开目录、空状态和主题界面，并补充响应式几何与 reduced-motion 行为检查。
- Artifact Gateway 正式采用 MIT License，并在双语 README 与贡献指南中明确项目和
  贡献许可条款。
- 改善 Raw Console 体验：仓库浏览、公开浏览、全局搜索、保留预览和分发选择器统一显示
  可读的 Unicode/空格路径，但协议操作继续使用不变的规范坐标。搜索可正确处理文件名中的
  字面 `%`，下载命令显式选择经过 shell 引号保护的可读文件名，文件及 checksum 响应通过
  `Content-Disposition` 提供下载名称。
- 加固 Raw 路径边界：编码后最多 4096 字节，拒绝控制字符和双向格式化字符；上传校验不再
  静默裁剪首尾空格或移除开头 `/`，会在发送前返回可执行的修正提示。
- 公开浏览进入管理端时复用当前浏览器已有登录态：已认证用户直接打开 Console，直接访问
  `/login` 也会自动返回请求的管理位置，无需重复登录。
- 明确 Raw path 为兼容 Nexus 迁移的可变引用，`path + digest` 才是治理与分发使用的不可变快照。

## 0.1.0 - 2026-08-24

- 发布首批可复现、带版本且包含 Migrations 与环境模板的 Gateway/healthcheck 二进制归档，
  以及 Console 静态包、已解析 OpenAPI 契约、校验和、GHCR 镜像和通过 CI 的 `main` 主线
  快照；Release 二进制与镜像统一报告版本号和 Git Revision。

- 新增 Maven、npm、PyPI、Raw、Go Hosted/Proxy/Group 的 Nexus 风格
  `/repository/<name>/...` 迁移根路径；真实 Maven/Gradle、npm、twine/pip、Raw HTTP 与
  Go 客户端可保留原 Base Path，npm Tarball、Raw 分页/断点上传、PyPI 与 Go 发布返回地址
  也保持该根路径。PyPI 可直接在 Repository 根路径接收 Twine，Go Hosted 支持 Nexus
  3.93+ 的版本 ZIP 上传并从归档推导、授权模块身份。为避免跨仓库歧义，旧 Maven
  canonical 前缀继续保留精确名称 `maven`。

- 新增 Go Hosted：认证单 ZIP 发布、模块与 `go.mod` 校验、原子生成 `.info`/`.mod`、PostgreSQL/RustFS 按内容持久化、幂等重放、不可变冲突拒绝、发布扫描、墓碑/恢复、保留、24 小时恢复窗口、引用安全回收、三表示晋级与断点复制、Hosted-first Group，以及真实 `go mod download` 门禁。
- 新增面向 Jenkins、CI、扫描器和第三方应用的稳定 Service Account：一次性过期凭证、零停机轮换、立即禁用、Bearer/Basic 认证、Repository Grant、审计、双语 Console 和独立发布门禁。
- 重做公开制品目录，明确只读边界、来源/格式摘要、搜索/过滤及 Hosted/Proxy/Group 引导；管理员界面展示全局、Repository、Group/成员门禁和影响范围，不改变默认拒绝策略。
- OCI browse 响应增加 manifest 不可变创建时间，客户端无需从 tag 或 digest 推断发布顺序。
- 运行时、Compose、Kubernetes、集成测试和配置全面切换到 RustFS 与 AWS SDK for Go v2，移除 MinIO 服务、依赖、迁移工具和旁路，同时对遗留资源失败关闭。
- 完成首个 APT H3 签名加固：远程 signer 必须使用匹配一至两个固定 fingerprint 的公钥 keyring，Gateway 在可见前验证两份签名，支持受控轮换重叠并记录不可变签名证据。
- 新增 APT 签名状态管理 API、双语 Console、有界结果/延迟指标和专用外部 signer 门禁，覆盖旧密钥、重叠、新密钥、拒绝和退役；旧 Gateway 缺少接口时 Console 显示能力不可用，而非误报 Repository 不存在。
- APT H3 后优先规划 Cargo sparse registry，NuGet 延期但保留解析器基础。
- Principal 选择器隐藏禁用用户与已撤销/过期 API Key，只展示可用授权主体。

### 新增

- 准备阶段的双语项目与文档入口、符合架构的 README 主图、本地链接门禁，以及幂等生成本地 PostgreSQL/RustFS 六项凭证的 `make dev-bootstrap`。
- 双语 Mermaid 系统边界、单体/分角色部署、发布可见性和后台任务图；补充带图标架构总览及 PostgreSQL 锁、队列、通知、JSONB、搜索和可观测特性的证据说明。
- 可复现隔离 Docker 性能基线，覆盖 Go 二进制、distroless 镜像、Gateway/PostgreSQL/RustFS 静默内存、认证元数据读取和 64 KiB Raw 读取，并提供双语边界说明与自动清理 runner。
- 从大型 Console Browse 页面拆出 OCI 元数据读取、tag 分页和仓库配置片段，形成有测试的纯模块；随后为所有站点文档补齐实质中文 companion，并增加受测的框架中立导航图。
- Cargo C0 有界 parser：验证官方 publish framing 与完整 `.crate`，从 `Cargo.toml` 派生碰撞安全身份并生成 checksum-owned sparse index；官方 `cargo package/publish` 测试不代表公开接纳格式。
- Docker Desktop overlay 增加固定版本、非 root Traefik Ingress，以 `artifact-gateway.localhost` 暴露同源 Console、API 和协议面。
- NuGet `.nupkg`/`.nuspec` 有界 parser 与大小写不敏感规范身份；协议门禁完成前保持不可发现。
- 加固 Kustomize base 和一键本地 Kubernetes 部署，包含 Gateway、Console、PostgreSQL、RustFS、幂等迁移、持久卷、健康检查、manifest 验证和同源路由。
- APT Hosted H1/H2：流式 Debian control 解析、预留配额 session、管理 provisioning、PostgreSQL/RustFS 持久化、孤儿回收、不可变快照、`Packages`/Release/By-Hash/签名资产、原子可见切换、Range 读取、参考 signer、真实 Debian 安装及精确备份恢复；H3 前仍不公开 Hosted。
- RustFS-only 对象存储基线，覆盖流式、元数据、Range、生命周期、备份和恢复契约。
- 一键本地开发启动、检查和仅停止当前 checkout Console 的生命周期目标。
- 仅管理员可见且已脱敏的系统诊断，以及双语 Console 和可复制 support JSON。
- `api`、`scheduler`、`worker` 分角色部署，支持格式/任务过滤；PostgreSQL 节点心跳、节点清单和 Worker 能力视图。
- 服务端聚合仓库管理、跨仓库搜索、每仓库出站代理及连接检查。
- npm Hosted/Proxy/Group、PyPI Hosted/Proxy/Group、Go Module Proxy/Group、APT Proxy/Group 的原生协议读取、缓存、授权过滤、搜索、容量与 Console 深链接；相应格式补充生命周期、晋级和复制。
- 可配置外部制品扫描与可选非 root Trivy 参考扫描器，提供持久幂等任务、隔离 Worker、原生资产解析、CycloneDX、许可证、漏洞、健康和漏洞库缓存。
- Hosted 发布扫描策略、每 Artifact 状态、手动重扫、有界补扫和漏洞明细。
- 版本化隔离/释放、默认关闭的隔离读取策略、Group 防绕过、晋级/复制 Worker 二次保护，以及 PostgreSQL/OpenAPI/Console 支持。
- 管理员 Webhook：事务 outbox、加密 HMAC、SSRF 安全 HTTPS、有界重试、死信重放、集群租约、审计和 Console 可见性。
- 本地用户治理：档案、大小写不敏感身份、登录锁定、时间戳、强制改密、管理员重置、可撤销 session 和最后活跃管理员保护。

### 变更

- APT 请求同时经 Vite 开发代理和生产 Console 容器转发，避免包客户端路径落入 SPA。
- 扫描、晋级、复制默认输入改为协议所有的不可变 Artifact 选择器，覆盖历史 npm/PyPI、已缓存 Proxy 和 Conan revision；保留精确身份输入作为恢复路径。
- 新增可发现的 Repository Scanning 工作区，支持手动扫描、能力说明、历史补扫和近期任务状态。
- Retention 使用 Maven、OCI、Conan、Raw、npm、PyPI 和 Go 的 cleanup unit 术语，不再使用 Maven fallback 文案。
- Security Tab 分离隔离读取与晋级准入 guardrail，展示格式范围、保存状态和扫描器可用性；修复桌面网格与窄屏 overflow。
- 扫描与晋级 intelligence 统一 claim、租约、指标、终态、轮询和 PostgreSQL notification 语义。
- 减少 Console Repository 请求 fan-out、拆分大 vendor bundle、压缩详情导航并增加克制动效。
- 制品上传流式化并限制数据库、HTTP 和后台 Worker 资源。
- 增加 Go package 覆盖率下限及 Console lint、格式、可访问性、组件、覆盖率门禁。
- 增加 session-aware 分布节点清单、离线状态、清理和集群能力健康摘要；用户管理改用服务端搜索/过滤/分页与聚焦 Drawer。
- 发布锁使用有界可观测 PostgreSQL pool，多文件对象锁和坐标锁复用同一后端 session。
- Raw 大对象路径改为有界缓冲临时 staging、流式缓存发布/读取、真实上游 `HEAD`、可续租请求锁和每 Gateway staging 准入；超限返回 `503`/`Retry-After` 与有界指标。
- Raw/OCI 可恢复上传改为不可变 offset chunk，完成时一次组装，避免每次 PATCH 重写历史字节；回收会删除完成、取消或过期 session 的残留 chunk 并保留 PostgreSQL 轨迹。
- 性能报告增加 64 MiB Hosted warm read 和受控 HTTPS Raw Proxy cold miss，验证 single-flight 与离线缓存重放。

### 修复

- Maven Hosted 默认兼容 Nexus：标准 Maven/Gradle 上传后可直接读取；新增默认关闭的 `mavenStrictPublication`，供需要 Gateway companion commit 和坐标级原子可见性的团队选择，并覆盖双语说明与真实客户端。
- Maven SNAPSHOT 元数据使用最新 timestamped version 与独立 extension/classifier；Hosted/Group 提供 SHA-512、SHA-256、SHA-1、MD5 sidecar。
- npm Hosted/Proxy/Group 提供 package-version 元数据和冷缓存 Group tarball URL 重写，支持 Corepack 固定版本安装。
- 认证 PUT 会替换过期 Maven staging session，使中断发布可重试。
- 修复 npm cold `package-lock.json` tarball、旧 scoped package 路径、无害 dot segment、旧 metadata integrity、无效 dist-tag、大 packument、online-to-offline `npm ci` 和 member-owned 终态审计。
- OCI manifest 媒体类型选择支持重复 `Accept` 请求头。
- OpenAPI 检查不再在运行中的 Vite 下重装依赖，默认 lazy-route 异常页改为双语恢复界面。
- 公开制品深链接可跨越 browse 第一页解析；修复 PostgreSQL OCI 过期上传回收、CI 生成物与生命周期顺序。
- 聚合容量包含 PyPI 和 Go；PyPI 晋级、复制、恢复阻止文件成员变化造成的部分版本，并在精确重放时刷新检查点。
- 用户管理审计正确记录执行操作的管理员，自助改密记录本人。

### 安全

- Go 工具链和发布镜像升级到 1.26.6，使发布就绪检查基于已修补标准库。
