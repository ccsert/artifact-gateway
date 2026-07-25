---
title: 按行为域拆分测试并完成全量验证
label: wayfinder:task
state: closed
assignee: codex
depends_on:
  - tickets/04-protocol-shared-authorization-cache.md
---

## Question

生产代码边界稳定后，测试文件如何按 Hosted lifecycle、grants/recovery、协议状态机和 adapter
contract 拆分，并用全量门禁证明重构行为等价？

## Acceptance criteria

- 大型测试文件按行为域拆分，测试名和 fixture 更贴近对应 Module。
- 不降低覆盖面，不删除关键授权、恢复和协议 E2E 断言。
- 通过 `make lint`、`make test`、`make integration-test`、`make openapi-check`、
  `make console-typecheck`、Native OCI/Raw/Maven 和 Conan E2E。

## Progress

- `repository_api_integration_test.go` 按 management/legacy contract、Native OCI、Native Raw 和
  Native Maven lifecycle/maintenance 拆分为四个 integration 测试文件。
- `conan_test.go` 按核心协议、authorization、proxy 网络安全和 Conan 2 client E2E 拆分。
- `raw_test.go` 按核心 HTTP 协议、cache/proxy 和 authorization 拆分。
- 拆分暴露 `TestPostgresHTTPIntegration` 对全局 Maven tombstone claim 顺序的隐式假设；
  断言已改为精确匹配本测试创建的 object key，从而避免跨文件执行顺序影响。

本轮验证已通过：

- `go test -count=1 ./internal/app`
- `go test -count=1 -tags=integration ./internal/app`
- `git diff --check`
- `make lint`
- `make test`
- `make openapi-check`
- `make console-typecheck`
- `make integration-test`
- `make native-raw-e2e`
- `make native-oci-e2e`
- `make native-maven-e2e`（首次 Maven Central TLS 握手中断，重试后通过）
- `make conan-e2e`

## Resolution

测试已按与生产 Module 一致的行为域重组，所有原有测试函数和关键授权、恢复、cache、
协议 E2E 断言均保留。集成测试已消除对跨测试 tombstone claim 顺序的依赖；测试现在只认领并
删除自身创建的 Maven object intent。
