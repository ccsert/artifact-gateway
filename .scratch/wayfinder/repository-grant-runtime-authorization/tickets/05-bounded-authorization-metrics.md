---
title: 有界授权拒绝指标
label: wayfinder:task
state: open
assignee:
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
