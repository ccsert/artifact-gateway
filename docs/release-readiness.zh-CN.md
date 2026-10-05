# Artifact Gateway 受控部署就绪

[English](release-readiness.md) | [文档索引](README.zh-CN.md)

本清单区分仓库发行证据与目标部署验收。发行要求干净 main、适用 CI 成功、主线制品
和不可变镜像候选；不表示已在生产执行清单。v0.6.0 准备中的新增数据有界门禁是
`make backup-transfer-test` 和 `make upgrade-readiness`；更广的协议、性能和目标部署
验收仍独立保留。

本套件覆盖 OCI、Maven、Raw、Conan、npm、PyPI 的 Hosted/Proxy 生命周期与分发，以及
Go Module 原子 Hosted 发布和 Hosted/Proxy/Group 读取。

门禁还通过真实 Debian 客户端和签名快照恢复演练覆盖未公开的 APT Hosted 预览；通过不等于把 APT Hosted 纳入 0.1.0 兼容声明。

从干净 checkout、配置本地 `.env` 的 Docker Desktop 工作站执行，无需外部包服务或生产凭证：

```sh
make integration-test
make test
make native-oci-e2e
make native-raw-e2e
make native-maven-e2e
make native-npm-e2e
make native-pypi-e2e
make native-go-e2e
make native-apt-e2e
make apt-signer-rotation-e2e
make cargo-contract
make conan-e2e
make readiness-e2e
make resolver-rotation-e2e
make service-account-rotation-e2e
make oci-performance-e2e
make cache-operations-e2e
make openapi-check
make console-typecheck
make console-check
make console-test
make console-build
make console-e2e
make upgrade-readiness
make backup-transfer-test
make published-native-backup-test
make backup-restore-readiness
```

published-native门禁经HTTPS下载固定官方公开资产，仅使用合成且明确所有权的
PG/RustFS资源，不使用生产凭据。它是Unreleased新增兼容验收，不追溯声称原v0.6.0
验证器能接受自己的trimpath字节。

`make test` 包含隔离的 `dev/dev-status/dev-down` CLI 边界。把输出、Git revision、operator、UTC 起止和偏差写入[发布记录](release-record-template.zh-CN.md)，不得记录 Bearer、存储凭证或未脱敏上游 URL。

## 固定演练镜像

`make upgrade-readiness` 固定正式 v0.5.0 commit
`ea60aea333b29bb60d4ac1b8e2b2a8720563726f`，从该源码构建基线，使用全新 owned
PostgreSQL/S3 资源与合成凭据，不加载 `.env`。单一迁移作业增加 000136–139，第二次
运行必须保持完整元数据摘要不变。读回既有仓库 ID、Group 顺序、授权和对象字节，并在
候选版本通过 API 创建加密目标，通过导出的 Store 接口创建／评估规则状态和事件，再
用 API 读回事件；独立恢复门禁核对安全 API 字段身份／状态。回退将一致的升级前 DB／对象
快照恢复到全新目标，使用匹配的 v0.5.0 软件，然后再前滚。本门禁不证明旧 binary 能
读扩展数据库、向下迁移、所有协议/provider 或已发布镜像。`make backup-transfer-test`
另用任务拥有的 TLS 接收端，证明 binary 与 OCI 软件 profile 的新增 outbox 恢复语义。

`scripts/historical-upgrade-readiness.sh` 保留 v0.1.0 协议历史回归，以及显式
`GATEWAY_UPGRADE_FROM_REF` 覆盖；其共享 volume binary 回退仅为历史证据，不是本次
发行回退批准。下述镜像选项适用于该历史 helper 和 `backup-restore-readiness`，不适用
固定 v0.5.0 门禁。

镜像参数必须是以 `@sha256:<64 位小写十六进制>` 结尾的镜像仓库引用，或已加载的
本地 `sha256:<64 位小写十六进制>` image ID；可变 tag 会被拒绝。启动演练前同时检查
镜像的 revision/version 标签和实际 `gateway version` 输出。候选镜像挂载的迁移、
主题、Compose 与启动文件也必须与 `GATEWAY_READINESS_REF` 一致。

用已校验发行包中的二进制组装本地运行容器，属于发行包演练，不证明已发布的镜像
可以从镜像仓库拉取。记录中应分别保留发行包校验和、本地 image ID，以及镜像仓库
访问失败的结果。

## 受控部署清单

- [ ] 上述 test、integration、各 native E2E、APT signer rotation、Cargo contract 和 Conan E2E 全部通过。
- [ ] Integration 包含 PostgreSQL/RustFS 中 OCI、Maven、Raw、Conan 晋级和断点复制证据，验证对象发布、retry/resume 与 SHA-256；迁移执行两次证明第二次 no-op，并拒绝已应用文件 checksum drift。
- [ ] OCI 真实 publish/pull；Maven 真实 publish/resolve；npm 真实 Hosted/Proxy/Group、离线上游重放；PyPI 真实 twine/pip；Go Hosted 发布与 `go mod download`、Proxy 离线 cache replay 全部通过。
- [ ] APT 预览构建真实 `.deb`，发布/安装签名快照，捕获签名、索引和包 digest，备份 PostgreSQL/RustFS，发布后续快照后恢复原快照，并在 signer 离线时再次安装。
- [ ] Raw 黑盒覆盖 public GET/HEAD/Range、匿名 allow/deny、canonical path、negative cache、Proxy allowlist、上游中断 cache 恢复、Audit 和 metric；Conan 2.21.0 覆盖 handshake、revision recipe/package、cache、checksum failure、匿名和 allowlist。
- [ ] `readiness-e2e` 在 RustFS/PostgreSQL 停止时 `/readyz=503`，恢复后为 `204`。
- [ ] `cache-operations-e2e` 证明 collection 仅管理员可用、执行成功且成功计数增加；确定性 retention 由单测覆盖。
- [ ] OpenAPI 和 Console typecheck/lint/format/accessibility/component/coverage/build/browser 门禁通过，并经 Console proxy 完成管理员 Dashboard session。
- [ ] Maven retention 在请求外执行，保留每 module 的最新版本，只逻辑删除过期多余 coordinate，再由 collector 回收。
- [ ] Resolver rotation 在重启后拒绝旧 Token 派生的 OCI bearer，并允许新 Token。
- [ ] Service Account rotation 证明稳定 Grant 下新旧 credential 重叠、只撤销旧值、禁用账户后拒绝剩余值。
- [ ] OCI performance 默认 50 次缓存 manifest、并发 10、零错误、p95 ≤1 秒。任何覆盖必须写入已批准发布记录。
- [ ] `make backup-transfer-test` 经真实公开导出／恢复，保留非空加密目标、配额规则／状态／事件与 outbox 许可；并发、显式启用的 TLS worker 仅发送可处理项。已取消的过期 claim 与终态不自动重发；活动租约／未来重试、旧 token fencing 和 possibleDuplicate 保留。
- [ ] `make upgrade-readiness` 通过上述固定 v0.5.0 前向迁移、二次 no-op、核心／新数据读回与升级前快照隔离回退／再前滚；源码构建身份与已发布二进制／镜像分发验收分别记录。
- [ ] 迁移 `000095` 前停止新 replication，drain 所有旧 plan 并停止旧 Worker；迁移后只启动新 Worker，再开放 replication/quarantine。每个当前 plan 必须 coordinate 和 digest 同时存在，旧空身份 plan 由新 Worker 失败关闭。
- [ ] Backup/restore 使用隔离 volume，通过 HTTP 创建 OCI、Maven、Raw、Conan、Go 源 Artifact，创建/重放晋级和复制，恢复后验证指令与 Audit。Go 前后运行真实下载并校验三表示 digest，证明备份后 mutation 不存在。
- [ ] 恢复还验证 Raw cache、Quarantine state/reason、Conan Group、Grant version/content 和 Native Raw deny/allow。隔离 rehearsal 通过后才可对发布环境运行 `make backup-drill`。
- [ ] `native-apt-e2e` 证明 signing-state、Release、两份签名、direct/by-hash index 与 package byte 在恢复后精确一致；signer key volume 明确不属于此证明。
- [ ] `apt-signer-rotation-e2e` 使用 signer-owned 只读私钥 volume 和 CA 验证 HTTPS，证明 Debian client 在 old、overlap、new 阶段行为，并证明旧 key-only client 明确拒绝新快照。
- [ ] 检查 `/metrics`、Audit、capacity、allowlist、Grant、quota、OIDC issuer/audience。授权拒绝与后台队列使用有界聚合，不增加 actor/Repository label。

建议检查：

```promql
sum by (format, authorization_reason) (
  increase(artifact_gateway_repository_authorization_denials_total[15m])
)

sum by (kind, format, state) (
  artifact_gateway_background_jobs{state=~"pending|retrying"}
)
max by (kind, format) (
  artifact_gateway_background_queue_oldest_actionable_age_seconds
)
```

## 默认运维策略

| 区域 | MVP 默认 |
| --- | --- |
| Hosted 来源 | PostgreSQL 权威 metadata + RustFS S3-compatible byte |
| 外部 Proxy | 精确上游 host 未进入 allowlist 时禁用 |
| 认证 | CI/app 使用可轮换 Service Account；生产人类身份用 HTTPS RS256 OIDC；静态 Token 只作本地 break-glass |
| 授权 | 拒绝未匹配的仓库读取者。未配置任何 `GATEWAY_REPOSITORY_READERS` 模式的部署会拒绝无授权的已认证调用方，而不再放行；`GATEWAY_LEGACY_READ_DEFAULT=allow` 可恢复 0.4 之前的姿态并在设置期间打印启动告警。升级与回滚步骤见 [legacy group migration](legacy-group-migration.zh-CN.md) |
| OCI cache | 按内容 read-through，TTL 宽限后每五分钟清理 |
| Maven cache | component 15m，metadata 与 negative 1m |
| 备份 | PostgreSQL + RustFS；演练目标 RPO 24h、RTO 30m |
| OCI 性能 | 50 请求、并发 10、0 错误、p95 ≤1000ms |
| Cache 运维 | Resolver 被拒，Admin collection 增加成功计数 |
| Upgrade | 固定 v0.5.0 `ea60aea…`，000136–139、二次 no-op、核心／新数据读回、升级前快照回退与前滚 |

## 架构

```mermaid
flowchart LR
  clients[Docker / ORAS / Maven / Gradle / npm / pip / Go] --> gateway[Artifact Gateway]
  gateway --> auth[Service Accounts / OIDC / break-glass tokens]
  gateway --> postgres[(PostgreSQL metadata / audit / coordination)]
  gateway --> cache[(RustFS S3-compatible object storage)]
  gateway --> proxy[Allowlisted external Proxy]
  gateway --> telemetry[Metrics / OTLP traces]
```

## 已知限制

- V1 生命周期覆盖 OCI、Maven、Raw、Conan、npm、PyPI、Go；晋级复制均发布已验证 Artifact。Go 把 info/mod/zip 作为一个快照、拒绝 Proxy 目标并在 Worker 发布前复查隔离。恢复 rehearsal 保留任务和 plan，但不要求恢复后 Worker 完成它们。
- Raw Hosted 支持认证 PUT/DELETE、单 Range GET/HEAD、派生 checksum 与 resumable upload；不支持条件更新和非 HTTP 客户端工具。Conan 支持 Conan 2 Hosted、revision delete/restore、Group/Proxy、晋级复制；不支持 Conan 1、remote copy 或通用上游 index aggregation。
- 静态 Token 轮换只有 Gateway 重启后才撤销已签发 bearer。OIDC 撤销服从 Token expiry/IdP，JWKS 缓存五分钟。
- Cache collection 异步；宽限期内对象可能仍被活跃 index 引用。

## 回滚

1. 停新部署流量及所有新 evaluator/worker，保留日志、指标和当前一致状态；重新启用邮件前复核外部副作用。
2. 本门禁证明的 v0.5.0 回退，将一致的升级前 PostgreSQL／对象快照恢复到全新隔离目标，使用匹配旧软件、配置和独立托管密钥，丢失快照后变更。仅 binary 回退到扩展 DB 必须另有明确兼容证据；仅增量迁移不足以批准。迁移仅向前，不执行 down migration。
3. 等 `/readyz=204`，执行已知 OCI/Maven 认证读取。
4. 若涉及 metadata/cache，按[恢复手册](recovery-runbook.zh-CN.md)从同一备份集同时恢复 PostgreSQL/RustFS。V2 受影响时再验证 Raw GET 与 Conan revision；已管理 Repository 同时验证一个 granted principal 成功和另一个 ungranted principal 被拒绝，不要为诊断删除 Grant。
5. 轮换可能暴露的 resolver、admin、object-store credential，并记录事件与最终验证。

## V2 匿名策略运维

匿名默认关闭。只有 owner 批准全局与目标双重开关后，才在 Group 和每个允许匿名服务的 member Repository 开启。任一 member false 会将其及其 cache/upstream 排除。

使用未认证读取、`actor=anonymous` Audit 和匿名指标验证。回滚时先关闭成员，再关闭 Group 并确认返回协议 challenge；之后才可回滚应用。Schema 字段为增量且仅向前，不得删除；需要修正时使用前向补偿迁移并复验旧协议、策略拒绝和 Audit query。
