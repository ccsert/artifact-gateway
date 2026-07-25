---
title: 提炼 Raw 协议与 Hosted 生命周期 package
label: wayfinder:task
state: closed
assignee: codex
depends_on:
  - tickets/05-test-boundary-split-and-full-verification.md
---

## Question

如何将 Raw 的 HTTP 协议语法、认证/响应与 native Hosted object lifecycle 从
`internal/app` 提炼为按领域组织的深 Module，同时保持 V1/V2 路由、Raw HTTP
响应、Store interface 和对象回收语义不变？

## Acceptance criteria

- `internal/app` 仅负责 Raw handler 的构造与路由注册，不保留 Raw 协议实现。
- 新 Module 的 Interface 小于现有 handler 的依赖面；不通过导出 app 内部 helper 制造浅 seam。
- Raw 单元、授权、cache/proxy、integration 和真实 HTTP E2E 测试仍覆盖原有行为。
- 通过 `make lint`、`make test`、`make integration-test`、`make native-raw-e2e` 和
  `git diff --check`。

## Progress

已先提炼 `internal/maintenance/raw.Collector`：对象回收的筛选、对象锁、最终引用复核、
字节删除和 collection trace 更新现在位于独立深 Module。它只依赖四个 Store 操作和一个
对象删除操作；`cmd/gateway` 负责装配，测试随 Module 移动。

验证已通过：

- `make lint`
- `make test`
- `make integration-test`
- `make native-raw-e2e`
- `git diff --check`

下一步：Raw HTTP handler 仍直接依赖 `app` 的 Authenticator、RepositoryAuthorizer、
Metrics、OCIObjectStore 和 CacheQuota。必须先提炼协议无关的 runtime ports，避免
`internal/protocol/raw` 反向依赖 `internal/app` 或制造大量 adapter glue。

认证与授权的协议无关前置依赖现已迁至 `internal/authorization`：

- `Authenticator`、OIDC 验签、`RepositoryAuthorizer` 和 managed member grant
  decision 不再位于 `internal/app`；app 仅保留 source-compatible type alias。
- Maven、Conan 与 native Maven 的 Basic credential 流程改用
  `AuthenticateBasic`；OCI token endpoint 与 Hosted guard 改用
  `ResolverPasswordMatches`。协议调用方不再接触常量时间 token 比对或 actor 映射的
  私有实现。
- 新 module 直接测试 Basic principal 映射及 managed grant 对 legacy policy 的覆盖；
  原有 app/OIDC/协议测试继续作为 compatibility regression coverage。

本阶段验证通过：

- `make lint`
- `make test`
- `make integration-test`
- `make native-raw-e2e`
- `git diff --check`

下一步：收窄 Raw object store、quota 与 cache coordinator 的 port，随后才将 Raw
HTTP handler 与其按行为域拆分的测试迁到 `internal/protocol/raw`。

Raw HTTP 表示层现已迁至 `internal/protocol/raw`：canonical path parser、checksum
sidecar 验证、conditional GET 与单 Range 响应由该 package 负责；legacy Raw handler
和 native Raw handler 共用 parser，legacy handler 直接使用其内容响应实现。该 package
有独立的 canonical path、checksum 和 Range 测试，应用层原有 Raw 测试继续覆盖端到端
协议行为。上述完整门禁再次通过。

通用对象存储现已迁至 `internal/objectstore`：Store port、稳定的 NotFound sentinel、
Memory adapter 与 S3 adapter 不再归属 app。`app.OCIObjectStore`、`OCIObjectInfo`、
`MemoryOCIObjectStore` 与 `S3OCIObjectStore` 保留为 source-compatible aliases，
`errOCICacheMiss` 指向同一 stable sentinel，因而现有缓存、native 生命周期和 S3 集成
测试的错误语义不变。新 module 直接覆盖内存对象的复制、Range 读取和 missing-object
错误。完整门禁再次通过。

共享 cache runtime 现已迁至 `internal/cache`：cross-instance Coordinator interface、
PostgreSQL advisory-lock/circuit-breaker adapter 以及 Quota 的 logical-index admission
实现不再归属 app。`app` 保留 `OCICacheCoordinator`、`PostgresCacheCoordinator`、
`CacheQuota`、构造函数和 quota error 的 source-compatible aliases；现有 OCI/Maven/Raw/
Conan cache 仍注入相同 adapter。新 module 直接测试 quota rejection，完整门禁再次通过。

下一步：迁移 RawCache 本体至 `internal/protocol/raw`，直接依赖 `objectstore.Store` 与
`cache.Quota`/`cache.Coordinator`，随后迁移 legacy Raw handler 与测试。

迁移前置依赖已全部就绪：RawCache 所需的对象存储、配额 admission 和跨实例 publication
coordinator 均已有不依赖 app 的 module interface；下一次实现可直接以这些 interface
构造 protocol/raw.Cache，不需要导出 app 内部 helper 或创建反向 import。

RawCache 现已迁至 `internal/protocol/raw.Cache`，并实际由 Gateway runtime 使用。它拥有
Raw index 的 legacy JSON 读取、正/负缓存、request/publication distributed lock、lease
renewal、对象 GC 与 proxy admission；`app.RawCache`/`RawContent` 仅保留 aliases。旧 app
cache implementation 已删除，Raw legacy handler 改为只调用 `Key`、`Load`、`Store`、
`AcquireRequestLock`、`MaxObjectBytes` 等 module 操作。完整门禁再次通过。

Raw protocol package 还拥有 `Client` seam 和 `MemberProxyAllowed`：app 的
`UpstreamClient` 仍实现 client transport 与 DNS-pinned 连接，handler 不再拥有
member proxy 格式/allowlist 判断。新增 protocol-level public/private proxy test，完整门禁
再次通过。下一步只剩 legacy Raw handler 的 runtime adapter 与实现迁移。

legacy Raw handler 已迁至 `internal/protocol/raw.Handler`。它的 Runtime interface 仅
包含 identity、匿名策略、grant decision、member ordering、审计与指标动作；`app` 的
adapter 将既有 Authenticator、RepositoryAuthorizer、Metrics、Audit store 连接进去，保持
V1/V2 路由与 HTTP 响应不变。`internal/app/raw.go` 现在只含上游 transport/DNS pinning 与
audit correlation，`RawHandler` 是构造 adapter；没有残留的 app Raw cache 或 handler
implementation。完整门禁再次通过：`make lint`、`make test`、`make integration-test`、
`make native-raw-e2e` 和 `git diff --check`。
