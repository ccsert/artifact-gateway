# Gitea 后端参考与后续目标

[English](gitea-backend-reference.md) | [文档索引](README.zh-CN.md)

> 本文的运维目标在产品战略上已由[完整制品库路线图](full-artifact-repository-roadmap.zh-CN.md)取代；Preflight 和证据收集仍是必需的发布基础设施。

## 参考基线

分析使用 Gitea `v1.24.7`、revision `99053ce4fa2b45f1bca5837418c0c57f793ca824` 的本地只读 checkout，位于仓库外 `../gitea-reference-v1.24.7`，不得提交。

Gitea 使用 MIT License。其实现只作为设计和测试参考；Artifact Gateway 新代码必须独立编写，并在适用时保留版权声明，不能整体复制。

## 当前位置

Artifact Gateway 已拥有 OCI、Maven、Raw 和 Conan 读取所需核心运行时：PostgreSQL 元数据、S3 兼容对象存储（默认 RustFS）、协议自有缓存/维护、出站 allowlist、配额、OIDC、Grant、审计、指标、就绪和异步回收，以及隔离集成/协议/升级/备份恢复门禁。

本地 Docker 门禁只能证明隔离环境中的代码行为，不能证明目标部署的凭证、外部依赖、TLS、策略或 RPO/RTO 恢复能力。这是剩余后端风险。

## 可借鉴的 Gitea 模式

| Gitea 参考 | 说明 | Artifact Gateway 决策 |
| --- | --- | --- |
| `cmd/doctor.go`、`services/doctor/doctor.go` | 命名、有序、可列出并可选择中止的检查 | 提供只读、CLI-first preflight 和机器可读输出，不新增 HTTP 路由 |
| `modules/storage/storage.go` | 统一存储边界支持生命周期与迁移 | 保持现有 S3 边界，缺少真实需求前不泛化 provider |
| `cmd/migrate_storage.go` | 状态变更是独立显式 CLI 操作 | 备份、恢复和修复不得成为 preflight 副作用 |
| `routers/api/packages/*` | 协议保留格式专用校验与响应 | 继续由 `internal/protocol/{oci,maven,raw,conan}` 所有，不引入通用 handler |

## 目标

**生产就绪证据闭环：** 不改变 schema、V1/V2 路由、认证授权或协议响应，提供可重复的操作员工作流，证明目标部署适合发布并能给出恢复目标证据。

### 范围内

1. CLI-first `release preflight`，具有稳定命名检查和 JSON 输出；只读验证 PostgreSQL、对象存储、可选 OIDC/JWKS、readyz、allowlist、缓存配额与 Grant，并脱敏 secret、Token、数据库密码和完整上游路径。
2. 生产证据收集器读取现有 `/readyz`、`/metrics`、`/api/v1/audits`、`/api/v1/operations/cache`，使用操作员提供的凭证并输出可链接到发布记录的脱敏目录。
3. 受控恢复演练 wrapper，编排现有备份恢复、记录时间与摘要，验证一条允许读取和一条拒绝读取；没有显式隔离演练标记时拒绝执行。

### 明确不做

- 不做 Console、协议扩展、发布、复制或删除工作流。
- 不新增公开管理 HTTP、V1/V2 alias 或 OpenAPI 变化。
- Preflight 不自动修复、迁移、删除对象、修改 Grant 或轮换凭证。
- 本目标不新增存储 provider 抽象或 schema 迁移。

## 交付顺序与退出标准

1. **Preflight 基础：** check registry、`list/run/--format json`、退出码和脱敏/分类单测；错误依赖必须生成命名失败且不泄密。
2. **目标检查：** PostgreSQL、S3、OIDC、TLS、策略和既有 endpoint；集成夹具覆盖成功、依赖不可用、无效 OIDC/TLS 和脱敏。
3. **证据收集：** 生成时间戳 manifest 和脱敏 snapshot；每条记录可归属目标 revision 且不含 supplied secret/Bearer。
4. **恢复演练：** 显式安全确认，捕获 RPO/RTO；恢复后验证 ready、允许/拒绝读取、审计和摘要。
5. **发布采用：** 仅把新后端命令加入就绪清单和模板；生产记录链接证据附件及单独授权的演练。

## 架构边界

命令包可依赖 `internal/config`、Repository/Object Store constructor 和 HTTP client，不得导入或修改协议 handler/store。`internal/app` 保持 composition root，协议缓存和生命周期继续属于各自包。

生产访问、凭证来源、证据保留位置和隔离标记都是部署决策；本地结果不能替代生产记录。

## 阶段 1 状态

Gateway 二进制已提供：

```sh
gateway preflight list
gateway preflight run --format json
gateway preflight run --check postgres --check object_store --format json
```

`make preflight` 在已有 Compose Gateway 中执行。仅 pass/skip 返回 0，检查失败返回 1，CLI 用法错误返回 2；JSON 不含凭证、Token、完整数据库 URL 或依赖错误文本。

## 阶段 2 状态

证据收集器读取现有就绪、指标、审计和缓存接口，输出 `manifest.json` 与每接口一个脱敏 JSON：

```sh
export GATEWAY_EVIDENCE_ADMIN_TOKEN='injected-at-runtime'
make evidence \
  GATEWAY_URL='https://gateway.example.test' \
  EVIDENCE_OUTPUT='.artifacts/release-evidence/20260725T000000Z' \
  GIT_REVISION='candidate-revision' \
  IMAGE_DIGEST='registry.example/gateway@sha256:...'
```

输出目录必须为空。Manifest 只保存目标 URL 哈希；指标缩减为固定聚合 allowlist；审计只保留 outcome/format 计数；不写入 Token、URL、actor/path/upstream 或响应错误体。
