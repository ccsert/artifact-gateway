---
title: 收窄 repository_api runtime composition root
label: wayfinder:task
state: closed
assignee: codex
depends_on:
  - tickets/02-postgres-adapter-domain-split.md
---

## Question

`internal/app/repository_api.go` 如何变成薄 composition root，把认证 token、cache operations、
cache invalidation 和 legacy V1 管理端点移到私有 handler Module，同时保持路由和响应稳定？

## Acceptance criteria

- `NewGatewayHandler` 和公开装配点语义不变。
- V1/V2 路由、OpenAPI 生成 adapter、认证/授权顺序和错误响应保持稳定。
- 受影响的 app 单元测试、OpenAPI check、Console typecheck 和协议 E2E 通过。

## Resolution

- `internal/app/repository_api.go` 现在只保留 Gateway runtime composition、OpenAPI mux bridge、
  `GatewayStore` 和 `NewGatewayHandler*` 装配函数；公开装配函数和路由注册顺序保持不变。
- Auth token 与 principal 逻辑移入 `repository_auth.go`；resolver 逻辑移入
  `repository_resolver.go`；metrics 逻辑移入 `repository_metrics.go`。
- Cache operations、repository operations 和 audit API handlers 移入
  `repository_operations_api.go`；legacy V1 OCI/Maven/Raw/Conan group handlers 与 Raw/Conan
  cache invalidation handlers 移入 `repository_legacy_api.go`；JSON response helpers 移入
  `repository_http.go`。
- 没有修改 OpenAPI 生成文件、V1/V2 路由、authenticator 行为、authorization checks、
  status codes 或 error payload construction。
- 已验证：`gofmt`、`go test -count=1 ./internal/app ./contracts`、`make lint`、
  `make test`、`make openapi-check`、`make console-typecheck`、`make integration-test`、
  `make native-raw-e2e`、`make native-oci-e2e`、`make native-maven-e2e`、
  `make conan-e2e`、`git diff --check`。
