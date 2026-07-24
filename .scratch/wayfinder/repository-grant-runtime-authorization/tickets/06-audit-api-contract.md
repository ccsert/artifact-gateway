---
title: 审计 API 授权字段契约
label: wayfinder:task
state: closed
assignee: codex
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

## Resolution

新增管理员专用 `GET /api/v2/audits`，支持 `group`、`repository`、`limit` 查询。V2
`AuditRecord` 使用 camelCase，并将 `authorizationSource`、`authorizationReason` 作为可选
字段；当前值是有界策略词汇，消费者必须接受未来新增的有界值。`/api/v1/audits` 保持原有
响应形状。

OpenAPI bundle、生成的管理 server 和 Console client 均包含 `AuditRecord`、`AuditList` 与
`listAudits`。Memory handler、Postgres audit round-trip、OpenAPI 契约及 Console 类型检查
覆盖该契约。
