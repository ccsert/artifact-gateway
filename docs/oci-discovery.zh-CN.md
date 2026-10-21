# OCI 目录与关联制品发现

[English](oci-discovery.md) · [文档索引](README.zh-CN.md)

Gateway 为 Hosted、Proxy 和 Group 读取入口提供带授权的 Catalog 与 OCI 1.1
Referrers API。`/api/v2/formats` 的 `ociDiscovery.catalog`、`referrers` 表示
Gateway 的 API 能力，不承诺每个上游均支持完整枚举。`tagFallback` 与
`signatureVerification` 为 false。发现签名或 SBOM、核对原始摘要，只确认内容
关联，不能据此认定签名者身份、证书有效性或信任策略已经通过。

## 枚举可访问名称

认证后请求 `GET /v2/_catalog?n=100`。返回名称包含仓库或 Group 前缀，可以直接
用于 `/v2/<返回名称>/manifests/<引用>`。原样跟随 `Link`，其中 `snapshot` 标识
不可变且绑定调用身份的分页集合。`n` 为 1–1000；名称去重并按字典序排列。
`gateway.entries` 按实际读取优先级列出有权访问的来源仓库名称，Hosted/Proxy
混合成员沿用现有 Group 解析顺序。

Catalog 实际枚举上游页面，不把本地缓存名称冒充完整目录。同次扫描在仓库和
Group 视图间复用该源的实际枚举结果。响应头 `OCI-Gateway-Discovery-Coverage`
与 `gateway.coverage` 区分 `complete`、`partial`、`unsupported`；
`gateway.sources` 只报告有权访问命名空间的覆盖情况及有限原因码，不暴露私有
来源名称、端点、凭据或原始上游错误正文。

不完整扫描有有效名称时返回 200 和 `partial`；不完整且没有名称时返回 502。
全部可用来源都不支持 Catalog 时返回 501 `UNSUPPORTED`，不能当作成功空目录。
调用身份的可访问视图确实没有名称时可以返回完整空结果。外源分页链接、循环
游标、非法名称、认证失败和安全预算不能被静默标记为完整。

游标绑定身份声明、仓库配置、Group 成员及授权版本。配置或权限变化返回 409
`DISCOVERY_CHANGED`，应去掉旧游标重新请求 Catalog。另一身份不能复用游标。
每次返回分页前还会检查当前授权与 Hosted 隔离状态。新的发现入口必须由明确的
管理员、当前角色或仓库 grant 授权，不使用旧协议的授权兜底。

## 发现并消费关联内容

`GET /v2/<仓库或Group>/<镜像>/referrers/<sha256摘要>` 返回核验后的 OCI index。
subject 从第一个有权限且真实持有它的成员解析；根不存在时可以继续下探，根
已存在却缺少子内容或内容非法时必须失败。低优先级成员不能向另一来源的权威
subject 注入自己的签名。

Gateway 逐项获取制品 manifest，核对 subject 完整描述符、媒体类型、原始大小
与摘要，并检查 config、manifest、blob 的完整依赖闭包。完整核验后才在本地
应用 `artifactType` 过滤。原样跟随返回的 `Link` 获取稳定分页，不能向已有快照
替换过滤条件或 subject。不支持 Referrers API 的上游返回 501，不采用标签回退。

成功来源按稳定身份、作用域、镜像和 subject 持久绑定。受限身份不能抢先改变
另一身份的来源选择；角色或下游凭据轮换不改变这个稳定 owner。Group 重排仍
保留原来源。删除该来源、撤销授权、修改源仓库配置或版本，会拒绝既有绑定，
不会自动改选其它来源。当前没有对同一 subject 的隐式重绑操作；确需替换上游
时，应使用新的仓库身份。后续通过同一名称读取 manifest/blob 也遵守此边界。

使用独立 ORAS registry 配置保存凭据，例如：

```sh
oras repo ls --registry-config "$OWNED_REGISTRY_CONFIG" registry.example.test
oras discover --distribution-spec v1.1-referrers-api --format json \
  --registry-config "$OWNED_REGISTRY_CONFIG" \
  registry.example.test/group/team/image@sha256:<subject>
oras pull --registry-config "$OWNED_REGISTRY_CONFIG" --output ./empty-output \
  registry.example.test/group/team/image@sha256:<artifact>
docker pull registry.example.test/group/team/image@sha256:<subject>
```

## 持久化、预算与验收

通过标准追加式迁移 runner 应用 000161。生产组合根显式注入
`OCIDiscoveryStore`；缺少该端口返回 503，不静默创建 Memory 存储。
PostgreSQL 在同事务锁下核对配置及授权版本，原子提交快照/绑定和审计；来源
并发更新不能封存旧清单。持久回执只保存逻辑缓存身份，不保存秘密。

快照保留 15 分钟，每个 owner 最多 32 个有效快照。Catalog 最多 128 个命名
空间/来源、128 个上游页面、10,000 个名称；Referrers 最多 128 个页面与 256 个
描述符。内容图最大深度 8、1024 个对象、单 manifest/index 16 MiB、manifest
总计 64 MiB、单 blob 64 MiB、闭包总计 256 MiB；持久发现 payload 最多 8 MiB。
超出内容图或存储预算会明确返回 429。既有缓存容量准入、出站限制和加密凭据
规则继续生效。

数据库及客户端重启后，来源绑定和快照仍存在。新近完整核验的绑定可以在源离线
时从目标缓存消费；15 分钟后重新发现需要上游新响应。缓存缺失只允许向绑定
来源重新获取。因此缓存回收或源离线可能使内容不可用，却不能替换来源或伪造
成功空列表。备份恢复应同时保存数据库和已有缓存对象。

运行 `bash scripts/native-oci-discovery-e2e.sh` 执行隔离门禁。它创建独立
PostgreSQL/RustFS，标准迁移执行两遍，按 archive/index/平台摘要固定官方
ORAS 1.3.3 与 Docker 29.7.2，每次拉取使用全新带标签的 Docker daemon。
Basic/Bearer 私有源、Proxy/Group、Catalog、递归发现、原始 SBOM 字节、完整
OCI-layout 闭包、Docker 镜像字节、重启离线、来源 fence 与受限前缀都必须真实
执行并通过，必需测试禁止跳过。`output/native-oci-discovery-e2e/<project>/`
保留精确源码摘要、工具来源、原始结果及自有资源清理回读。本机验收、合并后
CI 与生产运行是不同事实。

协议参考：[OCI Distribution 1.1.1](https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md)、
[Registry Catalog](https://distribution.github.io/distribution/spec/api/#catalog)、
[ORAS discover](https://oras.land/docs/commands/oras_discover/)。
