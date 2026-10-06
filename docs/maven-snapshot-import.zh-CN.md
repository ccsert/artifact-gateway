# Maven SNAPSHOT 历史导入

[English](maven-snapshot-import.md) | [文档索引](README.zh-CN.md)

`gateway snapshot-import` 是 **Unreleased** 的运维命令，用于冻结的离线 Maven SNAPSHOT
bundle。已发布的 v0.6.1 没有此能力。它保留准入构建的源 timestamp/build 文件名、
extension/classifier、完整主资产字节及版本 metadata 原文。每一对 metadata 都保持源
选择，包括选旧构建或不同 classifier 各选不同构建。普通 Maven PUT/session 发布仍
分配新的本地身份，不能用来原样保留历史。

本实现对应父项 [#199](https://github.com/ccsert/artifact-gateway/issues/199) 下的
[#257](https://github.com/ccsert/artifact-gateway/issues/257)。只读客户端验证
[#204](https://github.com/ccsert/artifact-gateway/issues/204) 与通用迁移 ledger
[#205](https://github.com/ccsert/artifact-gateway/issues/205) 仍是独立工作。命令不抓取源
资产，不证明完整依赖闭包，不创建 Repository、发现凭据、启动服务或修改生产配置。

## 准入契约

- 固定 `manifest.json` SHA-256 与不含敏感信息的 source ID。`complete:true` 是操作者
  对冻结源清单完整性的声明；源采集和完整性证据需另外取得。所有列出的主资产与
  metadata 都流式核验完整字节。
- GAV 必须以 `-SNAPSHOT` 结尾。构建身份是 **timestamp 加 build number**，不能只看
  编号。同编号不同时间、同时间不同编号均保留。路径、POM GAV（含父 POM 中字面继承
  group/version）、packaging 与必需主资产必须一致。支持 packaging：`jar`、
  `maven-plugin`、`maven-archetype`、`ejb`、`bundle`、`war`、`ear`、`rar`、`zip`、`pom`。
  [官方 archetype packaging](https://maven.apache.org/archetype/archetype-packaging/)
  映射为必需的主 JAR；classifier JAR 不能代替主 JAR。自定义 packaging 或未解析的
  POM 表达式被拒绝，不自动修复。
- 版本 metadata 必须通过唯一 extension/classifier 的 `snapshotVersions` 明确指向
  已准入 timestamp/build。Maven 也支持只有全局 timestamp/build 的旧式 metadata；
  **v1 暂不支持这种表示**，报告不支持。漏写 `metadata` 字段被拒绝。显式
  `metadata:null` 声明源无当前版本 metadata：历史 URL 可读，版本 metadata 与规范
  `-SNAPSHOT` 别名返回 404。
- 缺主文件、POM/路径不一致、未知字段、重复 JSON 字段、重复 XML 身份字段、符号链接、
  不安全路径、字节变化及无效当前指向，会在目标写入前拒绝整个 bundle，并单列异常
  GAV。若暂缓异常资产，须在 **apply 前** 的已审阅清单中按 **整个 GAV** 明确排除并
  说明原因；同一 GAV 的正常历史也一起暂缓。排除是清单决策，不自动创建原生安全隔离
  记录。不通过合成资产、POM、metadata 指向或缺失主文件修复异常。
- 主文件、签名及版本 metadata 原文使用 SHA-256 身份。源 checksum sidecar 不作为
  manifest 主文件；命令从核验字节派生小写 MD5/SHA-1/SHA-256/SHA-512 sidecar，不声称
  保留源 sidecar 空白或大小写的文本序列化。
- 目标须为已存在、active 的 Maven Hosted Repository，retention **必须停用**。
  预检、预留和提交在与 retention 更新相同的 Repository 锁下复查，命令不改策略。
  制品 `createdAt` 使用源 timestamp，checkpoint 时间记录实际导入。验收后重新开启
  年龄/数量 retention 会删除仍受保护的历史及源选中的当前构建。显式接管后，整个
  Repository 禁止开启 retention；已形成的旧任务也须在删除事务内复验策略和接管状态。

manifest 为严格 version-1 JSON，最多 8 MiB。POM 最多 16 MiB，metadata 最多 4 MiB，
单个主文件最多 1 TiB。整数 size 与 SHA-256 digest 必填，零字节文件也一样。至少须有
一个准入 GAV。操作者主机支持 Linux/macOS。每次主文件上传先复制到匿名私有临时
spool，在同一次复制中核验 hash 后才写对象；除目标容量外，还须单独预留最大主文件加
headroom 的本地临时空间。以下为 **模板**，须替换为实际测量的字节数和摘要，并列出全部准入历史。

```json
{
  "schemaVersion": 1,
  "sourceId": "frozen-source-20261006",
  "complete": true,
  "coordinates": [{
    "coordinate": "org.example:widget:1.0-SNAPSHOT",
    "builds": [{
      "timestamp": "20260101.000000",
      "buildNumber": 7,
      "files": [
        {"path": "org/example/widget/1.0-SNAPSHOT/widget-1.0-20260101.000000-7.pom", "digest": "sha256:<64 lowercase hex>", "size": 123},
        {"path": "org/example/widget/1.0-SNAPSHOT/widget-1.0-20260101.000000-7.jar", "digest": "sha256:<64 lowercase hex>", "size": 456}
      ]
    }],
    "metadata": {"path": "org/example/widget/1.0-SNAPSHOT/maven-metadata.xml", "digest": "sha256:<64 lowercase hex>", "size": 789}
  }],
  "excluded": [{"coordinate": "org.example:broken:1.0-SNAPSHOT", "reason": "missing-main-artifact"}]
}
```

文件必须放在 bundle 目录内的相同相对路径。冻结 bundle 防止修改，并取得源完整字节
覆盖证据；此前抽样核验的一部分不能作为完整导入证据。采集 URL、私有坐标、原始资产、
凭据和操作证据必须保留在公开 issue、PR 和 Git 仓库以外。

## 操作顺序

1. 通过既有管理能力建立并审阅明确的源到目标 Repository 映射，先用隔离目标演练。
   真正 apply 前，按[恢复手册](recovery-runbook.zh-CN.md)完成已验证的 PostgreSQL/S3
   配对备份与完整写入者范围。连接目标的全部 Gateway API、reader、scheduler、worker
   须运行包含导入器和 migration `000141`/`000142` 的同一 revision，并通过既有流程应用
   migration。**不能在旧新版本混跑期间导入**。旧 binary 不识别预留 checkpoint 和
   原解析选择，单纯添加 schema 不足以支持导入。
2. 验收期间保持目标 retention 停用。使用已有特权操作者的数据库/S3 凭据引用；命令
   不授权。数据库身份读取 `pg_control_system()` 与 database name；权限不足会拒绝，
   不请求或授予新权限。
3. 固定 manifest 精确摘要并运行源核验。`verify` 不加载目标配置，不连接源或目标：

   ```sh
   umask 077
   manifest_digest="sha256:$(shasum -a 256 /private/bundle/manifest.json | awk '{print $1}')"
   /path/to/import-capable/gateway snapshot-import verify \
     --bundle /private/bundle --manifest-sha256 "$manifest_digest" > /private/verify.json
   ```

4. 写入 mode `0600` 的显式普通文件 target spec。它只包含已有环境变量 **名字**，不含
   秘密值；不发现 runtime `.env`、Kubernetes 或 Jenkins 凭据。示例模板：

   ```json
   {
     "targetId": "reviewed-snapshot-target",
     "repositoryId": "11111111-1111-4111-8111-111111111111",
     "actor": "migration-operator",
     "databaseUrlEnv": "MIGRATION_DATABASE_URL",
     "s3Endpoint": "https://s3.example.invalid",
     "s3Bucket": "reviewed-artifact-bucket",
     "s3AccessKeyEnv": "MIGRATION_S3_ACCESS_KEY",
     "s3SecretKeyEnv": "MIGRATION_S3_SECRET_KEY"
   }
   ```

   endpoint 支持 HTTP/HTTPS，不能带 userinfo、query、fragment 或路径。actor 是可追责
   的操作者标签，不能替代数据库/S3 授权。通过已批准的运维流程获取已有值。

5. 运行目标 dry-run。只读检查 Repository/checkpoint 冲突、已有物理对象完整字节及
   bucket 可用性，不创建对象、session、checkpoint、metadata 或 bucket：

   ```sh
   /path/to/import-capable/gateway snapshot-import dry-run \
     --bundle /private/bundle --manifest-sha256 "$manifest_digest" \
     --spec /private/target.json > /private/dry-run.json
   ```

   `targetBinding` 是实际 PostgreSQL cluster/database 与规范化 S3 endpoint/bucket
   的摘要。即使 `targetId` 相同，换实际目标也会拒绝；同目标凭据轮换允许。
6. 根据真实逻辑用量/quota、对象/引用成员关系、物理存储池空闲字节、临时上传/下载、
   backup/restore 与 headroom 证据生成新鲜的私有[容量计划](migration-capacity-preflight.zh-CN.md)。
   `inventoryId = manifestDigest`；plan 与 snapshot 的 `targetId` 都等于 dry-run 的
   `targetBinding`。精确复制其 `capacityReferences`，包括派生 checksum、metadata、
   字节数及 Repository ID。缺失、过期、不足、不匹配或 unknown 的计划均拒绝 apply。
   未知物理容量必须保持 unknown。等待锁后、每个 GAV 预留前再次检查时效。只读评估：

   ```sh
   /path/to/import-capable/gateway preflight capacity --input /private/capacity.json --format json
   ```

7. 使用同一冻结 bundle 与绑定的 sufficient 容量计划 apply：

   ```sh
   /path/to/import-capable/gateway snapshot-import apply \
     --bundle /private/bundle --manifest-sha256 "$manifest_digest" \
     --spec /private/target.json --capacity-plan /private/capacity.json > /private/apply.json
   ```

   保留 manifest/hash、私有 spec、源完整性证据、容量证据及每次 JSON 报告。切换前在
   已审阅目标范围核验历史与源选中当前 HTTP GET/HEAD URL 及客户端。本变更的 fixtures
   都是合成的，不证明私有源清单、生产目标或完整依赖闭包。

## 提交、恢复与证据

PostgreSQL `native_maven_snapshot_imports` checkpoint 持久绑定 source ID、manifest
SHA-256、Repository、target ID/binding 与完整 GAV 计划。复用原生 publish session、
object intent、reference、quota trigger 与生命周期回收器。导入按 GAV 串行；导入 I/O
与 GC 删除共用对象锁。任一 live upload 都保护去重对象，即便 intent 原属另一 session。
有效 GC claim 会拒绝恢复；过期 claim 在该锁内重置，旧 GC job 不能删除已恢复字节。

全部主/派生字节上传或核验完成前 GAV 不可见；之后所有 artifact/asset、精确 metadata
selector、session 和一条 `maven.snapshot.import` 审计在事务中原子提交。已有 GAV
（含已 tombstone 历史）、session、path 或不同 checkpoint 均拒绝，没有 force/overwrite。
导入 GAV 默认持续预留，普通 Maven PUT/session 发布返回 `409 snapshot_import_reserved`。
显式接管仅开启普通带 timestamp 的 Maven 部署；原导入 session 和原生 session API 继续受保护。
制品列表/搜索暴露 `sourceTimestamp`/`sourceBuildNumber`；既有 `buildNumber` 仍是用于
cursor/browse 的唯一 **本地发布序列**，不能当作源编号。

提交前失败不会暴露部分 GAV；此前已提交 GAV 的历史与解析保持不变。用 **相同** 源、
manifest、目标与新鲜容量证据重试：staged session 续期 24 小时，缺字节可重新上传，
已提交 GAV 只核验不重发布。提交响应丢失通过 checkpoint 和相同输入重试确认。不能
改 manifest、删 checkpoint 或重发布绕过冲突。导入后降级旧 binary 须使用已验证的
导入前配对恢复；删 schema 列/checkpoint 不是支持的回滚。

报告为 schema version 1，包含 `checkedAt`、固定身份、独立 `rejected`/`excluded`、
各 GAV session/state/reason 和数量。`verify` 返回 `source-verified`，dry-run 返回
`ready`；apply 仅在目标完整字节与可见主文件路径匹配后返回 `verified`。`partial` 保留
已完成、失败和待处理状态；已确认提交但读回失败仍为 `committed`，不能算 `verified`。
counts 是互斥的当前状态统计，不能当作通用迁移 ledger 完成。退出码 0 表示本动作成功，
1 表示操作失败/拒绝，2 表示参数/spec/报告失败。保存 stdout 时也必须检查退出码。

审计可按目标 Repository 名检索，evidence 保存 Repository ID、manifest/source/session
和 target binding。retention/tombstone 或配置的安全隔离可隐藏导入构建。源选中构建
隐藏后不自动选择兄弟历史：不可用当前 metadata 返回 404，隔离资产 GET/HEAD 依既有
策略拒绝，Group 同样适用。

## 显式接管并继续 Maven 发布

历史验收后，保留同一冻结 bundle、manifest hash 和私有 target spec。接管范围严格等于
bundle 中的 GAV，使用已有特权操作者；普通 Repository writer 不能授权接管。预检核验
所有导入字节、引用、源构建可见性及既有 quarantine 读策略。目标须为 active Maven
Hosted、普通直接发布模式且 retention 停用。接管不写对象、不发布构建、不改源 metadata
和 aliases，因此不需要容量 plan。只读数据库 session 或缺权限无法完成授权。

```sh
/path/to/import-capable/gateway snapshot-import takeover \
  --bundle /private/bundle --manifest-sha256 "$manifest_digest" \
  --spec /private/target.json --idempotency-key reviewed-takeover-1 \
  --dry-run > /private/takeover-dry-run.json

/path/to/import-capable/gateway snapshot-import takeover \
  --bundle /private/bundle --manifest-sha256 "$manifest_digest" \
  --spec /private/target.json --idempotency-key reviewed-takeover-1 \
  > /private/takeover.json
```

dry-run 报告 `takeover-ready`，成功报告 `writable`。每个 GAV 原子记录首次 key、actor、
时间及一次 `maven.snapshot.takeover` 审计。丢响应或部分失败时，使用同身份/key 重试；
换 key 或物理目标会冲突。保留每次报告。接管后，目标侧 import `dry-run`/`apply` 永久
返回 `snapshot_import_taken_over`，源侧 `verify` 仍可运行。不能删除 checkpoint 或换
manifest 追加历史。

在审阅的合成/演练 POM 中配置目标 `distributionManagement` 的 server id/URL、匹配
Maven settings 与已有 writer 凭据，然后仍执行普通命令：

```sh
mvn deploy -DskipTests -f /path/to/pom.xml -s /private/settings.xml -B -ntp
```

Gateway 在规范化文件名前持久化客户端 timestamp/build 收据；中断后新 timestamp 的重跑
形成独立 session，并发收据不混用文件。版本 metadata PUT 只完成该 actor 对应的、未过期
且 POM/主制品/全部声明主资产已完整校验的部署；GA metadata 仅作辅助。新 pair 更新独立
live alias overlay，未重新发布的 classifier/extension 保持原来选定的版本，含 `jar.asc`
等多段 extension。失败或不完整部署不改变 source/上一次完成部署的解析；迟到的低编号
完成只能成为历史，不能回退 current。新指向的 checksum 同时派生并校验。

服务端编号越过所有源 build number 与本地序列，包括 tombstone；不能从源选中的旧编号
起算。同编号不同 timestamp 继续独立保留。首次客户端从旧 metadata 推导的编号可能与
服务端分配不同，应解析 Gateway 结果 metadata，不假定 PUT 文件名原样保留。int32
序列耗尽会拒绝接管。导入历史和已完成新构建的 namespace（含 sidecar、新 classifier、
tombstone）拒绝 PUT；已关闭的客户端收据也冲突，不能覆盖固定历史。

验收须包含两次普通部署、逐次清空制品缓存后的重新解析，以及新 metadata/alias/checksum
和所有固定历史 GET/HEAD/checksum URL。接管后整个 Repository 禁止开启 retention，
两个旧 worker 在删除事务内复验版本、enabled 与接管收据。显式管理员删除、隔离与权限
保持既有语义；隐藏选定路径不回退到兄弟构建。回滚使用配对备份恢复，不删除收据。

## 可复现检查

公开合成 fixture 包含三个历史、重复源编号、classifier/extension 与旧/混合当前选择。
回归还覆盖同 timestamp 下 build 1/10/70 与相同 POM、缺文件、metadata/POM 不一致、
显式不存在、整 GAV 排除、源/目标字节漂移、多 GAV 部分失败、并发重试、提交响应丢失、
retention 准入、普通同 actor PUT、隔离，以及真实 PostgreSQL/RustFS CLI 重放和过期
claim 恢复。

```sh
go test ./internal/snapshotimport ./internal/repository ./internal/protocol/maven ./internal/app ./cmd/gateway
make integration-test
make native-maven-e2e
make ci-local-full
```

门禁使用隔离本地 fixture。本实现工作不执行生产导入或发版。
