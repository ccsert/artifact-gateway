# 协议兼容性基线

[English detailed matrix](protocol-compatibility.md)

状态：当前协议基线。本页覆盖 Artifact Gateway 已经存在的 OCI、Maven、Raw、Conan、
npm、PyPI、Go、Cargo 和 APT 协议，并用中文说明已实现行为、明确限制和回归门禁。英文页面保留
逐项的完整 Nexus 差异与规范链接；两页发生歧义时，以可执行测试和英文详细矩阵为准。

## 如何理解“兼容”

- **标准主路径兼容**表示真实生态客户端可以完成矩阵列出的读取、安装或上传流程，不表示实现了规范中的全部可选端点。
- **Gateway 扩展**表示读取或基础上传沿用生态协议，但发布、删除或管理需要额外的 Gateway API。
- **Proxy/Group 兼容**只覆盖明确列出的只读路径。它不自动继承 Hosted 的写入、Catalog、Referrer 或生命周期能力。
- **回归门禁通过**证明当前 fixture 和真实客户端流程成立，不代表所有客户端版本、认证方式和扩展组合都经过测试。
- **预览或研究**不得当作公开支持能力。APT Hosted 和 NuGet 仍属于这一层级。

## 原生客户端根路径

客户端使用 Artifact Gateway 的 canonical 协议入口。#385 已退役 Nexus 风格
`/repository/<name>/...` 别名与 Go 版本 ZIP 上传，旧路由返回 404。迁移时更新仓库
URL，并重新生成仍包含旧 URL 的 lockfile 或缓存元数据。

| 格式 | 发布 | 读取与 Group 解析 |
| --- | --- | --- |
| Maven | `/repository/maven/<repository>/` | Hosted 使用同一根路径；Proxy/Group 使用 `/maven/<repository>/` |
| npm | `/npm/<repository>/` | `/npm/<repository>/`；生成的 Tarball URL 使用该根路径 |
| PyPI | `/pypi/<repository>/legacy/` | `/pypi/<repository>/simple/`；发行文件 Location 使用 `/pypi/<repository>/packages/` |
| Raw | `/raw/<repository>/<path>` | `/raw/<repository>/<path>`；目录分页与恢复上传 Location 使用该根路径 |
| Go | `PUT /go/<repository>/<escaped-module>/@v/<version>.zip` | 将 `/go/<repository>` 设为 `GOPROXY` |
| OCI | `/v2/<repository>/<image>/...` | Registry V2 的 `/v2/` 根路径 |

这些入口保持标准包客户端协议，不模拟 Nexus REST API、用户、权限或 Blob Store。
Nexus 发现仍作为只读迁移输入；导入数据与切换生产客户端 URL 分别验收。

## 能力矩阵

| 协议 | 当前支持 | 明确限制 | 主要回归门禁 |
| --- | --- | --- | --- |
| OCI Hosted / Proxy / Group | Hosted 基于 `/v2/<repository>/<image>/...` 实现 Registry V2；支持可恢复 Blob 上传、取消、跨 Hosted 仓库 mount、GET/HEAD/Range、Manifest PUT/GET/HEAD/DELETE、Tag 移动与分页、Catalog 和 Referrers。Proxy 只读缓存 Manifest/Blob；Group 只读解析 Manifest/Blob/Tag，Hosted 成员优先。可选隔离读取策略会拒绝被隔离 Manifest 及其 Blob closure。 | 不支持直接删除 Blob。Proxy/Group 不支持写入，且不代理或聚合 Catalog/Referrers。Docker/ORAS 黑盒客户端矩阵仍未覆盖全部组合。 | `go test ./internal/app ./internal/repository ./contracts`、`make native-oci-e2e` 与 PostgreSQL/RustFS 上传、Proxy 缓存、Group fallback、锁、Tag、对象意图和孤儿回收测试。 |
| Raw Hosted / Proxy / Group | `/raw/<repository>/<path>` 下的 PUT 替换（无条件，或以 `If-Match`/`If-None-Match` 为条件、与引用替换原子判定，`*` 即 create-only，恢复上传的完成同样适用）、GET、HEAD、单 Range 与[有界多段 Range](raw-http-ranges.zh-CN.md)、可读 `Content-Disposition`、Digest/ETag、DELETE、Hosted 目录前缀分页、`.sha256`/`.sha512` 派生 sidecar，以及可恢复上传。path 是可变引用，`path + digest` 才是不可变快照身份。大对象读取走 `Open`/`OpenRange`；上传 PATCH 持久化不可变 offset chunk，完成时只顺序组装一次。Proxy/Group 要求 HTTPS allowlist 上游，使用有界缓冲临时落盘、可续租分布式 single-flight、流式缓存发布/读取，以及可配置的单 Gateway 落盘 admission。字节校验并提交元数据前保持不可见；隔离读取策略同时隐藏路径、sidecar 和 Hosted 目录项。 | 不支持 `If-Unmodified-Since` 与条件 DELETE、Hosted 前缀投影之外的通用文件管理或非 HTTP 客户端。部署仍需临时卷、空闲空间监控和硬性 ephemeral-storage 限额。 | Nexus Raw Hosted 的浏览/管理体验更宽；Gateway 默认保留同路径替换行为以降低迁移门槛，同时保持未完成字节不可见、sidecar 不重复存储，并使用元数据删除和延迟回收。 | `make native-raw-e2e`，以及 Hosted/Proxy 大对象流式读取、落盘 admission、HEAD、Range、恢复上传、条件写入（同一 validator 并发写者）、墓碑、Digest/ETag、回收和 PostgreSQL/RustFS 生命周期测试。 |
| Maven Hosted / Proxy / Group | Hosted 默认直接发布每个经过验证的标准 Maven/Gradle PUT，不需要 companion 调用；可按仓库开启默认关闭的 `mavenStrictPublication`，把一个坐标暂存到显式 commit。客户端 metadata/checksum 不具权威性，Gateway 从验证对象生成。Proxy 只读缓存；Group 只读且 Hosted 优先。完整说明见 [Maven Hosted 发布流程](maven-hosted-publication.zh-CN.md)。 | 默认模式在发布中途失败时不保证一个 GAV 的多文件原子可见；严格模式需要 Gateway 专属 commit。不支持客户端 metadata 权威、跨坐标构建事务、可变 Release 覆盖或向 Proxy/Group 写入。 | `make native-maven-e2e`，以及免 commit 的 Maven/Gradle 解析、严格模式 commit 前 404、Proxy 缓存、Group fallback、校验重试、提交冲突、分页、过期、墓碑和回收测试。 |
| npm Hosted / Proxy / Group | 标准 Packument、精确版本元数据和 Tarball；Hosted 支持 npm CLI 发布不可变 SemVer、scope、dist-tag、生命周期，以及基于 Gateway 主体标识回答的认证 `GET /npm/<repository>/-/whoami` 身份查询（不经过仓库授权）；Proxy 校验并缓存元数据/Tarball，支持条件重验、负缓存、stale 与分布式熔断；Group 按 Hosted 优先和成员顺序合并。Hosted 支持发布后 `npm dist-tag add/rm`（`PUT`/`DELETE /npm/<repository>/-/package/<package>/dist-tags/<tag>`，需要仓库写权限），标签只能指向可见、未墓碑、未隔离的已提交版本；每次修改与审计记录在同一存储事务中原子提交，墓碑会移除指向该版本的标签，`latest` 只能移动不能删除，Tarball 字节与版本身份不变。`npm dist-tag ls` 支持 Hosted、Proxy（缓存的上游标签）与 Group（首个定义该标签的成员为权威）；Proxy 与 Group 拒绝标签写入。Console 展示指向当前所选版本的全部标签。Hosted 原生支持精确/范围提示和清除（仓库 write）、单版本/整包撤包（仓库 admin）以及既有管理恢复；修订号 CAS、操作者/版本原子审计、Group 墓碑权威和持久回收栅栏保持不可变字节。Console 提供提示、写入者编辑与管理员撤包，限制和恢复见 [npm 版本治理](npm-version-governance.zh-CN.md)。 | 不支持需要认证的上游 Registry、Console 中 dist-tag 的编辑、npmjs.com 账户/时间窗策略和漏洞数据库集成。 | `make native-npm-e2e`（含 scope/非 scope `npm dist-tag add/ls/rm` 与空缓存 `npm install <pkg>@<tag>`），协议、应用、Repository 测试，dist-tag Memory/PostgreSQL 契约、同标签并发移动与标签/墓碑竞争测试，以及 PostgreSQL/RustFS 跨实例缓存、发布、Group 在线/离线安装测试。原生客户端门禁校验官方 npm 11.6.2 压缩包的固定 SHA-512，在 PostgreSQL/RustFS 发布 scope/非 scope 版本，验证标签修改、重建 Gateway 后的持久状态、空缓存安装和完整 Tarball size/SHA-256 身份；同一固定客户端门禁还验证 scope/非 scope 的精确/范围提示和清除、单版本/整包撤包、Group 不复活墓碑、新实例管理恢复与四份原始 Tarball。Memory/PostgreSQL 共用契约、并发发布/删除与元数据 CAS、审计失败回滚、149 前升级、S3 部分回收/重启及撤权均有测试；Console 验证桌面/窄屏明暗主题的标签、提示/编辑/撤包状态，并执行完整 Playwright。 |
| PyPI Hosted / Proxy / Group | twine multipart 上传、PEP 503 HTML、PEP 691 JSON、Wheel/sdist 元数据校验、不可变 SHA-256 文件、真实 pip 安装与版本深链；Proxy 要求 SHA-256 上游链接并支持离线缓存；Group 采用 Hosted 优先冲突语义，保留首选文件的撤回状态；Hosted 支持[文件级撤回/恢复与版本原子管理](pypi-yank.zh-CN.md)，HTML/JSON 同步输出原因。 | 不支持认证上游、Simple API 之外的项目元数据接口和漏洞数据库集成。 | `make native-pypi-e2e` 与 PostgreSQL/RustFS 跨实例发布、搜索、撤回/CAS/审计、墓碑和恢复测试；`scripts/pypi-yank-storage-e2e.sh` 验证独立 pg_dump/S3 恢复。 |
| Go Hosted / Proxy / Group | `/go/<repository>/<escaped-module>/...` 下的标准 GOPROXY 读取，覆盖 `@v/list`、`@latest`、`.info`、`.mod`、`.zip`、转义、ETag/HEAD、stale/offline 与 Group 聚合。Proxy 支持 `none`/`basic`/`bearer` 认证上游凭据（以 `GATEWAY_SETTINGS_ENCRYPTION_KEY` 封装，只应用于 Go Proxy 抓取，并在跨主机重定向时剥离），并可镜像一个已列入 egress 允许列表的校验和数据库：`/go/<repository>/sumdb/<name>/supported` 探测仅对允许列表内的主机返回 `200`，`/latest`、`/lookup/`、`/tile/` 请求经允许列表内的 HTTPS egress 与仓库 egress 代理策略转发，原样透传上游状态码、内容类型与签名树字节，不缓存、不附带上游凭据。Hosted 使用 `PUT /go/<repository>/<escaped-module>/@v/<version>.zip`，按路径中的显式模块/版本校验 ZIP 身份并执行资源授权，原子派生三种表示，并支持扫描、墓碑/恢复、保留、晋升和复制。默认关闭的 Hosted 隔离读取策略会把被隔离的模块版本从 `@v/list` 与 `/@latest` 隐藏，阻断 `info`/`mod`/`zip` 全部表示，并阻止 Group fallback。 | Gateway 显式模块/版本上传仍是扩展；校验和数据库的可用性与 `GONOSUMDB`/`GOPRIVATE` 范围由操作者负责，镜像目标不可达时按 go 客户端自身的代理契约直接失败、不再回落。 | `make native-go-e2e`（含真实 go 客户端经镜像校验和数据库的签名验证）与 PostgreSQL/RustFS 跨实例发布、缓存、身份、恢复/回收串行化、晋升、复制、搜索和容量测试。 |
| Cargo Hosted / Proxy / Group | `/cargo/<repository>/` 下的 sparse index 与 Cargo Registry API；Hosted 支持不可变 `.crate` 发布、搜索、yank/unyank、私有或匿名读取，以及管理墓碑/恢复、保留、隔离、晋升和持久检查点复制。Proxy 校验上游摘要后缓存，已缓存版本可离线重放。Group 持久保存 crate/version owner，拒绝成员冲突，重排或恢复不改变原 owner 字节。 | 不支持 Git index、`cargo owner`、Cargo Proxy 上游认证凭据或经 Proxy/Group 写入。混合私有 Group 不能作为 crates.io 源替换，必须使用独立 Registry 身份。 | `make cargo-contract`（Cargo 1.96.0）、PostgreSQL/RustFS 上的 `make integration-test`、`make backup-restore-readiness` 与 `make cargo-upgrade-readiness`；[Cargo 客户端配置](cargo-usage.zh-CN.md)。 |
| APT Proxy / Group | `/apt/<repository>/...` 原样读取 Release/InRelease、签名、Index、by-hash 和 Pool 软件包；支持 SHA-256 缓存、ETag/HEAD、授权过滤、Group fallback、搜索与 Console 浏览。非公开 Hosted 预览支持暂存、原子签名快照、固定公钥验证、TLS、签名状态、Range 与真实 Debian 安装/轮换门禁，并在 Console 提供签名快照导出与离线归档完整性校验、同仓库受信任归档恢复、删除/保留/恢复/修剪、以及生成目标自身签名的晋升与复制。 | APT Hosted 尚缺生产 KMS/HSM 托管、密钥恢复、已安装告警、密钥分发、上游认证、通用定时保留策略执行器、Console 首次发布表单，以及 APT Group 聚合，因此不作为公开 Hosted 能力。 | `make native-apt-e2e`、`make apt-signer-rotation-e2e`，以及迁移、暂存清理、签名快照、归档导出/恢复、分发、容量与搜索测试。 |
| Conan 2 | `/conan/v2/<repository>/...` 下的 Group/Proxy read-through 与原生 Hosted 解析；支持发布 Session、Recipe/Package revision 删除恢复、延迟引用安全回收、浏览搜索、晋升和复制。隔离读取以 Recipe revision 为分发锚点并阻止 Group 重新引入。 | 不支持 Conan 1、通用上游索引聚合、remote-to-remote 复制和不可变 revision 生命周期之外的包管理。 | `make conan-e2e`、Handler 测试及 PostgreSQL 生命周期、搜索、复制与回收 Worker 集成测试。 |

## 契约对齐

- 格式中立扫描器适用于 Maven、OCI、Raw、npm、PyPI、Go 和 Conan 原生存储解析出的
  不可变制品。详细发现会进入 Artifact Intelligence，但不会修改包协议响应字节。
- 隔离是独立治理层：它会在请求和 Worker 最终发布两个阶段阻止晋升与复制。各 Hosted
  仓库可以显式启用隔离读取策略；该策略按版本管理且默认关闭。开启后会拒绝协议读取、
  过滤元数据，并让 Group 中更早出现且已隔离的成员具有权威性。Conan 的 recipe revision
  是原子的分发单元，也是 Conan 唯一的隔离锚点；package revision 仍可独立扫描。
- Raw 与 OCI 使用原生协议发布，不使用管理 Publish Session。Maven 标准 PUT 默认直接
  发布；按仓库开启严格模式后才创建暂存 Session，并以显式 Coordinate commit 为可见
  边界。Conan 使用独立的版本化管理发布 Session；Go Hosted 的 Gateway 显式模块/版本
  路径是扩展，Nexus 版本 ZIP 迁移兼容入口已退役。
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
- Go：[GOPROXY protocol](https://go.dev/ref/mod#goproxy-protocol)；读取保持标准，Hosted
  使用 Gateway 显式模块/版本 ZIP 发布路径。
- Cargo：[Cargo Registry Index](https://doc.rust-lang.org/cargo/reference/registry-index.html)；`api/openapi/protocols/cargo.yaml` 定义 sparse 路由，`make cargo-contract` 运行官方客户端流程；配置见[Cargo 客户端配置](cargo-usage.zh-CN.md)。
- APT：[Debian Repository Format](https://wiki.debian.org/DebianRepository/Format)，Proxy 与
  Group 原样保留签名元数据和软件包字节。

## 兼容性治理

README 只保留简明能力与入口，精确协议声明应更新本页及英文详细矩阵。增加枚举、路由
占位符或 Console 选项不代表支持一种格式；新增生态必须先通过
[格式扩展指南](format-extension-guide.zh-CN.md)中的完整准入门禁。

任何协议能力变更都必须同步更新对应 OpenAPI 源、聚焦测试、真实客户端 E2E 和两种语言
的兼容性说明。APT Hosted 与 NuGet 等预览或研究能力不得混入公开支持矩阵。
