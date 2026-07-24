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

无。

## Fog

- 首先要确认隔离备份恢复演练能否构造受管 grant set，并在恢复后通过真实协议入口同时
  证明允许与拒绝。结果会决定后续是否需要新增备份范围、迁移兼容检查或专门的恢复校验。
- 授权拒绝指标已具备有界标签；仍需根据恢复演练结果确定发布记录应要求的 PromQL 查询、
  告警阈值及责任分工，避免把环境特定阈值硬编码进代码。
- 需要核对恢复/回滚手册是否清晰区分“恢复数据使 grants 重新生效”与“临时回退运行时
  evaluator 使用静态策略”；这取决于首个演练对实际操作步骤的发现。
