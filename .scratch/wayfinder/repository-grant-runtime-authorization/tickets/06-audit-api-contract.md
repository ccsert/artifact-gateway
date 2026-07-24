---
title: 审计 API 授权字段契约
label: wayfinder:task
state: open
assignee:
depends_on:
  - 04-audit-decision-persistence
---

## Question

如何将审计中的 `authorizationSource` 与 `authorizationReason` 稳定地暴露给管理 API 和
OpenAPI 消费者，并避免把未定义、敏感或高基数字段变成 API 契约？

## Acceptance criteria

- 审计响应模型、OpenAPI 定义和 Console 类型一致包含两个可选有界字符串字段。
- handler、Memory/Postgres integration 和 OpenAPI 契约测试覆盖 grant 拒绝记录。
- 文档说明字段仅在授权决策相关记录上出现，以及允许值的兼容性规则。
