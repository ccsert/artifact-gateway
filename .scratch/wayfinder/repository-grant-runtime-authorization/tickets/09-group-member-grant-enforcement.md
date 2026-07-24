---
title: Group 成员 grants 绑定与执行
label: wayfinder:task
state: closed
assignee: codex
depends_on:
  - 07-group-member-authorization
---

## Question

如何为 OCI、Maven、Raw Group 成员持久化可选、格式匹配的 `repositoryId`，并让所有已绑定成员
在 cache/upstream 前统一执行 Repository grants，同时保持未绑定 V1 Group 的静态策略语义？

## Acceptance criteria

- 对 OCI、Maven、Raw 成员新增 Memory/Postgres 持久化的可选 `repositoryId`，其目标必须存在、
  active 且格式匹配；不从名称或 endpoint 推断绑定。
- 在 OCI、Maven、Raw、Conan 的每个已绑定候选成员上，在 cache 和 source 访问前调用
  `RepositoryAuthorizer.ManagedDecision`。
- grants 拒绝或 lookup 失败的已绑定成员会审计并计数，然后跳过；若没有其他可访问候选，返回
  format 原有的 access-denied 响应而非 `404`。
- 缓存命中与负缓存也必须验证其来源成员仍然有授权；拒绝不会读取、写入或命中缓存。
- 未绑定遗留成员、匿名策略和现有静态行为保持兼容；Memory/Postgres、协议 handler、E2E 与
  V1 回归覆盖混合 Hosted/Proxy Group。

## Resolution

OCI、Maven 与 Raw Group members 现在在 Memory/Postgres 中持久化可选 `repositoryId`，
创建时仅接受 active、format-matching 的 Hosted Repository。运行时由
`ManagedGroupMemberDecision` 统一处理显式绑定；受管 grants 拒绝或 lookup 失败会审计、
计入固定标签指标并跳过候选，未绑定成员继续走原有静态策略。

Raw、Maven、OCI 与 Conan 均在 source/cache 前过滤被拒绝成员；正缓存与负缓存均保存并
重新验证来源成员，OCI 的缓存来源撤权后可改走后备成员。每种协议均保留既有的 terminal
access-denied 响应。handler tests 覆盖 grants allow/deny、缓存撤权及 legacy 路径；Postgres
integration 覆盖四种 Group binding 往返；`go test ./...`、`make integration-test`、Native
OCI/Raw/Maven E2E、Conan E2E、OpenAPI 与 Console typecheck 均通过。
