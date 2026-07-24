---
title: 拒绝决策审计
label: wayfinder:task
state: closed
---

## Question

如何在不记录 token、credential 或 actor 派生标签的前提下，将 Repository grant 拒绝原因
持久化并保证 Postgres 与 Memory 一致？

## Resolution

`resolver_audit_log` 增加 `authorization_source` 和 `authorization_reason`，拒绝记录仅保存
有界策略值；Memory 断言和 Postgres integration round-trip 已覆盖。实现提交为
`6075b3d8`，待下一次发布流程纳入远端主线。
