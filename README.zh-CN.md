<p align="center">
  <img src="docs/assets/artifact-gateway-hero.png" alt="多种制品流经过统一验证网关，分别进入 PostgreSQL 元数据账本和不可变对象存储" width="100%">
</p>

<h1 align="center">Artifact Gateway</h1>

<p align="center">
  轻量级、协议原生的可信软件制品库。
</p>

<p align="center">
  <a href="README.md">English</a> ·
  <a href="docs/README.zh-CN.md">文档</a> ·
  <a href="ARCHITECTURE.zh-CN.md">架构</a> ·
  <a href="CONTRIBUTING.zh-CN.md">参与开发</a>
</p>

<p align="center">
  <a href="https://github.com/ccsert/artifact-gateway/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/ccsert/artifact-gateway/actions/workflows/ci.yml/badge.svg"></a>
  <img alt="Go 1.26.6" src="https://img.shields.io/badge/Go-1.26.6-00ADD8?logo=go&logoColor=white">
  <img alt="PostgreSQL 16" src="https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white">
  <img alt="S3 兼容对象存储" src="https://img.shields.io/badge/Object_storage-S3_compatible-06B6D4">
  <img alt="状态：准备中" src="https://img.shields.io/badge/status-preparing-6B7280">
</p>

> [!IMPORTANT]
> **项目状态：公开前准备阶段。** 核心团队仍在持续加固协议契约、运维能力、文档和
> 贡献流程。当前仓库不是稳定公开版本，也尚未进入正式分发阶段；请勿根据当前包版本
> 推断生产支持承诺。

## 为什么选择 Artifact Gateway

Artifact Gateway 有意把运维控制面保持得足够精简：

- **单一 Gateway 二进制。** 同一镜像既可以作为紧凑的单机节点，也可以拆分为 API、
  Scheduler 和 Worker 角色。
- **PostgreSQL 是唯一的协调与数据库依赖。** 仓库状态、鉴权、生命周期任务、租约、
  锁、幂等、审计和运行协调统一由 PostgreSQL 承担。
- **无需 Redis、Kafka、Elasticsearch 或额外消息队列。**
- **经过实测的 Go 资源占用。** 当前本地 Docker 基线产出的 Linux/arm64 Gateway
  二进制为 28.88 MiB，运行时镜像为 36.06 MiB；Gateway 静默平均占用
  53.59 MiB，在 128 并发下观测峰值约 104 MiB。吞吐、完整栈内存、方法与边界详见
  [性能基线](docs/performance-baseline.zh-CN.md)。
- **制品字节不进入数据库。** 经过校验的不可变字节使用 S3 兼容对象存储接口；本地
  开发栈已经内置 RustFS。
- **原生协议优先。** 客户端继续使用熟悉的镜像仓库和包管理器路径，而不是退化为
  只能上传文件的通用对象浏览器。

一句话概括：PostgreSQL 管控制面，S3 兼容存储管字节面，Gateway 负责把二者连接起来，
不需要额外部署一整套中间件。

## 仓库能力

| 格式 | Hosted | Proxy | Group | 说明 |
| --- | :---: | :---: | :---: | --- |
| OCI | ✓ | ✓ | ✓ | Registry V2 上传、Manifest、Tag、Range 与 Referrer |
| Raw | ✓ | ✓ | ✓ | PUT/GET/HEAD、Range、校验和与断点续传 |
| Maven | ✓ | ✓ | ✓ | 默认支持标准 Maven/Gradle 直接发布；可选[严格坐标提交](docs/maven-hosted-publication.zh-CN.md) |
| Conan 2 | ✓ | ✓ | ✓ | 感知 Revision 的发布与生命周期 |
| npm | ✓ | ✓ | ✓ | 原生发布、可信缓存与 Packument 合并 |
| PyPI | ✓ | ✓ | ✓ | Nexus 根路径 twine 上传与 PEP 503/691 读取 |
| Go modules | ✓ | ✓ | ✓ | 标准 GOPROXY 读取；兼容 Nexus 的版本 ZIP 发布 |
| APT | 仅预览 | ✓ | ✓ | 生产密钥托管门禁通过前不公开声明 Hosted 能力 |

Cargo 目前只是分阶段建设的解析与身份基础，NuGet 仍属于路线图工作；两者都不作为
可用仓库格式对外声明。准确且受测试约束的说明以
[协议兼容性基线](docs/protocol-compatibility.zh-CN.md)为准。

Maven Hosted 默认采用与 Nexus 兼容的直接发布，普通 `mvn deploy` 和 Gradle `publish`
不需要 companion 步骤。需要单坐标原子可见性时，可以按仓库开启严格发布，并由 CI 或
发布集成额外调用 Gateway commit。

为降低客户端迁移成本，Maven、npm、PyPI、Raw、Go 的 Hosted/Proxy/Group 也接受
Nexus 风格根路径 `/repository/<name>/...`。兼容入口仍复用原协议的鉴权、校验、审计和
生命周期处理；npm 生成的 Tarball URL、Raw 分页与断点上传地址、PyPI 上传结果和 Go
发布结果都会保持在同一兼容根路径。`maven` 仍由 Gateway 原有 Maven canonical 前缀
保留；Nexus 中同名目标迁移时需要改用其他 Repository 名称。OCI 必须遵守规范固定的
`/v2/` Registry 根路径，因此需要通过
镜像名称或 Ingress 映射迁移，不能伪装成普通 HTTP Base Path。

除协议读写外，当前基础还包括仓库授权、本地用户与 OIDC、Service Account、匿名读取
策略、审计、搜索与浏览、保留策略、可恢复删除、晋升、复制、Webhook、扫描器集成、
隔离、诊断、指标以及备份恢复流程。各领域入口统一收录在[文档索引](docs/README.zh-CN.md)。

## 快速本地启动

前置条件：支持 Compose 的 Docker、Node.js 24+、npm、GNU Make 和 OpenSSL。

```sh
git clone https://github.com/ccsert/artifact-gateway.git
cd artifact-gateway
make dev-bootstrap
make dev
```

`make dev-bootstrap` 会在需要时创建权限为 `0600` 的 `.env`，并且只生成本地
Gateway/PostgreSQL/RustFS 启动所需的六项凭据。它不会打印密钥、覆盖已有真实配置，
也不会在内容无变化时反复生成回滚副本。

`make dev` 会构建并启动 Gateway、PostgreSQL 和 RustFS；Console 依赖缺失时安装锁定
版本，然后等待 API 与 Console 都达到就绪状态。

默认访问 <http://127.0.0.1:4173>，使用 `.env` 中的
`GATEWAY_ADMIN_TOKEN` 登录，再从“制品库”页面创建 Hosted、Proxy 或 Group 仓库。

```sh
make dev-status   # 检查 Console、API 代理、存活与就绪状态
make dev-down     # 只停止当前 checkout 管理的 Console
make down         # 停止 Compose 服务并保留数据卷
```

凭据处理、首个仓库、端口、生命周期命令和故障排查详见
[快速入门](docs/getting-started.zh-CN.md)。

## 架构速览

![Artifact Gateway 轻量级系统架构](docs/assets/artifact-gateway-system-architecture.png)

默认 `standalone` 角色在一个进程中同时运行 API、Scheduler 和 Worker。规模扩大后可按
角色拆分同一镜像，无需额外引入消息队列或服务发现。详见[整体架构](ARCHITECTURE.zh-CN.md)、
[架构图](docs/architecture-diagrams.zh-CN.md)和
[PostgreSQL 能力](docs/postgresql-capabilities.zh-CN.md)。

## 文档导航

| 需求 | 从这里开始 |
| --- | --- |
| 建立本地开发环境 | [快速入门](docs/getting-started.zh-CN.md) |
| 确认协议实际能力 | [协议兼容性](docs/protocol-compatibility.zh-CN.md) |
| 发布 Maven Hosted 坐标 | [Maven Hosted 发布流程](docs/maven-hosted-publication.zh-CN.md) |
| 理解代码与运行边界 | [整体架构](ARCHITECTURE.zh-CN.md) |
| 查看系统、发布与后台任务流程 | [架构图](docs/architecture-diagrams.zh-CN.md) |
| 理解 PostgreSQL 协调能力 | [PostgreSQL 能力](docs/postgresql-capabilities.zh-CN.md) |
| 查看体积、内存与本地并发基线 | [性能基线](docs/performance-baseline.zh-CN.md) |
| 查看当前工程质量 | [项目质量评估](docs/project-quality-assessment.zh-CN.md) |
| 配置身份与权限 | [用户治理](docs/user-governance.zh-CN.md)、[OIDC SSO](docs/oidc-sso.zh-CN.md)、[Service Account](docs/service-account-operations.zh-CN.md) |
| 部署与恢复 | [Kubernetes](docs/kubernetes-deployment.zh-CN.md)、[分布式部署](docs/distributed-deployment.zh-CN.md)、[恢复手册](docs/recovery-runbook.zh-CN.md) |
| 扩展制品格式 | [格式扩展指南](docs/format-extension-guide.zh-CN.md) |
| 修改管理 API | [OpenAPI 治理](docs/openapi-governance-plan.zh-CN.md) |
| 浏览全部维护文档 | [文档总索引](docs/README.zh-CN.md) |

## 开发工作流

```sh
make test
make lint
make vet
make coverage
make build
```

协议、持久化、Console 和部署变更还有各自的专项门禁。禁止手工修改生成的 OpenAPI
客户端与服务端契约。修改代码前请阅读[参与开发](CONTRIBUTING.zh-CN.md)，并将用户
可见变化记录到 [CHANGELOG.md](CHANGELOG.md) 的 `Unreleased` 章节。

新增制品生态必须通过[格式准入规则](docs/format-extension-guide.md)。只增加枚举、路由
占位符或 Console 选项，不代表已经支持该协议。
