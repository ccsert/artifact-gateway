---
title: 验证恢复后的 Repository grants 强制执行
label: wayfinder:task
state: open
depends_on: []
---

## Question

隔离 PostgreSQL/MinIO 备份恢复演练应如何证明：恢复前保存的受管 Repository grant set、
关联审计记录和对象数据在恢复后共同恢复，并且真实协议入口仍拒绝无 scope 的主体、允许
有 `repositories:read` scope 的主体？

## Acceptance criteria

- `make backup-restore-readiness` 或等价的隔离自动化演练在恢复前创建受管 grants，
  在恢复后验证其版本、grants 和审计记录存在。
- 演练通过至少一个真实原生协议读入口验证相同对象：未授权主体得到该协议既有拒绝，
  授权主体成功读取；不得只断言数据库行。
- 运行手册和发布清单记录此验证及失败时的安全处置；不得将 token、主体或 Repository
  标识加入 Prometheus 标签。
- 修改通过 `make lint`、`make test`、`make integration-test`、OpenAPI/Console 门禁和
  受影响协议 E2E。
