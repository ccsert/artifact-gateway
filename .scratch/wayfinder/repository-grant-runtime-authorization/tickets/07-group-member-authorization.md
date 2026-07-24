---
title: Group 成员级授权语义
label: wayfinder:research
state: closed
assignee: codex
depends_on:
  - 05-bounded-authorization-metrics
---

## Question

对于 Maven/OCI/Raw/Conan Group 的多成员解析，非管理员缺少某一成员 read scope 时，何时应跳过
成员并继续候选解析，何时应拒绝整个请求；如何验证不会通过状态码、缓存或回退泄漏成员存在性？

## Acceptance criteria

- 对每种协议和 hosted/proxy 混合 Group 给出可测试的决策表。
- 明确已授权后续成员的成功回退、没有已授权成员时的拒绝、缓存命中和匿名路径的语义。
- 结论更新授权计划；如实现必要，拆出单独的实现票。

## Resolution

当前只有 Conan member 的显式 `repositoryId` 绑定使用 managed grants 并在拒绝后继续候选；
OCI、Maven、Raw legacy Group 只能执行静态策略，因为其成员未持久化可验证的 Repository
绑定。按名称或 endpoint 猜测绑定会允许错误授权，因此被明确禁止。

所有显式绑定成员的目标语义是：匿名过滤先于候选授权；grant 允许后才能读取该成员缓存或
upstream；grant 拒绝/lookup 失败审计并跳过；全部候选因该原因耗尽时返回现有协议拒绝而非
`404`。缓存项必须对其来源成员重新授权。未绑定成员保持格式当前的静态兼容行为。

实现已拆分至 [Group 成员 grants 绑定与执行](09-group-member-grant-enforcement.md)。
