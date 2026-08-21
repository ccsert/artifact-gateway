# 协议兼容性基线

[English detailed matrix](protocol-compatibility.md)

状态：当前协议基线。本页覆盖 Artifact Gateway 已经存在的 OCI、Maven、Raw、Conan、
npm、PyPI、Go 和 APT 协议，并用中文说明已实现行为、明确限制和回归门禁。英文页面保留
逐项的完整 Nexus 差异与规范链接；两页发生歧义时，以可执行测试和英文详细矩阵为准。

## 能力矩阵

| 协议 | 当前支持 | 明确限制 | 主要回归门禁 |
| --- | --- | --- | --- |
| OCI Hosted | 基于 `/v2/<repository>/<image>/...` 的 Registry V2；支持可恢复 Blob 上传、取消、跨 Hosted 仓库 mount、GET/HEAD/Range、Manifest PUT/GET/HEAD/DELETE、Tag 移动与分页、Catalog 和 Referrers。可选隔离读取策略会拒绝被隔离 Manifest 及其 Blob closure，并从 Tag/Catalog/Referrer 中隐藏。 | 不支持直接删除 Blob；Docker/ORAS 黑盒客户端矩阵仍未覆盖全部组合。 | `go test ./internal/app ./internal/repository ./contracts`、`make native-oci-e2e` 与 PostgreSQL/RustFS 上传、锁、Tag、对象意图和孤儿回收集成测试。 |
| Raw Hosted | `/raw/<repository>/<path>` 下的 PUT、GET、HEAD、单 Range、Digest/ETag、DELETE、目录前缀分页、`.sha256`/`.sha512` 派生 sidecar，以及可恢复上传。字节校验并提交元数据前保持不可见；隔离读取策略同时隐藏路径、sidecar 和目录项。 | 不支持条件写入/更新语义；浏览能力是路径前缀投影，不是通用文件管理器。 | `make native-raw-e2e`，以及大对象流式读取、Range、恢复上传、墓碑、Digest/ETag、回收和 PostgreSQL/RustFS 生命周期测试。 |
| Maven Hosted | 标准 Maven/Gradle PUT 暂存，服务端推导坐标与资产名；校验和 sidecar 作为断言；由服务端生成元数据与校验和；通过 Gateway companion commit 原子公开坐标；支持幂等、浏览、逻辑删除与恢复。 | 普通 Maven 流量不会静默自动公开；客户端元数据不是权威；不支持跨坐标事务、可变 Release 覆盖或缺少 companion commit 的发布。 | `make native-maven-e2e`，以及 Maven/Gradle 暂存、校验重试、提交冲突、分页、过期、墓碑和回收测试。 |
| npm Hosted / Proxy / Group | 标准 Packument、精确版本元数据和 Tarball；Hosted 支持 npm CLI 发布不可变 SemVer、scope、dist-tag 和生命周期；Proxy 校验并缓存元数据/Tarball，支持条件重验、负缓存、stale 与分布式熔断；Group 按 Hosted 优先和成员顺序合并。 | 不支持需要认证的上游 Registry、发布后 dist-tag 修改、unpublish/deprecate 和漏洞数据库集成。 | `make native-npm-e2e`，协议、应用、Repository 测试及 PostgreSQL/RustFS 跨实例缓存、发布、Group 在线/离线安装测试。 |
| PyPI Hosted / Proxy / Group | twine multipart 上传、PEP 503 HTML、PEP 691 JSON、Wheel/sdist 元数据校验、不可变 SHA-256 文件、真实 pip 安装与版本深链；Proxy 要求 SHA-256 上游链接并支持离线缓存；Group 采用 Hosted 优先冲突语义。 | 不支持认证上游、yank、Simple API 之外的项目元数据接口和漏洞数据库集成。 | `make native-pypi-e2e` 与 PostgreSQL/RustFS 跨实例发布、搜索、墓碑和恢复测试。 |
| Go Hosted / Proxy / Group | `/go/<repository>/<escaped-module>/...` 下的标准 GOPROXY 读取，覆盖 `@v/list`、`@latest`、`.info`、`.mod`、`.zip`、转义、ETag/HEAD、stale/offline 与 Group 聚合。Hosted 使用 Gateway 专属单 ZIP PUT，原子派生三种表示，并支持扫描、墓碑/恢复、保留、晋升和复制。 | Hosted 上传属于明确标注的 Gateway 扩展；不支持校验和数据库镜像和认证上游 Proxy。 | `make native-go-e2e` 与 PostgreSQL/RustFS 跨实例发布、缓存、身份、恢复/回收串行化、晋升、复制、搜索和容量测试。 |
| APT Proxy / Group | `/apt/<repository>/...` 原样读取 Release/InRelease、签名、Index、by-hash 和 Pool 软件包；支持 SHA-256 缓存、ETag/HEAD、授权过滤、Group fallback、搜索与 Console 浏览。非公开 Hosted 预览支持暂存、原子签名快照、固定公钥验证、TLS、签名状态、Range 与真实 Debian 安装/轮换门禁。 | APT Hosted 尚缺生产 KMS/HSM 托管、密钥恢复、已安装告警、密钥分发、删除恢复、保留、晋升、复制和上游认证，因此不作为公开 Hosted 能力。 | `make native-apt-e2e`、`make apt-signer-rotation-e2e`，以及迁移、暂存清理、签名快照、容量与搜索测试。 |
| Conan 2 | `/conan/v2/<repository>/...` 下的 Group/Proxy read-through 与原生 Hosted 解析；支持发布 Session、Recipe/Package revision 删除恢复、延迟引用安全回收、浏览搜索、晋升和复制。隔离读取以 Recipe revision 为分发锚点并阻止 Group 重新引入。 | 不支持 Conan 1、通用上游索引聚合、remote-to-remote 复制和不可变 revision 生命周期之外的包管理。 | `make conan-e2e`、Handler 测试及 PostgreSQL 生命周期、搜索、复制与回收 Worker 集成测试。 |

## 契约对齐

- 格式中立扫描器适用于 Maven、OCI、Raw、npm、PyPI、Go 和 Conan 原生存储解析出的
  不可变制品。详细发现会进入 Artifact Intelligence，但不会修改包协议响应字节。
- 隔离是独立治理层：它会在请求和 Worker 最终发布两个阶段阻止晋升与复制。各 Hosted
  仓库可以显式启用隔离读取策略；该策略默认关闭。
- Raw 与 OCI 使用原生协议发布，不使用管理 Publish Session。Maven 的可见边界是显式
  Coordinate commit；Go Hosted 的单 ZIP PUT 是独立记录的 Gateway 扩展。
- `api/openapi/native-hosted.yaml` 及其 `components`、`management`、`protocols` 片段是
  可编辑契约源；JSON bundle、Console 客户端和 Go 服务端契约由此生成。
- [Native Hosted 契约](native-hosted-contract.md)当前仍只有英文，负责元数据权威、对象
  生命周期、幂等和删除语义；本页负责对外协议兼容性摘要。

## 官方规范与 Gateway overlay

- OCI：[OCI Distribution Specification](https://distribution.github.io/distribution/spec/api/)，
  Gateway overlay 位于 `api/openapi/protocols/oci.yaml`。
- Raw：没有统一协议规范，Gateway 路由与 Range 语义由
  `api/openapi/protocols/raw.yaml` 定义。
- Maven：[Maven repository documentation](https://maven.apache.org/repositories/index.html)，
  Gateway overlay 记录标准 PUT 暂存与 companion commit。
- Conan：[Conan 2 remote documentation](https://docs.conan.io/2/reference/commands/remote.html)，
  协议 overlay 位于 `api/openapi/protocols/conan.yaml`。
- npm：[npm registry package metadata](https://github.com/npm/registry/blob/main/docs/REGISTRY-API.md)，
  `make native-npm-e2e` 是 Hosted/Proxy/Group 可执行 overlay。
- PyPI：[PEP 503](https://peps.python.org/pep-0503/)、
  [PEP 691](https://peps.python.org/pep-0691/) 与
  [PyPA upload API](https://warehouse.pypa.io/api-reference/legacy.html)。
- Go：[GOPROXY protocol](https://go.dev/ref/mod#goproxy-protocol)，读取保持标准；单 ZIP
  Hosted PUT 单独标为 Gateway 扩展。
- APT：[Debian Repository Format](https://wiki.debian.org/DebianRepository/Format)，Proxy 与
  Group 原样保留签名元数据和软件包字节。

## 兼容性治理

README 只保留简明能力与入口，精确协议声明应更新本页及英文详细矩阵。增加枚举、路由
占位符或 Console 选项不代表支持一种格式；新增生态必须先通过
[格式扩展指南](format-extension-guide.md)中的完整准入门禁（该指南当前仅英文）。

任何协议能力变更都必须同步更新对应 OpenAPI 源、聚焦测试、真实客户端 E2E 和两种语言
的兼容性说明。APT Hosted、Cargo 与 NuGet 等预览或研究能力不得混入公开支持矩阵。
