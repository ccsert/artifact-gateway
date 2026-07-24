---
title: Repository grants runtime authorization
label: wayfinder:map
tracker: local-markdown
---

## Notes

长期目标：将 Repository grants 从持久化管理数据升级为统一运行时授权策略，
同时保留 V1 与未受管仓库的静态策略兼容性。每个会话先读取
`docs/repository-grant-authorization-plan.md`、本地图和相关工作票；每次只领取
一个未阻塞工作票。

## Decisions so far

- [授权评估器与兼容边界](tickets/01-runtime-policy.md) — 已实现：受管 grant set
  （版本大于 1）为权威策略，未受管仓库继续执行各协议既有静态策略。
- [原生协议与 Conan 绑定](tickets/02-protocol-enforcement.md) — 已实现：Maven、OCI、
  Raw 与显式绑定 Repository 的 Conan member 都在实际目标仓库上评估 grants。
- [V2 单仓库 scope](tickets/03-v2-resource-scopes.md) — 已实现：Repository 详情、
  retention、Maven 会话和 artifact 路由按 read/write/admin scope 执行授权。
- [拒绝决策审计](tickets/04-audit-decision-persistence.md) — 已实现待发布：审计记录
  保存有界的授权来源和原因，并通过 Memory/Postgres round-trip 验证。

## Fog

- Group 路由的“成员不可见”与“请求被拒绝”的最终外部语义，取决于现有解析器能否在
  不泄漏成员存在性的前提下继续安全回退；先由成员级授权工作票验证。
- 全局 Repository 列表是否应对 scoped principal 过滤，以及没有任何可见资源时应返回
  空列表还是保持管理面管理员入口，取决于管理面可见性契约工作票的决定。
- 指标名称与已有 `/metrics` 兼容方式、仪表盘查询和告警阈值需要在指标工作票中依据现有
  Prometheus 输出定稿。
