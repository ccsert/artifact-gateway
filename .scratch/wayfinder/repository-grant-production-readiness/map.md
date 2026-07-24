---
title: Repository grants production readiness
label: wayfinder:map
tracker: local-markdown
---

## Notes

长期目标：将已交付的 Repository grants runtime authorization 纳入可验证的生产恢复
流程。每个会话先读取 `docs/repository-grant-authorization-plan.md`、
`docs/release-readiness.md`、`docs/recovery-runbook.md`、本地图和所领取的工作票；
每次只领取一个未阻塞工作票。

术语：Repository grant set 是 PostgreSQL 中版本化、按 Repository 归属的授权策略；
恢复一致性是从同一备份集恢复 PostgreSQL 与 MinIO 后，授权决策、审计记录和对象可用性
共同符合备份时的状态。

## Decisions so far

- [验证恢复后的 Repository grants 强制执行](tickets/01-grant-restore-enforcement.md) —
  已实现：隔离 Postgres/MinIO 恢复后通过 Native Raw 的 granted/denied bearer 请求、
  grant ETag、审计和有界 metrics 共同证明恢复一致性。

## Fog

无。发布清单给出固定标签 PromQL 查询；告警阈值和责任分工由部署环境的基线与值班制度
决定，不在应用代码中硬编码。恢复与运行时回退的安全操作已记录在发布及恢复手册中。
