# Helm HTTP 预览

[English](helm-hosted-preview.md) · [文档首页](README.zh-CN.md)

传统 HTTP Helm Hosted/Proxy/Group 是 #290/#291 下默认关闭的预览，提供 `index.yaml`、原始 chart/
provenance 下载、明确的 multipart 上传 API、有界浏览、仓库授权及容量预留。它不进入
正式 Format 枚举、格式档案、仓库列表、active capabilities 或 Console 选择器。现有
OCI chart 存储属于另一协议。预览墓碑/恢复和隔离治理由 #457 跟踪；其余生命周期与正式准入仍属于 #292。

## 启用与创建

通过正常迁移工具应用只向前追加的 SQL，并配置既有 PostgreSQL 及经过验证的 S3/RustFS
存储端口。显式设置 `GATEWAY_HELM_PREVIEW_ENABLED=true`；无效布尔值会阻止启动。
在启用 `retention` 工作的 worker/standalone 节点上启用同一预览，才能回收过期上传对象。
若限制了 `GATEWAY_WORKER_FORMATS`，其中必须包含 `helm`。API-only 节点不启动上传回收。
运行诊断把该回收器放在 `previewWorkerFormats:["helm"]`，不会加入正式 `workerFormats` 枚举。

随附 Compose 从 `.env` 转发 `GATEWAY_HELM_PREVIEW_ENABLED`，默认值为 `false`。
如需提供或维护此预览，先设为 `true`，再重建相应的 API 和 worker 服务。

管理员通过 `POST /api/v2/previews/helm/repositories` 提交
`{"name":"charts-internal"}` 创建私有 Hosted 预览。保存返回的 ID 及 Gateway 相对路径
`sourceUrl`；正式仓库目录不会列出预览。可选的 `Idempotency-Key` 按调用者隔离，同一创建
请求可重复读取结果，修改请求体返回 409。通过
`GET /api/v2/previews/helm/repositories/{id}` 读回创建结果。Proxy 与 Group 的配置见下文。

既有仓库授权 API 可绑定该 ID。资源前缀匹配区分大小写的 chart 名称；同一 chart 的所有
版本使用该名称作为资源。全量替换、单条写入与模板应用接受空前缀（整个仓库），或以
小写 ASCII 字母或数字开头、最长 63 字符且只含小写字母、数字和连字符的前缀；版本与
URL 路径被拒绝。此授权不会将 Helm 开放到正式制品搜索。
索引和预览浏览接受有效的前缀授权，再逐项过滤 chart；下载与
上传检查完整 chart 名称。读取接受 Bearer 或 HTTP Basic，Basic 的密码使用 Gateway 凭据，
用户名不能选择身份。管理上传使用 Bearer。显式无效或已撤销凭据始终被拒绝。创建结果为
默认私有；匿名读取同时要求显式 `anonymousRead:true` 和启用全局匿名策略。Group 匿名读取还要求每个成员允许匿名读取。

## 一次发布完整闭包

传统仓库没有统一的 `helm push` 协议。使用这里明确声明的 Gateway 专用接口：

```text
PUT /api/v2/previews/helm/repositories/{id}/charts/{chartName}/{version}
Content-Type: multipart/form-data
Authorization: Bearer <Gateway credential>
Idempotency-Key: <可选的调用者级重放键>
```

必须包含名为 `chart` 的文件 part，文件名严格为 `{name}-{version}.tgz`。可选的
`provenance` 文件 part 必须名为 `{name}-{version}.tgz.prov`，两者必须在同一请求中提交。
通过生成的 Console 客户端上传时，使用具有上述准确名称的 `File`，确保 multipart 保留文件名。
`Chart.yaml` 决定身份，并且必须与路径完全一致：名称仅允许小写 ASCII 字母、数字和连字符，
最多 63 字符；版本为严格 SemVer，允许预发布和构建元数据，不自动把 `1.2` 或 `v1.2.3`
转换为其他版本。

整个闭包不可变。原始 chart/provenance 完全相同的重放返回 201；更改任一字节流、去掉签名
或之后补签均返回 409。添加或替换签名应发布新版本。畸形 multipart、身份不一致、不安全/
不完整归档及无效 provenance 返回 400；压缩上传超界返回 413；配额冲突返回 409；已发布
对象不可用或损坏返回 503，不会把损坏字节发送给客户端。

chart 压缩包上限为 64 MiB，provenance 和原始 `Chart.yaml` 各为 1 MiB，持久化元数据上限
为 256 KiB。包括打包依赖在内的解压 tar 总量上限为 256 MiB、条目总数 10,000、打包依赖
嵌套深度八层。链接、设备、重复或穿越路径、多 gzip member、错误 gzip 校验和、缺失 tar
结束标记、YAML alias、重复映射键和额外 YAML 文档都会被拒绝。归档条目不会解压到文件系统；
发布过程不会访问依赖 URL。

provenance 必须是原始 OpenPGP clearsigned Helm 文档，包含与 chart 相同的元数据，并以
准确文件名绑定完整 chart 的 SHA-256。Gateway 校验这一绑定及签名 packet 结构、保存原始
字节，但不声明发布者可信。消费者必须通过显式信任的公钥环验签。Gateway 不接收或复制签名私钥。

对象写入前，持久化的一小时 session 预留 chart 与 provenance 的总大小。提交时完整重开
两个对象，检查 size/SHA-256、重新校验身份与签名文档绑定，再事务提交发布记录和审计。
未提交的上传不会进入任何索引或浏览结果。响应丢失或进程重启后重试完全相同的请求；已过期
预留对原重放键返回 410，应使用新键重新开始。过期上传回收与发布共享锁，且使用 session
独占对象键，不能误删替代发布；回收可重复执行。

## 消费与查看

在独立的 Helm 仓库配置中把返回位置配置为
`https://gateway.example/helm/charts-internal`，索引位于 `index.yaml`。条目包含原始 chart
元数据、完整包 SHA-256 及 `charts/owned/1.2.3/owned-1.2.3.tgz` 这样的相对 URL。
追加 `.prov` 读取同一已提交闭包的 provenance。索引不会提供上游或其他来源 URL。
索引是确定且经过授权过滤的快照；发布记录不变时 YAML 和 ETag 不变。GET/HEAD 下载支持
条件请求与 Range，并在发送范围字节前校验完整持久化对象。

`GET /api/v2/previews/helm/repositories/{id}/charts` 从一个一致快照返回最多 4096 个
chart；超过上限返回 422，不静默截断。`GET .../charts/{name}/{version}` 返回身份、摘要、
下载位置及明确的 `cached` 状态。Hosted 和完整缓存的 Proxy 闭包返回 `cached:true`，以及
真实的 `size`、`provenanceSize`；只有完整闭包的 provenance 大小为零才表示无签名。
尚未缓存的 Proxy 元数据返回 `cached:false`，省略未知大小及 provenance 字段。列表或详情
读取可能获取上游索引，但不会隐式下载资产或改变字节用量。
`GET .../{id}/capacity` 读取已提交 chart/provenance 的用量；仓库管理员
对该路径 PUT `{"quotaBytes":N}` 设置配额，零表示不限。并发待提交预留也占用同一配额。

## Proxy 与 Group 来源快照

管理员通过同一预览仓库 POST 创建 Proxy，传入 `type:"proxy"`、明确指向索引的 HTTPS
`endpoint`、可选 `allowedHosts`（最多 32 个额外 HTTPS authority）、`egressMode`
（`direct` 或 `environment`）及可选 `upstreamAuth:{scheme,username,secret}`。
仅支持 `none`、`basic`、`bearer`。只写的上游秘密通过 `GATEWAY_SETTINGS_ENCRYPTION_KEY`
加密，与 Gateway 客户端凭据、签名私钥相互独立；响应只包含 scheme、可选用户名和
`credentialsConfigured`。对仓库 PUT 必须提交当前 `version` 才能替换配置。更改 endpoint/
allowlist 而保留来源凭据时，必须提供新 secret 或显式设置 `scheme:"none"`。

URL 必须使用 HTTPS，不能带 userinfo、query、fragment、转义/穿越路径或尾点主机名。
每个 chart URL 必须属于索引原点或显式允许的 authority。每次跳转独立执行 DNS/egress 检查；
跨原点后剥离上游凭据，即使之后跳回原原点也不会重新附加。客户端 Authorization、cookie
及代理凭据不会变成上游凭据。此预览要求每个索引条目恰好一个 URL，不支持 mirror、plugin
或含签名 query 的地址。

完整索引上限为 1 MiB/4096 个身份。YAML alias/anchor、重复或非字符串键、超过 64 层、
超过 100,000 节点、额外文档、重复 chart version 和非 SHA-256 摘要都直接失败。成功文档
有效 30 秒，过期后通过 ETag/Last-Modified 条件重验；只有 dial/DNS/deadline 不可用时可读取
旧成功文档。HTTP 拒绝、TLS/egress 失败、畸形内容和变化的摘要不能以旧缓存掩盖。
来源配置仍相同时可持续离线读取旧文档；配置变更进入新的缓存命名空间。

索引条目只指向
`/helm/{view}/snapshots/{snapshotId}/charts/{name}/{version}/{name}-{version}.tgz`，
追加 `.prov` 使用同一冻结条目。持久化快照固定来源仓库/version、原始元数据、完整 chart
摘要及单一资产 URL。首次 chart GET 获取完整有界归档与可选 `.prov`，验证身份、元数据、
size/SHA-256 和 provenance 绑定后，再进入既有配额预留/不可变发布事务。缺包、坏摘要、
授权失败或畸形 provenance 都不能 fallback。只有来源 provenance 的 404 才固定为无签名；
后补签必须使用新版本。部分写入不会可见，重启后可恢复；已核验闭包支持离线 GET/HEAD/Range，
并计入仓库配额。待提交闭包沿用一小时过期及回收器。来源元数据不属于正式制品身份。

通过 `POST /api/v2/previews/helm/groups` 提交
`{name,members:[repositoryId,...],anonymousRead?}` 创建 Group。允许 1–32 个有序的直接
Hosted/Proxy 成员，拒绝嵌套 Group、重复成员与环。PUT `/groups/{id}` 带当前 `version`
替换顺序，GET 读取配置。发现数据前，同一调用者必须通过每个当前成员的读取权限；chart
前缀授权按各成员可读集合的交集过滤。首次进入索引的 name/version 建立持久来源归属；
成员重排不能换源，任一上游或成员错误都阻止完整索引。如果原来源被移除、配置/version
改变、不再出现在当前来源索引或声明不同字节，则返回 409，不选择后备成员。只要来源仍是
已授权的活动成员且配置版本相同，之前冻结的缓存字节不随上游索引变化。管理员可创建新
Group 以显式建立新来源；此预览不会在轮换后自动重绑定已有来源。

`GET /api/v2/previews/helm/groups/{id}/sources/{chartName}/{version}` 只读已经建立的归属，
返回来源仓库/version、digest、可选上游 URL 和完整闭包是否已缓存；逐个检查成员的 chart
读取权限，不隐式获取索引或下载资产。尚未选源的身份返回 404。快照只追加，每个 view 最多
128 份，每份最多 4096 条/8 MiB 序列化元数据，新增超界返回 422；预览没有快照修剪 API。
Proxy/Group 协议发布返回 405；已缓存 Proxy 的治理使用下文独立管理员接口。不可变冲突或来源版本变化返回 409，上游缺包/拒绝/坏内容返回
502，缓存边界返回 422，临时存储/传输失败返回 503。正式生命周期、扫描、分发和可见缓存
淘汰仍由 #292 承接。

## 保留 chart 的治理

启用预览前，应用迁移 `000183_native_helm_governance.sql`。仓库管理员通过
`GET /api/v2/previews/helm/repositories/{id}/charts/{name}/{version}/governance`
查看已提交 Hosted 或已缓存 Proxy 的状态。结果包含不可变 `digest`、独立的
`governanceVersion`、`tombstoned`、`quarantined`，以及最近操作者、理由和时间。
尚未缓存的上游索引元数据在此返回 404；来源索引不等于持久 publication，不能伪造治理对象。

对相同治理 URL 执行 POST，提交：

```json
{
  "expectedDigest": "sha256:<64 个小写十六进制字符>",
  "expectedGovernanceVersion": "1",
  "action": "delete",
  "reason": "已批准移除此 chart"
}
```

动作仅为 `delete`、`restore`、`quarantine`、`release`。不可变摘要和当前治理版本必须同时
匹配，过期请求返回 409。实际状态变化会推进治理版本，并在同一事务记录操作者和理由审计。
若动作已满足且提交当前版本，则以 200 返回原状态，版本和审计条数不变；重用旧版本仍然
冲突。治理检查完整 chart 名称的仓库管理员权限，仅有读取或写入授权不足以操作。

`delete` 创建墓碑，使该版本从 `index.yaml`、预览列表、chart 详情和固定来源解释中隐藏。
原 chart、provenance URL 和旧快照的 GET/HEAD、Range、条件请求均返回 404；不增加原生
Helm DELETE 别名。Group 先选择持久来源，再应用治理；成员重排、上游删包、离线/过期
索引或另一成员的相同身份都不能复活隐藏来源。墓碑不会改写字节、释放容量、来源绑定或
对象引用；原上传的完全相同重放也不会清除墓碑。

`quarantine` 记录独立隔离标记。仓库策略通过
`GET /api/v2/previews/helm/repositories/{id}/quarantine-read-policy` 读取，默认为
`{"version":"1","enabled":false}`。PUT 提交
`{"expectedVersion":"1","enabled":true}` 后，同一发现和资产路径会拒绝隔离版本。
该策略版本独立于仓库配置版本与 chart 治理版本。关闭策略后墓碑仍然阻止读取；恢复墓碑
不会清除隔离标记。策略状态与审计也在同一事务提交，过期 CAS 被拒绝；当前状态的无变化
写入不会推进版本或增加审计。

`restore` 或 `release` 放宽标记前，Gateway 在 publication 锁内完整重开保留的 chart/
provenance，检查原 size/SHA-256、规范名称/版本、`Chart.yaml`、持久元数据及 provenance
绑定。字节或元数据损坏返回 502；对象缺失或存储不可用返回 503，治理状态不变。把已启用
的隔离拦截关闭之前，也会完整重验所有即将恢复可见、没有墓碑的隔离 chart；库存超出
预览上限返回 422。治理动作与该策略放宽串行，避免并发隔离绕过校验。读取始终重查
持久状态，不信任旧浏览结果或快照。PostgreSQL 行锁与事务使治理、独立策略、保留容量
和审计在节点/进程重启后保持一致。这些属于预览专属控制，不等于正式生命周期准入或物理删除。

## 可重复验收与剩余范围

准备 Docker、仓库要求的 Go 工具链、curl 和 Node.js 24 后，执行 `make native-helm-e2e`。
验收还会通过生成的请求类型与 multipart 序列化器，经真实 HTTP 上传命名文件。
验收下载官方 Helm 3.19.0 对应平台归档，验证代码中
固定的 SHA-256；只使用独占、默认 bridge 网络上的 PostgreSQL/RustFS 容器、空 Docker 凭据、
空且只指向目标的 Helm 配置/缓存、合成签名 chart 及消费者独占公钥环。场景经过完整 Gateway
路由，覆盖带签名 `helm package`、声明的上传/重放、数据库和 handler 重启、`repo add/update`、
冷 `pull --verify`、独立 `verify`、私有上游缓存、混合 Hosted/Proxy Group 冲突及重排、
上游离线后的重启读取、权限拒绝、真实凭据轮换/撤销、旧缓存与空缓存客户端的墓碑/隔离
拒绝、恢复前完整闭包校验，以及治理在 PostgreSQL/handler 重启后的持久读回。压缩后超过 1 MiB 的 chart
还验证真实管理上传请求不会受到通用 JSON body 上限干扰。

Helm 3.19.0 的 `install --dry-run=client` 仍会在渲染前检查 Kubernetes 可达性。本验收提供
独占、仅接受 GET 的 discovery fixture，任何写请求都使测试失败，并记录精确客户端 Git
commit 与对应上游源码分支。该场景证明冷 chart 消费与 dry-run 渲染，不证明真实 Kubernetes
集群安装。

`output/native-helm-e2e/<run>/` 保存 source/head/tree 哈希、固定客户端来源、原始 JSON
测试事件、明确的 mandatory 清单、required/summary 结果、原始 CLI 输出、上游源码证据
及精确自有资源清理结果。mandatory 缺失或 skip 均使验收失败。

[Hosted 持久复制](helm-replication-preview.zh-CN.md) 固定 chart/provenance 计划，按当前
权限原子发布目标自有字节和逐项检查点；同一必需客户端门禁在源删除和服务重启后验证目标冷消费。

预览不提供 Proxy/Group 发布、Console 创建/上传/浏览、agctl 命令、可见 chart
保留策略、扫描、晋升、完整备份/升级验收或 Nexus Helm 迁移。这些仍属于 #292 或其迁移依赖。本交付不改变生产路由，不执行源迁移或切流。

协议依据：[Helm chart 仓库指南](https://helm.sh/docs/topics/chart_repository/)、
[Helm provenance](https://helm.sh/docs/topics/provenance/)、
[固定 Helm 3.19.0 release](https://github.com/helm/helm/releases/tag/v3.19.0)。
