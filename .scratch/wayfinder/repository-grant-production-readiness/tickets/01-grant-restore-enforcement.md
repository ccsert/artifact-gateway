---
title: 验证恢复后的 Repository grants 强制执行
label: wayfinder:task
state: closed
assignee: codex
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

## Resolution

- 隔离恢复演练在备份前创建 Raw Hosted Repository、对象与受管 grant set，并以
  `/auth/token` 签发的短期 Gateway Bearer token 验证 granted reader 与 denied reader。
- 恢复后演练验证 grants ETag 为 `2`、principal/scopes 仍存在、同一 Native Raw 对象
  对 granted reader 返回 `200`、对 denied reader 保持 Basic challenge `401`，并验证
  `repository_grants` 授权拒绝审计和固定标签 metrics。
- 修复 Postgres `TEXT[]` grants scopes 的读取：使用 `array_to_json` 后显式解码，并添加
  Postgres HTTP replace/list 回环覆盖；恢复脚本只重启既有 Gateway 容器，不重跑迁移。
- 已验证：`make backup-restore-readiness`、`make lint`、`make test`、
  `make integration-test`、`make openapi-check`、`make console-typecheck`、
  `make native-oci-e2e`、`make native-raw-e2e`、`make native-maven-e2e`、`make conan-e2e`。
