# Helm Hosted 预览

[English](helm-hosted-preview.md) · [文档首页](README.zh-CN.md)

传统 HTTP Helm Hosted 是 #290 下默认关闭的预览，提供 `index.yaml`、原始 chart/
provenance 下载、明确的 multipart 上传 API、有界浏览、仓库授权及容量预留。它不进入
正式 Format 枚举、格式档案、仓库列表、active capabilities 或 Console 选择器。现有
OCI chart 存储属于另一协议。Proxy/Group 仍由 #291 跟踪，生命周期与正式准入仍由 #292 跟踪。

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
`GET /api/v2/previews/helm/repositories/{id}` 读回创建结果。Proxy 配置会被拒绝。

既有仓库授权 API 可绑定该 ID。资源前缀匹配区分大小写的 chart 名称；同一 chart 的所有
版本使用该名称作为资源。全量替换、单条写入与模板应用接受空前缀（整个仓库），或以
小写 ASCII 字母或数字开头、最长 63 字符且只含小写字母、数字和连字符的前缀；版本与
URL 路径被拒绝。此授权不会将 Helm 开放到正式制品搜索。
索引和预览浏览接受有效的前缀授权，再逐项过滤 chart；下载与
上传检查完整 chart 名称。读取接受 Bearer 或 HTTP Basic，Basic 的密码使用 Gateway 凭据，
用户名不能选择身份。管理上传使用 Bearer。显式无效或已撤销凭据始终被拒绝。创建结果为
私有仓库，启用全局匿名策略也不会自动公开新的预览。

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

`GET /api/v2/previews/helm/repositories/{id}/charts` 从一个一致快照返回最多 4096 个已提交
chart；超过上限返回 422，不静默截断。`GET .../charts/{name}/{version}` 返回身份、大小、
摘要和下载位置。`GET .../{id}/capacity` 读取已提交 chart/provenance 的用量；仓库管理员
对该路径 PUT `{"quotaBytes":N}` 设置配额，零表示不限。并发待提交预留也占用同一配额。

## 可重复验收与剩余范围

准备 Docker、仓库要求的 Go 工具链、curl 和 Node.js 24 后，执行 `make native-helm-e2e`。
验收还会通过生成的请求类型与 multipart 序列化器，经真实 HTTP 上传命名文件。
验收下载官方 Helm 3.19.0 对应平台归档，验证代码中
固定的 SHA-256；只使用独占、默认 bridge 网络上的 PostgreSQL/RustFS 容器、空 Docker 凭据、
空且只指向目标的 Helm 配置/缓存、合成签名 chart 及消费者独占公钥环。场景经过完整 Gateway
路由，覆盖带签名 `helm package`、声明的上传/重放、数据库和 handler 重启、`repo add/update`、
冷 `pull --verify`、独立 `verify`、权限拒绝以及真实凭据轮换/撤销。压缩后超过 1 MiB 的 chart
还验证真实管理上传请求不会受到通用 JSON body 上限干扰。

Helm 3.19.0 的 `install --dry-run=client` 仍会在渲染前检查 Kubernetes 可达性。本验收提供
独占、仅接受 GET 的 discovery fixture，任何写请求都使测试失败，并记录精确客户端 Git
commit 与对应上游源码分支。该场景证明冷 chart 消费与 dry-run 渲染，不证明真实 Kubernetes
集群安装。

`output/native-helm-e2e/<run>/` 保存 source/head/tree 哈希、固定客户端来源、原始 JSON
测试事件、明确的 mandatory 清单、required/summary 结果、原始 CLI 输出、上游源码证据
及精确自有资源清理结果。mandatory 缺失或 skip 均使验收失败。

预览不提供 Proxy/Group 读写、Console 创建/上传/浏览、agctl 命令、墓碑/恢复、可见 chart
保留策略、隔离/扫描、晋升/复制、完整备份/升级验收或 Nexus Helm 迁移。这些仍属于 #291/
#292 或其迁移依赖。本交付不改变生产路由，不执行源迁移或切流。

协议依据：[Helm chart 仓库指南](https://helm.sh/docs/topics/chart_repository/)、
[Helm provenance](https://helm.sh/docs/topics/provenance/)、
[固定 Helm 3.19.0 release](https://github.com/helm/helm/releases/tag/v3.19.0)。
