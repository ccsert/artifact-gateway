# Nexus 持久迁移批次

[English](nexus-migration-batches.md) · [文档索引](README.zh-CN.md)

管理员 API 将[只读源发现](nexus-source-discovery.zh-CN.md)接到真实 Maven release
与 Raw Hosted 发布。它不执行生产切流、不删除制品、不覆盖冲突，也不迁移 Proxy
历史缓存、OCI 或在线 SNAPSHOT 历史。已交付的离线 SNAPSHOT 导入器保持独立。

先应用 `000147_nexus_migration_batches.sql` 和
`000148_nexus_migration_scan_diagnostics.sql`。将
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
  已有目标仓库 UUID 的显式映射、`policyVersion=release-raw-v1` 和容量证据。
  后台完整枚举后才原子封存 manifest 与条目。扫描中断、响应非法、超限或分页循环均
  明确失败，半清单永远不能执行。
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
先取得与恢复/控制相同的事务 advisory fence，锁批次行并校验有效 token、过期时间及取消
状态，再改变可见引用；该物理事务连接丢失时发布也必然回滚，不再依赖另一条会话连接
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

旧成功记录不能直接作为完成证据。同身份同摘要必须再次验证当前源字节/validator、
目标身份、完整 size/SHA-256 后才能跳过。目标内容不同则保留并冲突。上传后未落账和
提交响应丢失通过完整读回核对恢复，不生成第二份可见发布。源配置版本、映射或策略
变化需要新批次；后续批次始终重新完整枚举：新增身份发布、相同身份重验、源删除只报告
并保留目标，不使用仅基于时间水位的增量。

## 进度与限制

`metadataDiscovered` 计库存条数，并优先使用发现结果已有的必需摘要。缺 SHA-256，或
Maven 为生成旁文件缺 SHA-1/MD5 时，执行有界源验证，真实读取字节独立持久计为
`sourceVerificationBytes`，扫描未完成/失败也保留。此阶段仅流式计算摘要，不写目标、
不使用本地下载暂存文件。

`transferredBytes` 计复制阶段真实源正文读取字节，包含重试和对已有身份的重新验证；
`verifiedBytes` 按唯一原始目标字节的完整读回证据计数，不因重试膨胀；
`visibleIdentities` 独立计原生 Raw 路径/Maven GAV 的已提交或读回确认数量。
`totalBytes` 在清单完整封存前为 null，之后计支持范围内唯一原始字节。Unsupported 与
源删除项保留独立报告，不能把这些计数称为官方客户端验收。

扫描失败时，`lastScanFailure` 独立于可执行条目持久保存有界阶段、选定仓库范围、
已知的精确源仓库/资产路径/身份、公开错误码与发生时间，进程重启后仍可读。尚未确定
资产时路径和身份为空；元数据阶段记录选定仓库范围，不回显上游 URL 或凭据。半扫描
仍没有可执行 manifest 或条目，`totalBytes=null`；后续完整扫描成功才清除旧诊断。

首版保留源发现上限：十个仓库、100 页、10,000 个资产、16 MiB 元数据，每次源操作/
单文件最长 30 秒。单文件最多 1 GiB，以 size+1 有界读取并完整求摘要；慢文件可能先
触及时间上限。复制使用创建后立即 unlink 的私有临时文件，恢复按整文件重试，不承诺
Range/multipart 续传。大清单明确失败，需要后续分页扩展，不能截断后宣称完成。

`make integration-test` 包含独立 PG 包，验证真实 PostgreSQL/RustFS 的故障窗口、
多实例/过期租约、暂停/取消、容量、源变化和原生协议读回。独立的
`make nexus-migration-client-e2e` 门禁关掉源后使用空制品缓存，仅从目标解析。
固定官方 Maven 3.9.16（归档 SHA-512
`831a8591fe20c8243b1dbe7d71e3244f31d1665b0804b2e825e38cbbe5ce0cafb8338851f90780735568773e0a6cd07bbec107cda0b896b008b861075358b6f6`），
并在独立临时目录从官方源码构建 curl 8.14.1（SHA-256
`6766ada7101d292b42b8b15681120acd68effa4a9660935853cf6d61f0d984d4`）。
两种客户端都仅经 Gateway 验证完整预期库存、size 与 SHA-256。这份 loopback HTTP
证据不证明生产 TLS，也不授权切流；任何本机/CI 通过都不代表生产迁移放行。
