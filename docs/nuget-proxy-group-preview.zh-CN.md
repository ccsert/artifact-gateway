# NuGet Proxy 与 Group 预览

[English](nuget-proxy-group-preview.md) · [Hosted 预览](nuget-hosted-preview.zh-CN.md) · [NuGet 路线图](nuget-roadmap.zh-CN.md)

NuGet Hosted、Proxy 和 Group 使用同一个显式开启的预览。使用标准迁移程序应用
追加 SQL 后设置 `GATEWAY_NUGET_PREVIEW_ENABLED=true`；默认仍关闭。正式格式
档案、仓库/Group 目录和 Console 选择器不公开 NuGet。此阶段没有 `agctl` 配置
命令。[Hosted 治理预览](nuget-hosted-preview.zh-CN.md#列出状态与保留治理) 也保护
已校验 Proxy 缓存和固定 Group 所有者；完整治理与公开准入仍由 [#289](https://github.com/ccsert/artifact-gateway/issues/289) 跟踪。

## 配置上游与有序 Group

管理员调用 `POST /api/v2/previews/nuget/repositories` 创建 Proxy，可提供
`Idempotency-Key`。完整请求示例：

```json
{
  "name": "dotnet-private-proxy",
  "type": "proxy",
  "anonymousRead": false,
  "endpoint": "https://packages.example.test/v3/index.json",
  "allowedHosts": ["package-cdn.example.test"],
  "egressMode": "direct",
  "upstreamAuth": {"scheme": "bearer", "secret": "WRITE_ONLY_UPSTREAM_SECRET"}
}
```

响应包含 `id`、`version`、`preview:true`、原生 `sourceUrl` 和不含秘密的凭据状态。
上游必须是明确的 HTTPS V3 service index；资源路径从索引发现，不套用 nuget.org
地址模板。额外 HTTPS authority 必须列入 `allowedHosts`，省略端口表示 443。
含用户名密码的 URL、歧义路径、未知必需资源版本均显式拒绝。`egressMode`
支持 `direct` 和现有 `environment` 策略。私有 CA 必须通过 Gateway 运行时的正常
信任配置安装。

GET `/api/v2/previews/nuget/repositories/{id}` 读取配置；同路径 PUT 使用 `version`
做原子版本比较并全量替换。需要提交不变的名称/类型以及完整目标 endpoint、主机
和策略。改变 endpoint 或主机绑定时必须提供新的上游 secret，或显式
`{"scheme":"none"}`。省略凭据只会在原绑定内保留已有 secret。Basic 需要
`username` 和 `secret`，Bearer 使用 `secret`；未知方式拒绝。持久秘密使用现有
上游秘密用途单独加密；缺密钥或密文损坏时，冷、热缓存读取均拒绝。

调用 `POST /api/v2/previews/nuget/groups`，提交
`{"name":"dotnet-team","members":["HOSTED_UUID","PROXY_UUID"]}` 创建有序 Group。
成员必须是活动的 NuGet 预览仓库，严格使用请求中的顺序，不隐式将 Hosted 提前。
成员数为 1–32 且不能重复。GET `/api/v2/previews/nuget/groups/{id}` 读取回执；
PUT 通过 `version`、不变名称和完整成员表替换配置。Proxy/Group 服务索引只声明
读取能力，push 和发布会话继续仅适用于 Hosted。

## 身份、凭据与来源恢复

规范化小写包 ID 与规范版本共同确定权威来源。Group 第一次成功解析会持久绑定
成员 ID 和配置版本；重排成员不会改变绑定。元数据与压缩包必须就 ID、版本、
依赖图和发现的包 URL 达成一致。资产缺失、来源移除或变化时，不能改选其它成员
的同身份包；不同版本可以来自不同成员。

GET `/api/v2/previews/nuget/groups/{id}/sources/{packageId}/{version}` 会核验完整包，
返回成员 ID/版本、SHA-256 和大小。改变上游配置前保存这份回执。每次配置更新
产生新的 Proxy 缓存空间，旧元数据不能授权新读取；已提交的旧字节仍保留引用并
计入配额。

主动修改配置或轮换凭据后，旧 Group 绑定返回 409。管理员可向同一来源路径的
`/revalidate` POST
`{"groupVersion":"CURRENT_GROUP_VERSION","repositoryVersion":"PRIOR_OWNER_VERSION"}`。
它完整核验当前来源字节，只允许相同成员、相同 SHA-256 和大小原子前移配置版本，
不能换成员或替换字节。成功响应丢失后重试保留原结果。字节变化、Group 版本过期
或成员被移除时明确拒绝。

Gateway 本地凭据、上游 Basic/Bearer 秘密与签名私钥分别管理。原生读取使用现有
本地 Bearer，或将其作为 Basic 密码；Basic 用户名不选择身份。Group 读取要求
调用者通过每个有序成员的读取/资源授权。发现请求可用包名前缀授权，但返回每个
包前仍检查完整 ID；不存在虚构的 Group 仓库 grant。匿名读取要求全局策略、Group
及每个成员同时允许。显式错误、已撤销或已停用的本地凭据不会回落匿名。

上游秘密仅应用到配置的 origin。额外允许的跨源资源不会得到该秘密；redirect
跨 origin 时剥离凭据，之后返回原 origin 也不重新附加。本地认证、Cookie 和
代理认证不会成为上游凭据。每一跳继续受现有 egress、DNS、TLS 和 redirect 边界
约束。报告与错误响应不回显源秘密。

## 缓存与存储边界

V3 JSON 正缓存为 30 秒，负缓存为 10 秒。过期正缓存使用 ETag/Last-Modified
条件重验；304 必须有完整旧文档。拨号、DNS 或传输超时导致断网时，可使用完整
正缓存；TLS 失败、CONNECT 明确拒绝、HTTP 401/403、畸形元数据和来源绑定变化
均不允许回落。过期负缓存不能成为离线成功结果。

元数据不是可见制品。完整 nupkg 检查、SHA-256/SHA-512 与依赖校验、可重读对象
暂存和原子发布全部完成后，包才可见。同一来源空间内的重复、并发填充只保留
一次发布。提交与配额更新共享现有仓库锁。新旧已提交空间均计入配额和备份引用，
暂存始终不可见。缓存对象丢失或损坏时拒绝，不改变 Group 来源。过期暂存继续
使用既有预览收集器与发布/回收锁。

Registration 支持内联页和外置页，最多 64 页/4096 个版本，并校验精确版本边界、
数量和规范版本唯一性。搜索与自动补全按每页 100 条继续读取，拒绝不完整或重复
结果；聚合上限为 4096 个包版本。单个 JSON 文档最多 8 MiB，每仓库空间最多
4096 文档/64 MiB。超限直接失败，不截断依赖信息。压缩包、nuspec 和持久元数据
限制沿用 Hosted 预览。

依赖比较支持常见 .NET Framework、.NET Standard、.NET Core 和现代 .NET 的
长/短 framework 名称。未知平台限定、portable 或其它 framework 形状明确拒绝。
浮动版本和未知区间明确拒绝。上游 listed/unlisted 状态会保留：搜索和自动补全过滤 unlisted 版本，精确版本清单、元数据和完整字节仍支持读取。本地 Hosted unlist 动作仍在阶段范围外。NuGet V2、Proxy/Group push、
签名校验、symbols、晋级、软删除/恢复、可见缓存保留策略及全部搜索排名对等均在
范围外。备份元数据指纹包含新增表；引用覆盖不等于已单独完成 NuGet 全灾难恢复
验收。

## 验收与证据

使用 `make native-nuget-e2e`。脚本以不可变摘要固定官方 .NET SDK 10.0.100 根索引
及平台 child。Docker 凭据、NuGet 配置、客户端主目录与缓存均属于随机测试项目。
原 Hosted lane 保留宿主机 Gateway；私有 CA 的 Proxy/Group lane 在固定 SDK
容器内运行独立 Linux Gateway，并使用独立 CA 文件。这不证明 macOS 私有 CA
配置已验收。

官方客户端只配置 Group 一个源，恢复三层依赖链，验证 Hosted 与上游同 ID 冲突
和不同版本来源，再无需 restore 执行结果。Proxy 版本在上游明确标为 unlisted，仍必须按精确版本恢复。Basic/Bearer 分别覆盖冷、热缓存及
断源后重启 Gateway/PostgreSQL/RustFS 的冷恢复。完整缓存保留大小、SHA-256、
原始 nuspec 和绑定来源；热读取不能再次下载压缩包。所有必需用例不允许失败或
跳过。`output/native-nuget-e2e/<project>/` 保留源码文件摘要、各运行时 binary/
工具链来源、测试 JSON、客户端结果和空的临时资源清单。CI 证据必须绑定精确
源码树；本机通过不等于合并、公开格式准入或生产放行。

官方协议来源，读取于 2026-10-21：
[service index](https://learn.microsoft.com/en-us/nuget/api/service-index)、
[flat container](https://learn.microsoft.com/en-us/nuget/api/package-base-address-resource)、
[registration](https://learn.microsoft.com/en-us/nuget/api/registration-base-url-resource)、
[search](https://learn.microsoft.com/en-us/nuget/api/search-query-service-resource)、
[autocomplete](https://learn.microsoft.com/en-us/nuget/api/search-autocomplete-service-resource)。
