---
title: 有界授权拒绝指标
label: wayfinder:task
state: closed
assignee: codex
depends_on: []
---

## Question

如何在现有 `/metrics` 输出中增加能按授权决策来源与原因诊断 grant 拒绝的计数器，同时确保
不以 actor、Repository 名称、路径、坐标或 endpoint 作为标签？

## Acceptance criteria

- 拒绝计数按固定且有限的 `format`、`authorization_source`、`authorization_reason` 组合
  输出，或以等价的固定标签集输出。
- 所有 grant-aware 拒绝入口（V2、Native Maven、OCI、Raw、已绑定 Conan member）都会计数。
- Memory/handler 测试验证允许请求不增加拒绝计数、拒绝请求按正确标签增长。
- 文档列出指标、标签基数约束和至少一个运维查询示例。

## Resolution

新增 `artifact_gateway_repository_authorization_denials_total`。它仅接受受管理
Repository grants 的有界决策值，并固定输出 `management`、`maven`、`oci`、`raw`、
`conan` format 及 `scope_not_granted`、`grant_lookup_failed` 两种原因；拒绝来源固定为
`repository_grants`。V2、Native Maven、OCI、Raw 和已绑定 Conan member 的实际拒绝路径
都在审计旁计数，审计写入失败不会阻止计数。静态策略和未认证拒绝不进入此 grants 专用指标。

验证：handler 路径测试覆盖五种 format，边界测试拒绝未受支持标签；`go test ./...`、
`make integration-test`、全部 Native E2E 和 `make openapi-check` 通过。
