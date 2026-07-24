---
title: 授权评估器与兼容边界
label: wayfinder:task
state: closed
---

## Question

如何让显式管理的 grants 成为 Repository 的权威策略，同时不改变未受管仓库的 V1
认证和静态读写配置行为？

## Resolution

实现 `RepositoryAuthorizer`：管理员永久允许；版本大于 1 的 grant set 为权威；空
受管集合拒绝所有非管理员；读/写/admin scope 层级继承；grant 查询失败 fail closed。
版本 1 保持各协议原有 fallback，避免把新控制面错误地解释为旧策略变更。
