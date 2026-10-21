# NuGet 仓库路线图

[English](nuget-roadmap.md) | [文档索引](README.zh-CN.md)

## 状态与优先级

NuGet Hosted 发布与原生 V3 读取已通过 #287 提供显式启用的 [Hosted 预览](nuget-hosted-preview.zh-CN.md)，官方客户端门禁覆盖 push、传递依赖 restore、存储重启和离线执行。Cargo 已公开接纳，历史上先评估 Cargo 的 backlog 顺序不再适用。NuGet 是 Microsoft 支持的 .NET、Visual Studio 和 `dotnet` 包机制，V3 service index 也为私有仓库提供稳定发现边界。

N1 字节契约已实现：Gateway 能验证完整 `.nupkg` ZIP，读取根目录一个有界 `.nuspec`，拒绝歧义 XML 或归档路径，规范化 NuGet 版本，并派生大小写不敏感的规范身份。

预览新增独立 OpenAPI 管理与原生契约、持久发布 Session、配额预留和 V3 Hosted 读取。在 #289 治理与准入门禁通过前，NuGet 不进入正式格式目录、Console 选择器或正式仓库创建；[Proxy/Group 预览](nuget-proxy-group-preview.zh-CN.md) 已在相同默认关闭边界实现 #288，提供按源版本隔离的可验证缓存和规范包 ID/版本的持久来源绑定。

规范参考全部来自 Microsoft：

- [NuGet Server API](https://learn.microsoft.com/nuget/api/overview)
- [Package Content API](https://learn.microsoft.com/nuget/api/package-base-address-resource)
- [`.nuspec`](https://learn.microsoft.com/nuget/reference/nuspec)
- [版本规则](https://learn.microsoft.com/nuget/concepts/package-versioning)

## N1：不可变包与发布契约

- package ID/version 由 `.nupkg` 自身决定；发布输入只能声明预期身份，不能覆盖 manifest。
- ID 大小写不敏感，唯一性使用 NuGet 规范版本，防止 `1`、`1.0`、`1.0.0.0` 成为不同 Artifact。
- 在任何可见元数据写入前创建预留配额、幂等的发布 session 和按内容寻址的 staged 对象。
- 明确定义重复规范版本、仓库签名、包签名验证、symbol package 范围、审计和中断上传清理。
- 仅在 Memory/PostgreSQL 一致后才在 OpenAPI 冻结 PackagePublish 资源和管理 session。

验收门禁：有界异常 ZIP/XML 测试、官方客户端构建的 `.nupkg`、与 `NuGet.Versioning` 对照的身份向量、持久化一致、上传恢复，并保证 commit 前包不可见。

## N2：原生 Hosted restore

- 提供 V3 service index，资源 URL 根据外部请求 origin 和 Repository 路径生成。
- 实现 PackageBaseAddress 列表与 `.nupkg` 读取、Registrations 元数据，以及 Visual Studio/`dotnet` 所需的最小搜索与自动完成资源。
- 保持 `GET`、`HEAD`、Range、ETag、条件请求、授权、匿名策略、容量、浏览、搜索和稳定深链接。
- 从已提交 package 记录和 `.nuspec` 依赖构建 registration，不从文件名重建身份。

验收门禁：干净 .NET SDK 容器添加 Gateway 源，恢复应用及传递依赖，并从存储字节离线重放；客户端永远看不到 staged 或索引不完整版本。

## N3：Proxy 与有序 Group

显式启用的 [Proxy/Group 预览](nuget-proxy-group-preview.zh-CN.md) 提供以下 V3 发现与
身份边界。固定官方客户端门禁覆盖私有 Basic/Bearer 源、完整传递依赖、上游 unlisted
精确版本，以及断源和存储重启后的缓存重放。搜索在持久来源绑定前检查完整包 ID
权限。本阶段不公开接纳该格式，也不实现本地 Hosted unlist 或治理。

- 从各 V3 service index 发现上游资源，不假设 nuget.org URL 结构。
- 限制元数据/包缓存、负缓存、重定向、上游认证和出站 allowlist。
- Group 按规范化 ID/version 确定成员所有权；高优先级身份先 claim 请求，不能在资产缺失后随意 fallback。
- 保持 Repository source mapping，防止私有命名空间静默回退到公网。

验收门禁：真实 `dotnet restore` 覆盖 Hosted、Proxy、有序 Group、认证上游、缓存重放、规范版本碰撞和依赖混淆边界。

## N4：生命周期、扫描和分发一致性

- 对 package 及其元数据实现墓碑、恢复、保留、延迟回收、不可变晋级和断点复制。
- 解析 `.nupkg` 供手动与发布扫描；保留 SBOM、许可证、漏洞、签名和来源证据，但不声称扫描器不支持的行为。
- 将隔离准入和可选读取强制应用到规范 package 身份，覆盖直接与 Group 读取。
- 扩展备份恢复、指标、审计、Webhook、升级兼容、Console 和运行时 OpenAPI 验证。

验收门禁：完整生命周期/安全矩阵在 Memory、PostgreSQL、RustFS、Worker 和官方 `dotnet`/NuGet 客户端上通过；之后才能公开支持 NuGet。

## 交付顺序

保持 #287 Hosted 与 #288 Proxy/Group 预览显式启用，生命周期、Console 与公开准入仍由 #289 推进。APT Hosted 准入继续按独立路线图推进。现有解析器持续测试，能力发现不得提前宣称未完成的 NuGet 仓库行为。
