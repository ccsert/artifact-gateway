# OpenAPI 治理计划

[English](openapi-governance-plan.md) | [文档索引](README.zh-CN.md)

状态：治理基线已实现。本文定义 Native Hosted 契约的源文件、生成和评审边界。

## 目标源布局

手工维护的源是多文件 YAML 树：

```text
api/openapi/native-hosted.yaml
api/openapi/components/*.yaml
api/openapi/management/*.yaml
api/openapi/protocols/*.yaml
```

`native-hosted.yaml` 是公共入口；`management.yaml` 保存完整管理设计，`management-runtime.yaml` 是活跃 Repository 管理路由的生成服务端投影。`native-hosted-v1.json` 是版本化 bundle，禁止手改；Console、API diff、发布附件和外部消费方均使用该 resolved 文档。

## 构建与评审门禁

- `make openapi-bundle`：解析 YAML 入口及引用，生成版本化 JSON。
- `make openapi-check`：重新生成并检查 diff、验证 OpenAPI、运行契约测试，并检查 Console client 与 Go 管理契约生成物。
- `make openapi-generate-admin`：临时打包活跃管理投影，重新生成 `internal/admin/openapi/generated.go`。

每个契约变更必须在同一可评审变更中更新源 fragment、bundle、契约测试和受影响的生成客户端。Redocly 锁定在 `tools/openapi/package-lock.json`，`oapi-codegen` 使用 Go `tool` directive 固定；CI 在接受生成输出前运行 `make openapi-check`。

## 代码生成边界

管理 API 使用常规资源路由。`oapi-codegen` 为活跃 Repository、Maven publish session 和 Maven artifact list 生成类型及标准/strict server interface。运行时使用生成的标准 HTTP wrapper 绑定 path/query/idempotency header，手写代码保留授权、事务和领域决策。

`:commit` session 路由因 path 参数带字面后缀而使用小型标准库 bridge；strict interface 同时生成，作为后续管理路由迁移的 typed extension point。

## 管理运行时覆盖

`management.yaml` 是完整评审设计，不是运行时权威。`management-runtime.yaml` 是生产 `/api/v2` 的权威契约，其 bundle、server/client code 和 assembled-handler 一致性测试必须同步变化。

当前生成区域包括：Repository；Maven publish session 与 artifact list；Artifact detail 与逻辑删除；UUID V2 Group；带 `ETag`/`If-Match` 的 Grant；默认 `keepDays=30`、`minimumVersions=1` 的版本化 Retention Policy 及格式感知持久任务。

向运行时投影加入延期路由前，必须先实现领域 aggregate、Memory/PostgreSQL Store、授权行为和 handler 契约测试。生成 wrapper 只负责路由与参数绑定，不能发布占位能力。

`internal/app/openapi_runtime_contract_test.go` 枚举每个已发布 operation，通过组装后的 Gateway 执行，拒绝 operation ID 缺失/重复与未注册路由，并验证代表性成功响应的状态、header 与 JSON schema。新增 route family 或改变响应时必须增加 strict fixture；功能测试继续负责领域分支和授权场景。

协议 API 不由 handler generation 驱动。OCI Registry V2、Raw、Maven、Conan 首先服从官方规范和生态客户端；`api/openapi/protocols/*.yaml` 记录 Gateway overlay，兼容矩阵记录规范引用和执行门禁。OpenAPI 描述暴露面，handler/contract/E2E 才是符合性证据。

## 评审顺序

1. 只编辑 YAML 源 fragment，并更新对应 overlay 说明。
2. 运行 `make openapi-check`，在同一变更中包含 JSON、Console 和 Go 生成文件。
3. 运行受影响协议 E2E 和 `go test ./...`。
4. 协议 handler 抽取或新协议支持不得混入纯契约生成变更。
