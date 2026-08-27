# 协议兼容性基线

[English detailed matrix](protocol-compatibility.md)

状态：当前协议基线。本页覆盖 Artifact Gateway 已经存在的 OCI、Maven、Raw、Conan、
npm、PyPI、Go 和 APT 协议，并用中文说明已实现行为、明确限制和回归门禁。英文页面保留
逐项的完整 Nexus 差异与规范链接；两页发生歧义时，以可执行测试和英文详细矩阵为准。

## 如何理解“兼容”

- **标准主路径兼容**表示真实生态客户端可以完成矩阵列出的读取、安装或上传流程，不表示实现了规范中的全部可选端点。
- **Gateway 扩展**表示读取或基础上传沿用生态协议，但发布、删除或管理需要额外的 Gateway API。
- **Proxy/Group 兼容**只覆盖明确列出的只读路径。它不自动继承 Hosted 的写入、Catalog、Referrer 或生命周期能力。
- **回归门禁通过**证明当前 fixture 和真实客户端流程成立，不代表所有客户端版本、认证方式和扩展组合都经过测试。
- **预览或研究**不得当作公开支持能力。APT Hosted、Cargo 和 NuGet 仍属于这一层级。

## Nexus 风格 Repository 根路径

Maven、npm、PyPI、Raw、Go 客户端迁移到 Artifact Gateway 时，可以继续使用熟悉的
`/repository/<name>/...` 根路径；`<name>` 可以指向 Hosted、Proxy 或 Group。兼容路由
只负责解析目标并内部转发给已有格式 Handler，不会复制或绕过鉴权、授权、审计、协议
校验和对象生命周期。同名 Repository 与 Group 同时存在时，Repository 优先，与规范
路由保持一致。`maven` 这个精确名称由 Gateway 原有
`/repository/maven/<repository>/...` canonical 路由保留；Nexus 中同名目标迁移时应改名，
避免把制品路径误判成另一个 Repository。

| 格式 | Nexus 风格客户端根路径 | 已验证迁移流程 |
| --- | --- | --- |
| Maven | `/repository/<name>/` | 真实 Maven deploy/resolve 与 Gradle publish/resolve；默认直接发布不需要 companion commit。 |
| npm | `/repository/<name>/` | 真实 scope/非 scope 发布、Group 安装、冷 package-lock、在线/离线 `npm ci`；生成的 Tarball URL 保持该根路径。 |
| PyPI | 上传使用 `/repository/<name>/`，读取使用 `/repository/<name>/simple/` | 真实 twine 根路径上传，Hosted/Proxy/Group pip 下载及 Proxy 离线缓存。 |
| Raw | `/repository/<name>/<path>` | PUT、GET、HEAD、Range、DELETE、目录分页与断点上传；发现型响应保持该根路径。 |
| Go | 把 `/repository/<name>` 设为 `GOPROXY`；Hosted 使用 `PUT /repository/<name>/<version>.zip` | 真实 `go mod download` 覆盖 Hosted 与在线/离线 Proxy；Nexus 风格上传从 ZIP 根目录推导并验证模块身份。 |

这只是包管理客户端迁移入口，不模拟 Nexus REST API、任务、用户权限或 Blob Store。
OCI 被明确排除：Docker/ORAS 要求 Registry V2 根路径固定为 `/v2/`，而 Nexus 常通过
Connector 端口或虚拟主机选择 Docker Repository。OCI 迁移必须映射 Registry Host 与
Gateway Repository 名称前缀（或使用等价的 Ingress Rewrite），
`/repository/<name>/...` 不能成为合规的 Docker Registry 地址。

## 能力矩阵

| 协议 | 当前支持 | 明确限制 | 主要回归门禁 |
| --- | --- | --- | --- |
| OCI Hosted / Proxy / Group | Hosted 基于 `/v2/<repository>/<image>/...` 实现 Registry V2；支持可恢复 Blob 上传、取消、跨 Hosted 仓库 mount、GET/HEAD/Range、Manifest PUT/GET/HEAD/DELETE、Tag 移动与分页、Catalog 和 Referrers。Proxy 只读缓存 Manifest/Blob；Group 只读解析 Manifest/Blob/Tag，Hosted 成员优先。可选隔离读取策略会拒绝被隔离 Manifest 及其 Blob closure。 | 不支持直接删除 Blob。Proxy/Group 不支持写入，且不代理或聚合 Catalog/Referrers。Docker/ORAS 黑盒客户端矩阵仍未覆盖全部组合。 | `go test ./internal/app ./internal/repository ./contracts`、`make native-oci-e2e` 与 PostgreSQL/RustFS 上传、Proxy 缓存、Group fallback、锁、Tag、对象意图和孤儿回收测试。 |
| Raw Hosted / Proxy / Group | `/raw/<repository>/<path>` 下的无条件 PUT 替换、GET、HEAD、单 Range、可读 `Content-Disposition`、Digest/ETag、DELETE、Hosted 目录前缀分页、`.sha256`/`.sha512` 派生 sidecar，以及可恢复上传。path 是可变引用，`path + digest` 才是不可变快照身份。大对象读取走 `Open`/`OpenRange`；上传 PATCH 持久化不可变 offset chunk，完成时只顺序组装一次。Proxy/Group 要求 HTTPS allowlist 上游，使用有界缓冲临时落盘、可续租分布式 single-flight、流式缓存发布/读取，以及可配置的单 Gateway 落盘 admission。字节校验并提交元数据前保持不可见；隔离读取策略同时隐藏路径、sidecar 和 Hosted 目录项。 | 不支持 `If-Match`、create-only 等条件写入、多段 Range、Hosted 前缀投影之外的通用文件管理或非 HTTP 客户端。部署仍需临时卷、空闲空间监控和硬性 ephemeral-storage 限额。 | Nexus Raw Hosted 的浏览/管理体验更宽；Gateway 默认保留同路径替换行为以降低迁移门槛，同时保持未完成字节不可见、sidecar 不重复存储，并使用元数据删除和延迟回收。 | `make native-raw-e2e`，以及 Hosted/Proxy 大对象流式读取、落盘 admission、HEAD、Range、恢复上传、墓碑、Digest/ETag、回收和 PostgreSQL/RustFS 生命周期测试。 |
| Maven Hosted / Proxy / Group | Hosted 默认直接发布每个经过验证的标准 Maven/Gradle PUT，不需要 companion 调用；可按仓库开启默认关闭的 `mavenStrictPublication`，把一个坐标暂存到显式 commit。客户端 metadata/checksum 不具权威性，Gateway 从验证对象生成。Proxy 只读缓存；Group 只读且 Hosted 优先。完整说明见 [Maven Hosted 发布流程](maven-hosted-publication.zh-CN.md)。 | 默认模式在发布中途失败时不保证一个 GAV 的多文件原子可见；严格模式需要 Gateway 专属 commit。不支持客户端 metadata 权威、跨坐标构建事务、可变 Release 覆盖或向 Proxy/Group 写入。 | `make native-maven-e2e`，以及免 commit 的 Maven/Gradle 解析、严格模式 commit 前 404、Proxy 缓存、Group fallback、校验重试、提交冲突、分页、过期、墓碑和回收测试。 |
| npm Hosted / Proxy / Group | 标准 Packument、精确版本元数据和 Tarball；Hosted 支持 npm CLI 发布不可变 SemVer、scope、dist-tag 和生命周期；Proxy 校验并缓存元数据/Tarball，支持条件重验、负缓存、stale 与分布式熔断；Group 按 Hosted 优先和成员顺序合并。 | 不支持需要认证的上游 Registry、发布后 dist-tag 修改、unpublish/deprecate 和漏洞数据库集成。 | `make native-npm-e2e`，协议、应用、Repository 测试及 PostgreSQL/RustFS 跨实例缓存、发布、Group 在线/离线安装测试。 |
| PyPI Hosted / Proxy / Group | twine multipart 上传、PEP 503 HTML、PEP 691 JSON、Wheel/sdist 元数据校验、不可变 SHA-256 文件、真实 pip 安装与版本深链；Proxy 要求 SHA-256 上游链接并支持离线缓存；Group 采用 Hosted 优先冲突语义。 | 不支持认证上游、yank、Simple API 之外的项目元数据接口和漏洞数据库集成。 | `make native-pypi-e2e` 与 PostgreSQL/RustFS 跨实例发布、搜索、墓碑和恢复测试。 |
| Go Hosted / Proxy / Group | `/go/<repository>/<escaped-module>/...` 下的标准 GOPROXY 读取，覆盖 `@v/list`、`@latest`、`.info`、`.mod`、`.zip`、转义、ETag/HEAD、stale/offline 与 Group 聚合。Hosted 同时接受 Gateway 显式模块/版本 ZIP 路径，以及 Nexus 3.93+ 的 `PUT /repository/<repository>/<version>.zip`；后者先从规范 ZIP 根目录推导模块，再执行资源授权。两条路径都原子派生三种表示，并支持扫描、墓碑/恢复、保留、晋升和复制。 | Gateway 显式模块/版本上传仍是扩展；不支持隔离读取强制执行、校验和数据库镜像和认证上游 Proxy。 | `make native-go-e2e` 与 PostgreSQL/RustFS 跨实例发布、缓存、身份、恢复/回收串行化、晋升、复制、搜索和容量测试。 |
| APT Proxy / Group | `/apt/<repository>/...` 原样读取 Release/InRelease、签名、Index、by-hash 和 Pool 软件包；支持 SHA-256 缓存、ETag/HEAD、授权过滤、Group fallback、搜索与 Console 浏览。非公开 Hosted 预览支持暂存、原子签名快照、固定公钥验证、TLS、签名状态、Range 与真实 Debian 安装/轮换门禁。 | APT Hosted 尚缺生产 KMS/HSM 托管、密钥恢复、已安装告警、密钥分发、删除恢复、保留、晋升、复制和上游认证，因此不作为公开 Hosted 能力。 | `make native-apt-e2e`、`make apt-signer-rotation-e2e`，以及迁移、暂存清理、签名快照、容量与搜索测试。 |
| Conan 2 | `/conan/v2/<repository>/...` 下的 Group/Proxy read-through 与原生 Hosted 解析；支持发布 Session、Recipe/Package revision 删除恢复、延迟引用安全回收、浏览搜索、晋升和复制。隔离读取以 Recipe revision 为分发锚点并阻止 Group 重新引入。 | 不支持 Conan 1、通用上游索引聚合、remote-to-remote 复制和不可变 revision 生命周期之外的包管理。 | `make conan-e2e`、Handler 测试及 PostgreSQL 生命周期、搜索、复制与回收 Worker 集成测试。 |

## 契约对齐

- 格式中立扫描器适用于 Maven、OCI、Raw、npm、PyPI、Go 和 Conan 原生存储解析出的
  不可变制品。详细发现会进入 Artifact Intelligence，但不会修改包协议响应字节。
- 隔离是独立治理层：它会在请求和 Worker 最终发布两个阶段阻止晋升与复制。各 Hosted
  仓库可以显式启用隔离读取策略；该策略默认关闭。
- Raw 与 OCI 使用原生协议发布，不使用管理 Publish Session。Maven 标准 PUT 默认直接
  发布；按仓库开启严格模式后才创建暂存 Session，并以显式 Coordinate commit 为可见
  边界。Conan 使用独立的版本化管理发布 Session；Go Hosted 的 Gateway 显式模块/版本
  路径是扩展，Nexus 版本 ZIP 路径则作为迁移兼容入口。
- `api/openapi/native-hosted.yaml` 及其 `components`、`management`、`protocols` 片段是
  可编辑契约源；JSON bundle、Console 客户端和 Go 服务端契约由此生成。
- [Native Hosted 契约](native-hosted-contract.zh-CN.md)负责元数据权威、对象
  生命周期、幂等和删除语义；本页负责对外协议兼容性摘要。

## 官方规范与 Gateway overlay

- OCI：[OCI Distribution Specification](https://distribution.github.io/distribution/spec/api/)，
  Gateway overlay 位于 `api/openapi/protocols/oci.yaml`。
- Raw：没有统一协议规范，Gateway 路由与 Range 语义由
  `api/openapi/protocols/raw.yaml` 定义。
- Maven：[Maven repository documentation](https://maven.apache.org/repositories/index.html)，
  Gateway overlay 记录默认直接发布和可选的严格坐标 commit；两种流程见
  [Maven Hosted 发布流程](maven-hosted-publication.zh-CN.md)。
- Conan：[Conan 2 remote documentation](https://docs.conan.io/2/reference/commands/remote.html)，
  协议 overlay 位于 `api/openapi/protocols/conan.yaml`。
- npm：[npm registry package metadata](https://github.com/npm/registry/blob/main/docs/REGISTRY-API.md)，
  `make native-npm-e2e` 是 Hosted/Proxy/Group 可执行 overlay。
- PyPI：[PEP 503](https://peps.python.org/pep-0503/)、
  [PEP 691](https://peps.python.org/pep-0691/) 与
  [PyPA upload API](https://warehouse.pypa.io/api-reference/legacy.html)。
- Go：[GOPROXY protocol](https://go.dev/ref/mod#goproxy-protocol) 与
  [Nexus Go CLI usage](https://help.sonatype.com/en/go-cli-usage.html)；读取保持标准，Hosted
  同时接受 Nexus 版本 ZIP 上传与 Gateway 显式模块/版本路径。
- APT：[Debian Repository Format](https://wiki.debian.org/DebianRepository/Format)，Proxy 与
  Group 原样保留签名元数据和软件包字节。

## 兼容性治理

README 只保留简明能力与入口，精确协议声明应更新本页及英文详细矩阵。增加枚举、路由
占位符或 Console 选项不代表支持一种格式；新增生态必须先通过
[格式扩展指南](format-extension-guide.zh-CN.md)中的完整准入门禁。

任何协议能力变更都必须同步更新对应 OpenAPI 源、聚焦测试、真实客户端 E2E 和两种语言
的兼容性说明。APT Hosted、Cargo 与 NuGet 等预览或研究能力不得混入公开支持矩阵。
