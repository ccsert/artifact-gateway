---
title: Group 成员级授权语义
label: wayfinder:research
state: open
assignee:
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
