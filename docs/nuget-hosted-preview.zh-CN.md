# NuGet Hosted 预览

[English](nuget-hosted-preview.md) · [NuGet 路线图](nuget-roadmap.zh-CN.md) · [文档索引](README.zh-CN.md)

NuGet Hosted 必须显式开启。它在 `/api/v2/previews/nuget` 下使用独立的仓库和
发布 DTO，原生协议入口为 `/nuget/{name}`。正式格式目录、正式仓库创建与公开
目录、Console 格式选择器仍不公开 NuGet。独立启用的
[Proxy/Group 预览](nuget-proxy-group-preview.zh-CN.md) 扩展原生读取。
[#449](https://github.com/ccsert/artifact-gateway/issues/449) 治理子切片增加取消列出/重新列出、
保留墓碑和隔离读取拦截。symbols 发布、签名校验、扫描、晋级/复制、保留策略、
回收、Console 与完整治理准入仍由 #289 跟踪。
预览 API 暂无 `agctl` 命令。

## 开启与建仓

使用标准迁移程序应用追加的 SQL，配置现有 PostgreSQL 和经过校验的 S3/RustFS
端口，在 API 进程设置 `GATEWAY_NUGET_PREVIEW_ENABLED=true`。默认关闭，
无效布尔值会拒绝启动。API-only 节点不启动清理 worker；需要在允许 retention
工作的 worker/standalone 节点同时开启预览，才能清理过期上传。排除 NuGet 的
worker 格式过滤也会排除这个预览清理器。
诊断与运行节点列表通过 `previewWorkerFormats:["nuget"]` 单独显示它，
`workerFormats` 继续使用正式准入的格式枚举。

管理员调用 `POST /api/v2/previews/nuget/repositories`，例如提交
`{"name":"dotnet-internal","anonymousRead":false}`。回执包含仓库 ID、
`preview:true` 和绝对 `sourceUrl`。同一身份使用相同 `Idempotency-Key` 重试会
返回原仓库；改变请求内容返回 409。请保存回执，正式仓库列表不展示预览仓库。
可用 `GET /api/v2/previews/nuget/repositories/{id}` 读回。

角色、账户状态、grant 使用 Gateway 现有授权器。现有 grant/role API 可以绑定
预览仓库 ID。资源前缀匹配规范化的小写包 ID，例如 `acme.`。服务索引与搜索
发现允许有效的包名前缀授权，每个包的读写仍检查完整 ID，搜索结果逐包过滤。

原生 push 的 `X-NuGet-ApiKey` 是现有 Gateway 凭据。读取支持 Bearer，或将
Gateway 凭据作为密码的 HTTP Basic；Basic 用户名不能选择或冒充身份。使用
回执中的服务索引地址和独立 `NuGet.Config` 配置源，再用
`dotnet nuget push PACKAGE.nupkg --source SOURCE --api-key CREDENTIAL`
发布。显式无效或已撤销的凭据即使遇到匿名仓库也会被拒绝。匿名读取需要全局
策略与仓库配置同时开启。这里使用的是 Gateway 下游凭据，不是上游密码。

## 发布与故障恢复

规范身份由小写包 ID 与规范版本组成。模块复用现有 nupkg/nuspec 校验器，保存
原始 nuspec、完整压缩包 SHA-256、SHA-512、依赖组与版本区间。包最大 256 MiB，
nuspec 最大 1 MiB，持久元数据最大 256 KiB；浮动依赖版本、歧义和畸形元数据会
显式拒绝。

原生 PackagePublish 将预留、校验暂存与提交合成一次客户端操作。高级调用者可
使用独立授权的发布会话 API：携带 `Idempotency-Key` 预留，PUT 原始 nupkg 到
会话 `/package`，再 POST `/commit`。会话绑定发起身份和仓库。提交会重新完整
读取对象，核对摘要、身份与依赖元数据，然后在同一事务创建可见发布和审计。
预留、暂存内容不会出现在任何读取端点。相同内容的原生 push 返回第一次发布，
同一规范身份下的不同字节返回 409。

暂存响应丢失时，使用同一会话重传完全相同的字节。提交响应丢失或进程重启后，
读取会话并重试提交；已提交回执保留首次发布时间。重试预留、暂存或提交过期
会话返回 410，GET 回执仍可读。此时用新幂等键创建新会话。预留有效期一小时，
新会话使用独立不可变对象键。过期清理
与发布使用同一个锁，因此旧清理任务不能删除新的可见发布。清理标记持久化，
删除或标记失败会重试。重放已提交的 push 会保留原有列出、墓碑和隔离决策。

使用 GET/PUT `/api/v2/previews/nuget/repositories/{id}/capacity` 查看容量或设置
配额，PUT 需要仓库全局管理权限。`usedBytes` 与 `objectCount` 统计已提交内容，
准入还计算有效预留；PostgreSQL 提交和配额更新共享仓库锁。备份引用核验包含
已提交 NuGet 对象，排除仅暂存对象；元数据指纹包含两个表。这项引用覆盖本身
不等于已单独完成 NuGet 全灾难恢复验收。

## 列出状态与保留治理

Hosted `PackagePublish/2.0.0` 支持标准
`DELETE /nuget/{name}/api/v2/package/{id}/{version}` 取消列出，同一路径 POST
重新列出。两者需要包级写权限，达到目标状态返回 204；已加墓碑的版本返回 404。
官方 `dotnet nuget delete ID VERSION --source SOURCE --api-key CREDENTIAL`
使用此取消列出行为。取消列出后搜索和自动补全不再返回该版本；registration
保留该版本并标记 `listed:false`，flat-container 版本目录也保留它。精确版本的
GET/HEAD、nuspec、SHA-512、Range 与条件下载仍可用，重放 push 不会重新列出。

仓库管理员通过
`GET /api/v2/previews/nuget/repositories/{id}/packages/{packageId}/{version}/governance`
读取保留身份。同一路径 POST 提交完整不可变 `expectedDigest`、独立
`expectedGovernanceVersion`、操作（`unlist`、`relist`、`delete`、`restore`、
`quarantine`、`release`）和非空且不超过 2048 字符的 `reason`。回执包含
`listed`、`tombstoned`、`quarantined`、新 `governanceVersion` 以及操作者、时间
和原因。身份或版本变化返回 409，需要重新读回后再决策。状态和审计同事务提交；
精确无操作决策保留版本，不重复创建审计。

`delete` 创建保留墓碑，`restore` 只移除该墓碑；`quarantine`、`release` 独立改变
治理限制。release 和原生 relist 均不能恢复墓碑，restore 也不会释放隔离或重新列出。
治理保留不可变字节、发布会话、备份引用和已用容量，不释放配额、不允许相同坐标
替换字节，也不执行延迟回收。恢复、释放隔离和重新列出前会重新读取并校验保留
包字节；字节损坏时保留原决策。

仅管理员可访问 GET/PUT
`/api/v2/previews/nuget/repositories/{id}/quarantine-read-policy`。默认返回
`{"version":"1","enabled":false}`；PUT 必须包含 `expectedVersion` 和 `enabled`，
策略和审计同事务提交。开启后，隔离身份从 registration、搜索、补全、
flat-container 版本目录隐藏，并在处理 Range 和条件读取前拒绝所有精确表示的
GET/HEAD。墓碑始终使用同样的读取屏障，策略读取失败时关闭访问。策略关闭时，
隔离保留原生读取；释放隔离只移除自身限制。

Proxy 治理仅作用于已完成字节校验的本地缓存身份，未缓存的远程元数据不是治理
身份。取消列出/重新列出只支持 Hosted，Proxy 返回 405；其他治理操作保护本地
缓存身份及其远程元数据投影。Group 在固定规范成员上判断治理可见性，隐藏的
所有者不能回退到其他成员的包。这些操作不会开启预览或将 NuGet 加入正式格式
目录。迁移 `000182` 为追加迁移；旧二进制不理解其读取屏障，不能继续服务升级后
的 NuGet 预览。

## 原生读取与验收

服务索引声明 PackagePublish/2.0.0、PackageBaseAddress/3.0.0、gzip
RegistrationsBaseUrl/3.6.0，以及最小搜索、自动补全。Flat-container 包读取支持
GET/HEAD、Range、ETag/条件读取、原始 nuspec 与 SHA-512 sidecar。搜索支持
`q`、`skip`、`take`、`prerelease`，自动补全还支持 `id`。元数据枚举限制为
4096 个版本、8 MiB；超过限制返回 `nuget_preview_metadata_limit`，不会截断依赖
信息。响应中的仓库地址使用请求的外部主机和 HTTPS 方案（TLS 或部署的 HTTPS
转发标志）。

用 `make native-nuget-e2e` 运行隔离验收。脚本固定官方 .NET SDK 10.0.100 的
根索引摘要与平台子摘要，使用空 Docker 凭据配置和独立客户端目录、配置、缓存，
只在随机端口启动自己的 PostgreSQL/RustFS。检查包括原子可见性、配额并发、
上传/提交响应丢失、完整读回、清理互斥与备份引用。官方客户端会发布三层依赖
链，确认缺少传递依赖时失败，再用冷缓存只从目标源恢复；重启 Gateway、
PostgreSQL 和 RustFS 后再次冷恢复，最后停止目标，用已有缓存离线恢复和执行。
门禁还覆盖官方客户端取消列出后的精确冷恢复、隔离读取拦截或墓碑期间冷恢复失败、
三种服务重启后仍然拦截，以及显式治理恢复后读回原字节。
必需用例不允许失败或跳过。`output/native-nuget-e2e/<project>/` 保存源码哈希、
客户端/工具来源、JSON 结果及资源归零记录。本机验收、合并后 CI 和生产可用性
分别报告。

一手协议资料，读取日期 2026-10-20：[PackagePublish](https://learn.microsoft.com/en-us/nuget/api/package-publish-resource)、
[flat container](https://learn.microsoft.com/en-us/nuget/api/package-base-address-resource)、
[registration](https://learn.microsoft.com/en-us/nuget/api/registration-base-url-resource)、
[搜索](https://learn.microsoft.com/en-us/nuget/api/search-query-service-resource)、
[dotnet restore](https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-restore)。
