# 变更记录

[English](CHANGELOG.md) | [文档索引](docs/README.zh-CN.md)

本文件记录 Artifact Gateway 的用户可见变化。项目遵循语义化版本；1.0 之前已经是可用
分发版本，但契约仍可能演进。所有变化先归入 `Unreleased`，发布时移动到带日期的版本
标题，且不改写其含义。

## Unreleased

- 改善 Raw Console 体验：仓库浏览、公开浏览、全局搜索、保留预览和分发选择器统一显示
  可读的 Unicode/空格路径，但协议操作继续使用不变的规范坐标。搜索可正确处理文件名中的
  字面 `%`，下载命令显式选择经过 shell 引号保护的可读文件名，文件及 checksum 响应通过
  `Content-Disposition` 提供下载名称。
- 加固 Raw 路径边界：编码后最多 4096 字节，拒绝控制字符和双向格式化字符；上传校验不再
  静默裁剪首尾空格或移除开头 `/`，会在发送前返回可执行的修正提示。
- 公开浏览进入管理端时复用当前浏览器已有登录态：已认证用户直接打开 Console，直接访问
  `/login` 也会自动返回请求的管理位置，无需重复登录。
- 明确 Raw path 为兼容 Nexus 迁移的可变引用，`path + digest` 才是治理与分发使用的不可变快照。

## 0.1.0 - 2026-08-24

- 发布首批可复现、带版本且包含 Migrations 与环境模板的 Gateway/healthcheck 二进制归档，
  以及 Console 静态包、已解析 OpenAPI 契约、校验和、GHCR 镜像和通过 CI 的 `main` 主线
  快照；Release 二进制与镜像统一报告版本号和 Git Revision。

- 新增 Maven、npm、PyPI、Raw、Go Hosted/Proxy/Group 的 Nexus 风格
  `/repository/<name>/...` 迁移根路径；真实 Maven/Gradle、npm、twine/pip、Raw HTTP 与
  Go 客户端可保留原 Base Path，npm Tarball、Raw 分页/断点上传、PyPI 与 Go 发布返回地址
  也保持该根路径。PyPI 可直接在 Repository 根路径接收 Twine，Go Hosted 支持 Nexus
  3.93+ 的版本 ZIP 上传并从归档推导、授权模块身份。为避免跨仓库歧义，旧 Maven
  canonical 前缀继续保留精确名称 `maven`。

- 新增 Go Hosted：认证单 ZIP 发布、模块与 `go.mod` 校验、原子生成 `.info`/`.mod`、PostgreSQL/RustFS 按内容持久化、幂等重放、不可变冲突拒绝、发布扫描、墓碑/恢复、保留、24 小时恢复窗口、引用安全回收、三表示晋级与断点复制、Hosted-first Group，以及真实 `go mod download` 门禁。
- 新增面向 Jenkins、CI、扫描器和第三方应用的稳定 Service Account：一次性过期凭证、零停机轮换、立即禁用、Bearer/Basic 认证、Repository Grant、审计、双语 Console 和独立发布门禁。
- 重做公开制品目录，明确只读边界、来源/格式摘要、搜索/过滤及 Hosted/Proxy/Group 引导；管理员界面展示全局、Repository、Group/成员门禁和影响范围，不改变默认拒绝策略。
- OCI browse 响应增加 manifest 不可变创建时间，客户端无需从 tag 或 digest 推断发布顺序。
- 运行时、Compose、Kubernetes、集成测试和配置全面切换到 RustFS 与 AWS SDK for Go v2，移除 MinIO 服务、依赖、迁移工具和旁路，同时对遗留资源失败关闭。
- 完成首个 APT H3 签名加固：远程 signer 必须使用匹配一至两个固定 fingerprint 的公钥 keyring，Gateway 在可见前验证两份签名，支持受控轮换重叠并记录不可变签名证据。
- 新增 APT 签名状态管理 API、双语 Console、有界结果/延迟指标和专用外部 signer 门禁，覆盖旧密钥、重叠、新密钥、拒绝和退役；旧 Gateway 缺少接口时 Console 显示能力不可用，而非误报 Repository 不存在。
- APT H3 后优先规划 Cargo sparse registry，NuGet 延期但保留解析器基础。
- Principal 选择器隐藏禁用用户与已撤销/过期 API Key，只展示可用授权主体。

### 新增

- 准备阶段的双语项目与文档入口、符合架构的 README 主图、本地链接门禁，以及幂等生成本地 PostgreSQL/RustFS 六项凭证的 `make dev-bootstrap`。
- 双语 Mermaid 系统边界、单体/分角色部署、发布可见性和后台任务图；补充带图标架构总览及 PostgreSQL 锁、队列、通知、JSONB、搜索和可观测特性的证据说明。
- 可复现隔离 Docker 性能基线，覆盖 Go 二进制、distroless 镜像、Gateway/PostgreSQL/RustFS 静默内存、认证元数据读取和 64 KiB Raw 读取，并提供双语边界说明与自动清理 runner。
- 从大型 Console Browse 页面拆出 OCI 元数据读取、tag 分页和仓库配置片段，形成有测试的纯模块；随后为所有站点文档补齐实质中文 companion，并增加受测的框架中立导航图。
- Cargo C0 有界 parser：验证官方 publish framing 与完整 `.crate`，从 `Cargo.toml` 派生碰撞安全身份并生成 checksum-owned sparse index；官方 `cargo package/publish` 测试不代表公开接纳格式。
- Docker Desktop overlay 增加固定版本、非 root Traefik Ingress，以 `artifact-gateway.localhost` 暴露同源 Console、API 和协议面。
- NuGet `.nupkg`/`.nuspec` 有界 parser 与大小写不敏感规范身份；协议门禁完成前保持不可发现。
- 加固 Kustomize base 和一键本地 Kubernetes 部署，包含 Gateway、Console、PostgreSQL、RustFS、幂等迁移、持久卷、健康检查、manifest 验证和同源路由。
- APT Hosted H1/H2：流式 Debian control 解析、预留配额 session、管理 provisioning、PostgreSQL/RustFS 持久化、孤儿回收、不可变快照、`Packages`/Release/By-Hash/签名资产、原子可见切换、Range 读取、参考 signer、真实 Debian 安装及精确备份恢复；H3 前仍不公开 Hosted。
- RustFS-only 对象存储基线，覆盖流式、元数据、Range、生命周期、备份和恢复契约。
- 一键本地开发启动、检查和仅停止当前 checkout Console 的生命周期目标。
- 仅管理员可见且已脱敏的系统诊断，以及双语 Console 和可复制 support JSON。
- `api`、`scheduler`、`worker` 分角色部署，支持格式/任务过滤；PostgreSQL 节点心跳、节点清单和 Worker 能力视图。
- 服务端聚合仓库管理、跨仓库搜索、每仓库出站代理及连接检查。
- npm Hosted/Proxy/Group、PyPI Hosted/Proxy/Group、Go Module Proxy/Group、APT Proxy/Group 的原生协议读取、缓存、授权过滤、搜索、容量与 Console 深链接；相应格式补充生命周期、晋级和复制。
- 可配置外部制品扫描与可选非 root Trivy 参考扫描器，提供持久幂等任务、隔离 Worker、原生资产解析、CycloneDX、许可证、漏洞、健康和漏洞库缓存。
- Hosted 发布扫描策略、每 Artifact 状态、手动重扫、有界补扫和漏洞明细。
- 版本化隔离/释放、默认关闭的隔离读取策略、Group 防绕过、晋级/复制 Worker 二次保护，以及 PostgreSQL/OpenAPI/Console 支持。
- 管理员 Webhook：事务 outbox、加密 HMAC、SSRF 安全 HTTPS、有界重试、死信重放、集群租约、审计和 Console 可见性。
- 本地用户治理：档案、大小写不敏感身份、登录锁定、时间戳、强制改密、管理员重置、可撤销 session 和最后活跃管理员保护。

### 变更

- APT 请求同时经 Vite 开发代理和生产 Console 容器转发，避免包客户端路径落入 SPA。
- 扫描、晋级、复制默认输入改为协议所有的不可变 Artifact 选择器，覆盖历史 npm/PyPI、已缓存 Proxy 和 Conan revision；保留精确身份输入作为恢复路径。
- 新增可发现的 Repository Scanning 工作区，支持手动扫描、能力说明、历史补扫和近期任务状态。
- Retention 使用 Maven、OCI、Conan、Raw、npm、PyPI 和 Go 的 cleanup unit 术语，不再使用 Maven fallback 文案。
- Security Tab 分离隔离读取与晋级准入 guardrail，展示格式范围、保存状态和扫描器可用性；修复桌面网格与窄屏 overflow。
- 扫描与晋级 intelligence 统一 claim、租约、指标、终态、轮询和 PostgreSQL notification 语义。
- 减少 Console Repository 请求 fan-out、拆分大 vendor bundle、压缩详情导航并增加克制动效。
- 制品上传流式化并限制数据库、HTTP 和后台 Worker 资源。
- 增加 Go package 覆盖率下限及 Console lint、格式、可访问性、组件、覆盖率门禁。
- 增加 session-aware 分布节点清单、离线状态、清理和集群能力健康摘要；用户管理改用服务端搜索/过滤/分页与聚焦 Drawer。
- 发布锁使用有界可观测 PostgreSQL pool，多文件对象锁和坐标锁复用同一后端 session。
- Raw 大对象路径改为有界缓冲临时 staging、流式缓存发布/读取、真实上游 `HEAD`、可续租请求锁和每 Gateway staging 准入；超限返回 `503`/`Retry-After` 与有界指标。
- Raw/OCI 可恢复上传改为不可变 offset chunk，完成时一次组装，避免每次 PATCH 重写历史字节；回收会删除完成、取消或过期 session 的残留 chunk 并保留 PostgreSQL 轨迹。
- 性能报告增加 64 MiB Hosted warm read 和受控 HTTPS Raw Proxy cold miss，验证 single-flight 与离线缓存重放。

### 修复

- Maven Hosted 默认兼容 Nexus：标准 Maven/Gradle 上传后可直接读取；新增默认关闭的 `mavenStrictPublication`，供需要 Gateway companion commit 和坐标级原子可见性的团队选择，并覆盖双语说明与真实客户端。
- Maven SNAPSHOT 元数据使用最新 timestamped version 与独立 extension/classifier；Hosted/Group 提供 SHA-512、SHA-256、SHA-1、MD5 sidecar。
- npm Hosted/Proxy/Group 提供 package-version 元数据和冷缓存 Group tarball URL 重写，支持 Corepack 固定版本安装。
- 认证 PUT 会替换过期 Maven staging session，使中断发布可重试。
- 修复 npm cold `package-lock.json` tarball、旧 scoped package 路径、无害 dot segment、旧 metadata integrity、无效 dist-tag、大 packument、online-to-offline `npm ci` 和 member-owned 终态审计。
- OCI manifest 媒体类型选择支持重复 `Accept` 请求头。
- OpenAPI 检查不再在运行中的 Vite 下重装依赖，默认 lazy-route 异常页改为双语恢复界面。
- 公开制品深链接可跨越 browse 第一页解析；修复 PostgreSQL OCI 过期上传回收、CI 生成物与生命周期顺序。
- 聚合容量包含 PyPI 和 Go；PyPI 晋级、复制、恢复阻止文件成员变化造成的部分版本，并在精确重放时刷新检查点。
- 用户管理审计正确记录执行操作的管理员，自助改密记录本人。

### 安全

- Go 工具链和发布镜像升级到 1.26.6，使发布就绪检查基于已修补标准库。
