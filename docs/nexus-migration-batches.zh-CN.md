# Nexus 持久迁移批次

[English](nexus-migration-batches.md) · [文档索引](README.zh-CN.md)

管理员 API 将[只读源发现](nexus-source-discovery.zh-CN.md)接到原生 Maven release、
Raw、OCI、Conan 2、npm 与 PyPI Hosted 发布。批次统一使用 `native-v1`，不支持旧策略输入或历史
管理接口。生产切流、源删除、冲突覆盖、Proxy 历史缓存和在线 SNAPSHOT 历史不在此
流程范围内。已交付的离线 SNAPSHOT 导入器保持独立。

先应用 `000147_nexus_migration_batches.sql` 和
`000148_nexus_migration_scan_diagnostics.sql`，以及追加的
`000154_nexus_native_migration_formats.sql` 和
`000155_nexus_migration_scan_checkpoints.sql`、`000162_nexus_npm_migration.sql` 与
`000186_nexus_pypi_migration.sql`、`000189_nexus_migration_actor_kind.sql`；统一使用标准迁移程序。将
`GATEWAY_MIGRATION_STORAGE_POOL_ID` 配置为真实对象存储池标识；Compose 转发给所有
继承的 Gateway 角色。不配置时仅禁止迁移写入，其他网关功能和迁移报告仍可读。
源 HTTPS、凭据加密、精确内部地址 allowlist、DNS 固定及禁止重定向均复用源发现，
不调用 Nexus 写 API。

## 契约与容量

以下路径均相对 `/api/v2`，只允许管理员，并返回 `Cache-Control: no-store`。

- `GET /migrations/nexus/capabilities` 回读不透明 `targetId`、配置的 `poolId`、
  执行能力、策略和有限重试次数，不回显 endpoint、bucket 或凭据。生产绑定对显式装配
  的对象存储 endpoint/bucket 求摘要；自定义装配必须为实际存储提供匹配绑定，不能假设
  不同格式存储属于同一池。
- `POST /migrations/nexus/batches` 固定 `sourceId`、精确 `sourceVersion`、源仓库到
  已有目标仓库 UUID 的显式映射、`policyVersion=native-v1` 和容量证据。
  后台完整枚举后才原子封存 manifest 与条目，重启后继续已提交的分页；响应非法、超限、
  分页循环或源观察变化均明确失败，半清单永远不能执行。
- `GET` 集合、`/{batchId}` 和 `/{batchId}/entries` 回读持久状态；集合/条目使用
  `limit`（1–100）与 UUID `cursor` 分页。
- `POST /{batchId}/pause`、`/resume`、`/retry`、`/cancel` 控制批次。启动与控制均必填
  `Idempotency-Key`，控制还需把响应 `ETag` 的版本原文作为不带引号的 `If-Match`。
  相同键不同内容冲突；相同操作持久重放原响应，不因旧 CAS 再执行。

容量是**操作者提供的外部证据**，不是 Gateway 实测 S3/RustFS。证据固定目标、真实池、
观测/过期时间、空闲字节、下载/上传/备份/恢复峰值及预留空间；每个字节值必须已知且
非负，不接受未来观测，有效期最多一小时。恢复/重试必须提交观测时间严格更新的证据。
未知、不足、过期或绑定不符时，原生会话和对象写入均不启动。服务端独立重读仓库配额、
原生身份和已有对象完整字节，并调用 `capacityplan.Evaluate`；原生发布事务继续做实时
配额约束。等待格式/GC 锁后再次准入。证据不等于容量预留；临时目录/日志文件系统空间
绝不冒充真实对象存储池空闲空间。

## 恢复与身份

PostgreSQL 持久保存不可变批次输入、完整 manifest 摘要、每个对象的摘要/validator、
尝试次数与 checkpoint。单个 worker 持有一个批次租约，不同批次可并行。必须启用 worker 角色和 `migration` kind，格式过滤必须同时允许该
批次所有格式。争抢使用
`SKIP LOCKED`、新 token 和递增 epoch，旧/过期持有者不能更新账本。实际原生发布事务
先取得与恢复/控制相同的事务 advisory fence，再依次锁批次行与源配置行，校验源版本、
有效 token、过期时间及取消状态后才改变可见引用。源行锁保持到实际原生事务提交；
源更新若先完成则拒绝旧源发布，若发布先提交则更新等待到提交之后。等待源锁后再次
复查租约是否到期；该物理事务连接丢失时发布也必然回滚，不再依赖另一条会话连接
持锁。格式对象锁覆盖暂存到发布并与 GC 协调。原生提交后条目 checkpoint 丢失仍通过
完整读回恢复。

暂停确认后不再启动新条目，已记录的在途条目可完成并落账，排空后由 `pausing` 变为
`paused`。取消使用同一发布 fence，保留已经提交的目标；取消成功后旧 worker 无法越过
提交边界。每项最多三次尝试，显式重试只重新排入仍可尝试的失败/源缺失项，不重写冲突
或不支持项。

Raw 使用原生只创建 CAS。Maven 冻结包括 POM、主件和 classifier 的完整 release GAV，
校验 packaging 必需主件，通过原生发布会话原子提交全部路径。Gateway 生成校验旁文件，
其精确容量也纳入预算；Nexus 元数据明确报告为重新生成。缺 POM、缺必需主件或未知
packaging 明确不支持；已有 GAV 的资产集合变化产生冲突。无法原生寻址的路径及 Raw
虚拟校验资源路径也明确不支持。

OCI 将 Nexus `docker` 库存显式映射到原生 OCI。每个 tag/digest 保留完整原始
OCI index / Docker manifest list、嵌套 manifest、config、layer、全部平台及共享对象，
图深度最多 8、对象最多 1,024；每份 manifest 最多 16 MiB、单次库存预读总计最多 64 MiB。扫描先读取并校验 manifest 正文再封存，复制时重新核对。
完整验证闭包与缺失 tag 在同一事务中可见；已有不同 tag 冲突，不展开 index、不覆盖 tag。
缺 child、描述符 size/hash 错误、tag 漂移和无效图不能成为完整成功闭包。
foreign layer、subject/referrer 图和未知媒体类型明确不支持；没有平台子集选项。

Conan 只从精确 revision storage path 识别身份，不从 Browse 展示推断：
`conans/name/version/user/channel/revisions/RREV/files/file`；package 在其下使用
`packages/PID/revisions/PREV/files/file`。源文件下载增加 `v2/` 协议前缀。
每个 recipe/package revision 都复用原生 Conan 的完整 manifest、展开文件和身份校验，
原始归档逐字节保留，recipe 先于其 package 发布。过期后新会话使用新的 stage key；
可见元数据、会话完成和真实操作者审计共用带批次租约 fence 的事务。隔离或墓碑 revision
不能计为验证后跳过。Conan 1、SCM revision、可选 metadata 和非 tgz 归档不支持。

旧成功记录不能直接作为完成证据。同身份同摘要必须再次验证当前源字节/validator、
目标身份、完整 size/SHA-256 后才能跳过。目标内容不同则保留并冲突。上传后未落账和
提交响应丢失通过完整读回核对恢复，不生成第二份可见发布。源配置版本、映射或策略
变化需要新批次；后续批次始终重新完整枚举：新增身份发布、相同身份重验、源删除只报告
并保留目标，不使用仅基于时间水位的增量。

## PyPI Hosted 发行文件

[PyPI 迁移](nexus-pypi-migration.zh-CN.md) 保留同一 project/version 的完整 wheel 与
`.tar.gz` 源包、Requires-Python 和 yank 元数据。该适配器要求当前有效的持久管理员身份或
管理员 API key。批次与计划绑定真实认证种类和稳定所有者 ID，在派发和发布前重新检查
该所有者的当前权限。仅有显示名的静态主体无法证明当前权限，因此明确拒绝。源码构建及其他
归档形态仍在本切片之外；`make nexus-pypi-migration-e2e` 强制执行十一项真实持久化/客户端验收。

## npm Hosted 包闭包

npm 使用 Nexus Components REST 的 `group`/`name` 与 `version` 身份，和完整
packument、归档内 `package.json` 交叉核对，绝不从 tarball 文件名猜版本。每个条目包含
一个源包的全部版本及 dist-tag 目标；库存版本缺失或多出、tarball 缺失、SHA-256、
shasum、SRI 或包内身份不符，以及跨源或含凭据的 dist URL 均在发布前失败。读取沿用
源的 HTTPS、凭据、出站和超时边界。每个版本恰有一个 tarball，最大 256 MiB；
每个 packument 最大 64 MiB、1,000 个版本，每批全部 packument 合计最多 64 MiB。SRI 支持 SHA-1/256/384/512 并选择最强的已提供
算法；如有 shasum 也必须一致。未知算法和 SRI options 语法明确拒绝。

源 packument 的精确字节与摘要作为私有账本状态保存，在扫描完成后和发布前重新观察。
目标原生 packument 由原版本 manifest 与固定标签生成，URL、integrity、registry revision
及发布时间由 Gateway 生成；源包级 `name`、`versions`、`dist-tags` 以外字段保留在私有
回执，不回放到目标 registry。源读取只是观察，不是 Nexus 事务快照；最终核对需要这项
一致性时应停止源写入。

全部缺失版本与标签在一个 PostgreSQL 事务内提交，沿用迁移租约/源版本保护、原生包
协调锁、配额与隔离校验。已有相同版本保持原样；只创建缺失标签，已有标签指向、
manifest、deprecate 或字节身份不同会使整个包冲突。仅目标存在的额外版本和标签保留，
源删除不删除目标。源 tag 移动或 deprecate 更新必须独立审阅并显式治理目标后再建批次，
迁移不会自动覆盖。恢复在原生对象锁下暂存摘要寻址 tarball，存储重开或响应丢失后重新
校验完整字节；单独的源元数据成功不能使包可见。

这里封闭的是一个包的存储内容，不自动递归抓取其它 dependency registry。必须显式
选择并映射全部所需 Hosted 包仓库；外部、optional 和平台相关依赖仍需 target-only
消费验收。Proxy 缓存和 Group 字节仍不在导入范围。`visibleIdentities` 计包闭包；
固定 packument 只计入 `sourceVerificationBytes`，`totalBytes`、`transferredBytes`
与 `verifiedBytes` 只计真实 tarball 字节，私有 packument 回执不进入管理资产列表，也不会取得已上传/已验证的目标字节回执。

`make nexus-npm-migration-e2e` 必跑九组 PostgreSQL/RustFS 验收，拒绝跳过用例，覆盖
源与目标漂移、上传/校验/提交故障恢复、源版本/租约/原生事务丢失、配额与隔离保护。
门禁按官方归档 SHA-512 固定 npm 11.6.2，关闭合成源并重开目标存储，以空缓存解析
两包依赖链并核对四个原始 tarball。`output/nexus-npm-migration-e2e/<project>/` 保留
原始 Go JSON、必需用例汇总、客户端输出、源码 head/tree 与清理回执。脚本只创建、
清理其自有 Compose 项目，结束后自有容器、卷和网络必须全部清零。这些 fixture 不代表
真实业务 Nexus 版本或库存已验收，也不授权生产流量变更。

## 进度与限制

`metadataDiscovered` 计库存条数，并优先使用发现结果已有的必需摘要。缺 SHA-256，或
Maven 为生成旁文件缺 SHA-1/MD5 时，执行有界源验证，真实读取字节独立持久计为
`sourceVerificationBytes`，扫描未完成/失败也保留。OCI 图还需预读 manifest 正文，npm 则必须预读完整归档。Raw、Maven 和 OCI 校验流式
求摘要且不写目标；npm 为核对包内身份使用私有临时归档文件，有界读取后立即清理。

`transferredBytes` 计复制阶段真实源正文读取字节，包含重试和对已有身份的重新验证；
`verifiedBytes` 按唯一原始目标字节的完整读回证据计数，不因重试膨胀；
`visibleIdentities` 独立计原生 Raw 路径、Maven GAV、OCI 引用、Conan revision、npm 包闭包与 PyPI project/version 闭包的
已提交或读回确认数量。
`totalBytes` 在清单完整封存前为 null，之后计支持范围内唯一原始字节。Unsupported 与
源删除项保留独立报告，不能把这些计数称为官方客户端验收。
不支持的路径使用 `/unsupported/<解码后的源路径>` 合成报告身份，与原生相对 Raw
路径和 Maven GAV 必然隔离。重复源路径在发现阶段明确失败；完整重扫在每批次中为
每条不支持路径保留一份报告，不重命名或丢弃合法原生身份。

扫描失败时，`lastScanFailure` 独立于可执行条目持久保存有界阶段、选定仓库范围、
已知的精确源仓库/资产路径/身份、公开错误码与发生时间，进程重启后仍可读。尚未确定
资产时路径和身份为空；元数据阶段记录选定仓库范围，不回显上游 URL 或凭据。半扫描
仍没有可执行 manifest 或条目，`totalBytes=null`；后续完整扫描成功才清除旧诊断。

迁移库存扫描使用独立分页源读取器，明确限制为十个仓库、10,000 个已记录页、100,000 个资产，
资产 wire metadata 累计 64 MiB（含第二次观察），每页最多 2 MiB、30 秒。交互式发现预览仍保留较小的
100 页/10,000 资产预算；两条路径超出预算均明确失败，不截断后宣称完整。

私有 PostgreSQL 检查点将每页及下一 cursor 同事务提交，绑定源版本、选定仓库形状、
映射和策略。重启或应答丢失后从已确认 cursor 继续；暂停/取消和过期 worker 均不能
推进。枚举结束后逐页第二次观察，必须与全部已记录页面一致才封存完整 manifest。
cursor 过期、循环、重复路径、源观察变化和预算不足均明确失败；源漂移需要新建批次。
这两次观察不代表 Nexus REST API 提供事务快照。封存前即使 `metadataDiscovered`
已增长，也没有可执行条目，`totalBytes` 仍为 null。

单文件仍最多 1 GiB、30 秒，以 size+1 有界读取并完整求摘要。复制使用创建后立即
unlink 的私有临时文件，恢复按整文件重试，不承诺 Range/multipart 续传。

`make nexus-inventory-scan-e2e` 使用自有 PostgreSQL/RustFS fixture，验证超过100页/
10000资产、重启及应答丢失恢复、租约 fence、暂停取消、源绑定和扫描预算拒绝。
必需用例不允许跳过，自有资源必须清零。

`make integration-test` 包含独立 PG 包，验证真实 PostgreSQL/RustFS 的故障窗口、
多实例/过期租约、暂停/取消、容量、源变化和原生协议读回。独立的
`make nexus-migration-client-e2e` 门禁关掉源后使用空制品缓存，仅从目标解析。
固定官方 Maven 3.9.16（归档 SHA-512
`831a8591fe20c8243b1dbe7d71e3244f31d1665b0804b2e825e38cbbe5ce0cafb8338851f90780735568773e0a6cd07bbec107cda0b896b008b861075358b6f6`），
并在独立临时目录从官方源码构建 curl 8.14.1（SHA-256
`6766ada7101d292b42b8b15681120acd68effa4a9660935853cf6d61f0d984d4`）。
两种客户端都仅经 Gateway 验证完整预期库存、size 与 SHA-256。这份 loopback HTTP
证据不证明生产 TLS，也不授权切流；任何本机/CI 通过都不代表生产迁移放行。

新增 `make nexus-oci-migration-e2e` 固定官方 crane 0.22.1，对 OCI index、Docker list、
嵌套 index 分别使用独立空 layout 验证全部平台及 amd64/arm64，并覆盖真实 PostgreSQL
租约 fence、原子闭包发布、响应丢失恢复和 GC 竞争。`make conan-e2e` 同时强制 Conan
迁移验收；`make nexus-conan-migration-e2e` 可单独运行，固定 Conan 2.21.0，真实重启
PostgreSQL/RustFS，验证两个历史 revision 在全新客户端 home 中只从目标冷安装。
自有 fixture 均核对资源清零。这些使用合成 Nexus REST 库存及标准协议源，不代表所有
已部署 Nexus 版本或业务库存均已验收；生产信任、容量和最终切流另需证据。
