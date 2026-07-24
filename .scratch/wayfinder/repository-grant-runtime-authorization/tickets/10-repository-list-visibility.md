---
title: Repository 列表 scoped 可见性
label: wayfinder:research
state: closed
assignee: codex
depends_on:
  - 09-group-member-grant-enforcement
---

## Question

V2 Repository 列表、详情和关联管理资源应如何对拥有 repository-scoped grants 但不是
全局管理员的 principal 展示？需要决定 read/write/admin scopes 分别能否看到 metadata、
列表为空时的响应以及 V1 管理面兼容边界，同时避免通过列表、分页或错误码泄漏未授权
Repository 的存在性。

## Acceptance criteria

- 明确 V2 list/detail/associated-resource 路由的可见性矩阵，以及 scoped principal 与
  administrator 的响应语义。
- 说明是否需要将现有全局管理员 gate 拆分为 resource-scoped 授权，并识别 V1 保持不变的
  兼容约束。
- 将决定写入授权计划；若需要实现，拆出精确的后续工作票与验证范围。

## Resolution

Repository grants are capabilities for a known Repository, not global discovery
permissions. `read` (including inherited read from `write`/`admin`) exposes the
known Repository detail, retention policy, artifacts and publish-session reads;
`write` permits Repository-scoped mutations; `admin` manages its grants.

`GET /api/v2/repositories`, audit listing, Repository/Group lifecycle and
other cross-Repository discovery stay administrator-only. Therefore a scoped
principal never receives a filtered list, page token, or empty-list signal from
which it could infer unseen Repositories. V1 management routes remain unchanged.
The existing `withRepositoryScope` wrappers already implement the known-resource
part of this matrix, so this decision requires no code change.
