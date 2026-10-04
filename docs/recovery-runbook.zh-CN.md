# 备份与恢复演练

[English](recovery-runbook.md)

## 可移交离线 S3 字节备份（Unreleased）

`gateway backup export` 与 `gateway backup restore` 实现 #200/#201 的离线传输，
独立于 agctl，只读取显式私有 JSON spec，不加载服务器 `.env`。导出只读已停写来源，
不停止或重启 writer。操作者必须列出全部 API、worker 与外部 writer，提前停写并保持
到命令结束。前后停止身份、DB 元数据、库存、有效引用闭包和全字节复核用于发现变化，
不构成分布式 fence；一致性责任仍属于操作者。

```sh
gateway backup export --spec /private/source.json --bundle /private/new-bundle
gateway backup restore --spec /private/new-target.json --bundle /private/new-bundle
```

spec 必须是私有普通 JSON 文件（0600，最多 1 MiB），拒绝重复和未知字段。新备份根目录
为 0700，文件为 0600；拒绝已有目录。失败保留 `INCOMPLETE` 和本地证据，全部校验结束
才发布 `manifest.json` 并移除 `INCOMPLETE`。验证与恢复期间必须保持备份不可变且受控。
只使用可信 dump 和可执行文件：摘要证明身份，不证明内容可信。

失败导出会在受控备份集内保留 0600 的 `failure.json`，记录备份 ID、UTC 时间、阶段、
当前对象 key/相对文件和安全原因码；原始上游错误会被丢弃。

来源 spec 使用以下显式字段：

| 字段 | 契约 |
| --- | --- |
| `backupId`、`scopeId`、`inventoryDeclaredComplete` | 非敏感不透明 ID 与完整 writer 清单声明。 |
| `writers` | 非空 `{id, kind, reference}` 数组。`docker-container` 使用完整 64 位容器 ID；`systemd-unit` 使用显式 `.service` 单元。传输前后必须停止/inactive，观察到的停止身份相同。 |
| `docker.context` | Docker writer、OCI 和容器 PG 工具使用显式本地 Unix socket context；拒绝远程 Docker。继承的 `DOCKER_*`/`COMPOSE_*` 无法重定向子进程。 |
| `postgres` | `dsn` 为显式 `postgres://user:password@127.0.0.1:port/database?sslmode=disable`（也接受 localhost）。不使用 service 文件、默认值或其他 URL 参数。可选 `toolsContainer` 必须是该 loopback 绑定的运行中 PG 容器，否则使用本地 pg_dump。 |
| `s3` | 显式 `endpoint`、`bucket`、`accessKey`、`secretKey`。使用 S3 LIST/GET，不将物理目录或 multipart ETag 当成字节摘要。 |
| `release` | 操作者批准的本地 `directory`、`identity`；OCI 另需 `imageReference`，规则如下。 |

release directory 必须含与已应用 ledger 完全一致的 `migrations/*.sql`。binary 还需
`gateway`，身份为 `{version, revision, artifact:{kind:"binary", sha256:"sha256:...",
platform:"linux/arm64"}}`（或 linux/amd64）。完整文件摘要、Go 构建信息、注入的版本/
revision、平台与每项 migration checksum 均须一致。OCI 使用 `kind:"oci-image"`、
artifact.sha256 中的 manifest digest 和固定摘要 imageReference；镜像须已在本地，
RepoDigest、平台、OCI version/revision 标签全部一致。备份不能自行选择下载软件。
v1 保留仅镜像 imageDigest，新导出使用 v2 的明确 artifact 身份。

导出流式读取整个有序 bucket（包括无引用字节），随后再次 LIST/GET 并核对所有已发布
DB 引用存在；intent/GC 行不等于已发布对象。兼容 v0.4.2 的完全无 Cargo 表引用查询，
部分 Cargo schema 明确拒绝。软件与完整 migration ledger 仍必须匹配；这不证明
v0.4.2 到当前版本升级，向前迁移需单独验收，不支持向下迁移。

恢复 spec 含 `project`、`docker`、`release`、`postgresPassword`、`accessKey`、
`secretKey`、`rpcSecret`、`adminToken`、`resolverToken`。由操作者提供临时演练值，
命令不签发凭据、不改变 IAM/grant。project 必须是全新的 ag-restore-... 名称，最多
42 字符。已有网络、卷或服务容器即使为空也拒绝；不接受任意目标 DSN、endpoint、
bucket 或 Compose .env。适配器新建独立本地 bridge、两个独立 local 卷、PG16、固定
RustFS 和 Gateway。端口只绑定 127.0.0.1；写入前核对身份、所有权标签、挂载和唯一
网络归属。bridge 提供数据/项目隔离，不是出口防火墙。Gateway 使用 API role、只读
根目录、移除 capability 与有界 /tmp 内存挂载，支持既有协议 spool。

创建目标前验证全部输入、批准的软件/schema 和完整解压 PG custom archive。随后
pg_restore --exit-on-error 写入新 DB，在 Gateway 启动前核对完整 ledger 与所有 public
表指纹，包括 grant set、Group 归属、历史审计和持久工作。S3 上传后全字节读回；
readiness、协议与授权结果分开报告。

可选 readerToken、deniedToken 和 readChecks 执行固定 Raw/OCI GET。每项含 kind
（protocol/grant-allow/grant-deny）、format（raw/oci）、相对协议 path、credential
（admin/none/reader/denied）与预期 status。成功读需 size 和 sha256；grant 检查还需
principal，与 /api/v2/identity 返回 actor 一致。拒绝允许 401/403，使用独立已认证的
denied 主体。例如对同一 Group 路径同时配置允许与拒绝，独立记录来源摘要和成员顺序。
只有允许及拒绝都通过才标记 authorization: verified；未配置保持 not_run。
这只证明所选断言，不等于全部协议/身份通过。

runtimeEnvironment 仅接受恢复语义需要的既有 reader/legacy-read、settings-encryption、
egress-key 和 OIDC 配置。必要密钥与运行配置经受控 spec 传递，不写入公共报告；
不能覆盖 endpoint、DB、S3、role 或 instance 配置。

传输出口：exported/restored 为 0，失败为 1，参数/spec 输入或报告输出失败为 2。
报告含备份 ID、UTC 检查时间、软件身份、schema 摘要、字节统计，独立列出 database、
metadata、grant/Group/审计元数据、objects、readiness、protocol、authorization。
元数据结果是启动前精确指纹比较，启动后读回新增审计属于正常行为。stdout 不含源坐标、
对象 key、原始错误或凭据；失败计数只是验证前缀。preflight backup 仍保持整体一致性
unknown/退出 3，离线摘要校验不得代替真实恢复验收。

成功目标保留供受控复核。后续清理前记录新项目、核查 owner 标签及精确容器/网络 ID。
失败只清理本次创建且身份、所有权、挂载和网络证据一致的资源，变化/外来资源保留并报告
清理未完成；不执行项目级 compose down，不删源 bucket 或已有卷。SIGINT/SIGTERM
取消传输后使用独立、有界上下文清理。重试使用新 project，不接管/覆盖旧目标，不自动
开放流量、不部署、不发布或合并。

`make backup-transfer-test` 分别通过公开 export/restore 入口执行真实合成
PG/S3 恢复，覆盖 binary 与 OCI 软件 profile。两者均使用 Raw Hosted/Group 数据：
3 个对象、54 字节含一个未引用对象；验证完整 bucket、当前与 pre-Cargo 引用查询、
备份后 mutation 排除、grant 允许/拒绝、Group 顺序、历史审计及受保护 source/sentinel
的身份、数据和健康。每个 profile 的 11 项拒绝/启动失败场景只清理本次调用拥有的目标。

OCI fixture 将当前 checkout binary 与已有 digest-pinned runtime 构建成镜像，匿名推送到
随机命名的临时 registry，镜像固定为
`registry:2.8.3@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373`。
registry 只绑定已验证本地 daemon 内的 `127.0.0.1`（含 Docker Desktop VM），使用
私有 tmpfs，不采用现有 network 或 volume。临时 Docker 配置显式声明匿名 registry，
拒绝 helper 证明不调用主机凭据 helper。恢复使用实际推送所得 digest 及支持的 Linux 平台。
测试核对实际 RepoDigest、平台、标签、运行 image ID、无 binary 挂载，以及公开 Gateway
构建诊断。错误 digest/version/revision/platform、可变 tag、仅 image ID 和本地不可用
image 在公开 export/restore 中失败，不发布备份集、不创建目标。fixture 镜像和 registry
只在核对捕获的精确 ID、名称、标签与绑定后移除；资源变动则保留并报告清理失败。
共享固定 base image 与 build cache 保留。不创建持久凭据或 daemon 设置，
不假设外部 Gateway 包的可见性或权限。

这里 OCI 指 **Gateway 软件 profile**，不表示 OCI 制品协议恢复；此 transfer fixture
只执行 Raw 协议。OCI 与 binary profile 均构建当前 checkout。
`make backup-restore-readiness` 独立验收物理 profile。两者均不证明生产 fencing、所有
格式或身份提供方、systemd controller、跨版本升级、大对象中断矩阵或云厂商特定 S3
恢复。#200/#201 仍有更广验收工作，preflight 与这些限定测试均不能关闭完整矩阵。

## 邮件恢复门禁与安全升级回退（v0.6.0 准备）

完整 PostgreSQL dump 和动态 public 表摘要已包含新增邮件／配额表，没有需更新的表
白名单。本门禁在现有 binary／OCI 恢复 profile 加入非空加密目标、测试请求记录、配额
状态／episode／事件／描述符，以及 pending/retrying、活动／过期 claim、取消与终态
交付。八个并发且显式启用的 worker 使用任务拥有、不向外转发的 TLS SMTP 接收端，
只有可处理项发送。已取消的过期租约转 dead 并保留 possibleDuplicate；accepted/dead
保持终态。活动租约和未来重试期限阻止处理；过期未取消项保留 possibleDuplicate，
旧 token 无法完成新 claim。另验收 API 安全读回和非管理员拒绝。

`GATEWAY_SETTINGS_ENCRYPTION_KEY` 是独立运维前提，按操作者 secret 管理策略单独备份／
托管。备份集保存加密收件快照，不打包加密密钥、SMTP 认证文件或运行配置；通过私有
目标运行设置提供原密钥。缺失／错误密钥无法解密。恢复 Gateway 强制 API-only，运行
allowlist 无法启用 SMTP。显式启动邮件 worker 前复核 pending/in-flight/
possibleDuplicate 与取消状态。较早快照可能重复外部已接受尝试；SMTP 不保证
exactly-once，已接受邮件不能撤回。

`make upgrade-readiness` 固定正式 v0.5.0 `ea60aea333b29bb60d4ac1b8e2b2a8720563726f`。
停全部 API／Scheduler／Worker 和外部 writer；保留一致的升级前 PG＋对象快照与匹配
软件／配置／密钥。一个迁移作业应用 000136–139 后，再启动同 revision 各角色和
Console；邮件默认关闭。门禁检查二次 no-op、核心身份／字节和新状态读回，再将升级前
快照恢复到全新 v0.5.0 目标并前滚。对象保持相同 S3 字节格式，DB 是增量前向迁移；
仅 schema 增量不能批准旧 binary 读扩展 DB。本门禁不批准 down migration 或不认识
000139 的中间开发版邮件 worker。完整快照回退丢失快照后的变更，重开流量／worker
前需复核外部邮件副作用。已发布镜像分发、生产部署与所有 provider／协议／规模矩阵
仍未验收。

物理 `restore-drill.sh` 会重启原 Gateway 容器。如果操作者自定义启用了邮件角色／
配置，须先停用／排空邮件，并在恢复后明确复核／授权重新启用。

## 固定物理演练

在安装 Docker Desktop、已经配置 `.env`，并通过 `make up` 启动本地栈的工作站上执行
本演练。脚本会把备份保存在 `.artifacts/`，该目录不会进入源码版本控制。

## 目标

Unreleased [离线清单验证器](backup-manifest-verification.zh-CN.md) 仅验证私有本地
字节与声明。真实可移交传输使用上文独立命令；验证器不证明快照一致性，
两者均不替代此固定物理演练。
正常的 unknown 结果不能作为备份成功。

- RPO：两次成功执行 `scripts/backup-drill.sh` 的时间间隔。MVP 演练目标为 24 小时。
- RTO：从宣布开始恢复，到 `/readyz` 返回 204，目标为 30 分钟。

## 演练步骤

1. 记录 UTC 开始时间并建立备份必须保留的证据：创建或获取一个测试制品，记录预期审计
   记录，并通过原生协议客户端解析。验证 V2 时，记录 Raw 规范路径或 Conan revision
   坐标、成员 allowlist 决策，以及读取使用认证还是匿名。验证 Go Hosted 时，记录模块
   路径、版本及 `.info`、`.mod`、`.zip` 三种表示的摘要，然后使用全新 `GOMODCACHE`
   解析。涉及受管仓库时，记录 Grant Set ETag、一个拥有 `repositories:read` 的 Principal
   和另一个没有该权限的独立 Principal；禁止记录任何凭据。
2. 执行 `scripts/backup-drill.sh`，然后确认 PostgreSQL dump 与 RustFS tar archive 均通过
   `shasum -a 256 --check <backup-dir>/SHA256SUMS`。
3. 创建一次可逆的备份后变更，例如一次性 Group 或新增制品版本。记录该资源，并在可行
   时记录其唯一对象摘要；恢复后，它的元数据和无引用对象都必须不存在。
4. 记录 UTC 恢复开始时间，执行 `scripts/restore-drill.sh <backup-dir>`。
5. 确认 `curl -fsS -o /dev/null -w '%{http_code}' http://localhost:8080/readyz`
   返回 `204`；使用管理员 Token 查询 `GET /api/v1/audits`，并解析缓存制品。对 V2 数据，
   还要通过恢复后的 Gateway 解析所记录的 Raw 路径与 Conan 2 revision，确认审计记录仍
   保留格式、Actor、成员、缓存处置和结果。对 Go Hosted，使用另一个全新 `GOMODCACHE`
   再次下载，确认三种表示摘要仍与记录一致。

   通过管理或协议接口，以及对象存储中唯一对象摘要的直接查询，确认备份后变更已经
   消失。对受管仓库，确认记录的 Grant Set ETag 和 Principal 仍然存在：已授权 Principal
   可以读取所记录对象，未授权 Principal 收到该协议正常的拒绝响应。意外放行应作为
   安全事件处理：保持仓库下线、保留备份与审计证据，并由管理员恢复最后已知 Grant Set，
   然后才能重新开放流量。
6. 记录 UTC 完成时间、实测 RTO、用于计算 RPO 的备份时间戳，以及所有失败验证项。

## 安全边界

`restore-drill.sh` 会覆盖正在运行的 PostgreSQL 数据库和 RustFS 数据。只允许在隔离的
演练环境中执行，并且必须先保存所有需要保留的数据。脚本会在恢复两个存储时停止
Gateway，防止新元数据指向被中断状态下的对象。对象 archive 只对锁定的 RustFS 基线
有效；项目不再提供或支持旧对象存储迁移路径。
