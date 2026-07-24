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
- [有界授权拒绝指标](tickets/05-bounded-authorization-metrics.md) — 已实现：grant
  拒绝按固定 format、来源和原因计数，不包含 actor、Repository、路径或 endpoint 标签。
- [审计 API 授权字段契约](tickets/06-audit-api-contract.md) — 已实现：V2 管理审计 API、
  OpenAPI 和 Console 统一暴露可选且有界的授权决策字段，V1 响应不变。
- [Group 成员级授权语义](tickets/07-group-member-authorization.md) — 已决策：仅显式绑定
  Repository 的成员适用 managed grants；拒绝候选在 cache/source 前跳过，耗尽时返回拒绝。
- [Group 成员 grants 绑定与执行](tickets/09-group-member-grant-enforcement.md) — 已实现：
  OCI、Maven、Raw、Conan 的已绑定成员在缓存与 upstream 前统一执行 grants，来源缓存可撤权。
- [Repository 列表 scoped 可见性](tickets/10-repository-list-visibility.md) — 已决策：
  grants 仅授权已知 Repository 的操作，不授予列表、审计或跨资源发现能力。
- [CI lint 收口](tickets/11-ci-lint-cleanup.md) — 已实现：无 suppression 清除全部静态
  检查问题，并通过单元、集成、OpenAPI、Console 与四种协议 E2E 门禁。

## Fog

无。Repository grants runtime authorization 的范围、兼容边界和交付门禁均已收口。
