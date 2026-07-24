---
title: 管理面可见性与发布门禁
label: wayfinder:research
state: open
assignee:
depends_on:
  - 06-audit-api-contract
  - 07-group-member-authorization
---

## Question

在单仓库 scope 已下放后，如何定义全局列表和 Hosted Group 生命周期的可见性边界，并将授权回归、
迁移、OpenAPI 与原生协议 E2E 纳入 CI 及上线/回滚手册？

## Acceptance criteria

- 决定 scoped principal 的 Repository 列表与 Group 管理语义，并记录拒绝/空列表的安全理由。
- CI 显式运行 Postgres integration、OpenAPI 检查和所有 grant-aware 协议 E2E。
- 发布文档覆盖 migration 顺序、Conan member 绑定顺序、grant 切换、回滚和验证查询。
