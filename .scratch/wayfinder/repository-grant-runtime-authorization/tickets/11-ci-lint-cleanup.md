---
title: Repository grants CI lint 收口
label: wayfinder:task
state: closed
assignee: codex
depends_on:
  - 10-repository-list-visibility
---

## Question

如何清除当前 `make lint` 报告的 errcheck、ineffassign、staticcheck 与 unused 问题，
使 Repository grants 交付满足项目 CI 门禁，同时不改变协议响应、缓存行为、授权或审计语义？

## Acceptance criteria

- `make lint` 通过，不通过新增 lint suppression 隐藏问题。
- 资源关闭、事务回滚和错误处理修复保持现有语义；协议错误字符串和状态码仅做等价规范化。
- `make test`、`make integration-test` 与 OpenAPI/Console 门禁仍通过。

## Resolution

- 已消除 errcheck、ineffassign、staticcheck 与 unused 报告，未新增 lint suppression。
- 关闭游标与事务回滚明确忽略其返回值；Raw 代理保留对 deprecated `DialTLS`
  hook 的拒绝语义，避免绕过已固定的连接路径。
- 已验证：`make lint`、`make test`、`make integration-test`、`make openapi-check`、
  `make console-typecheck`、`make native-oci-e2e`、`make native-maven-e2e`、
  `make native-raw-e2e`、`make conan-e2e`。
