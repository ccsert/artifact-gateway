# 发布记录模板

[English](release-record-template.md) | [文档索引](README.zh-CN.md)

每次生产部署都应在获批的发布跟踪系统中复制一份。命令输出保存为 CI 附件或受限日志引用。不得记录 Bearer Token、对象存储凭证、OIDC Token 或未脱敏的上游 URL。

## 候选版本

| 字段 | 值 |
| --- | --- |
| Git revision | |
| 镜像 digest 或 release tag | |
| 目标环境 | |
| 操作员 | |
| 复核人 | |
| UTC 开始时间 | |
| UTC 结束时间 | |
| 变更或事件引用 | |

## 自动化门禁

| 门禁 | 结果 | UTC 起止时间 | 输出或 CI 附件引用 | 偏差 |
| --- | --- | --- | --- | --- |
| make test | | | | |
| make integration-test | | | | |
| make native-oci-e2e | | | | |
| make native-raw-e2e | | | | |
| make native-maven-e2e | | | | |
| make native-npm-e2e | | | | |
| make native-pypi-e2e | | | | |
| make native-go-e2e | | | | |
| make conan-e2e | | | | |
| make readiness-e2e | | | | |
| make resolver-rotation-e2e | | | | |
| make service-account-rotation-e2e | | | | |
| make oci-performance-e2e | | | | |
| make cache-operations-e2e | | | | |
| make openapi-check | | | | |
| make console-typecheck | | | | |
| make console-check | | | | |
| make console-test | | | | |
| make console-build | | | | |
| make console-e2e | | | | |
| make upgrade-readiness | | | | |
| make backup-restore-readiness | | | | |
| make native-apt-e2e | | | | |

## 生产验证

| 检查 | 证据引用 | 复核人 |
| --- | --- | --- |
| 部署后 readyz 返回 204 | | |
| OCI 和 Maven 认证读取成功 | | |
| 受影响时 Raw GET 和 Conan 2 revision 读取成功 | | |
| 已检查授权拒绝指标且基数有界 | | |
| 已检查本次上线范围的审计 | | |
| 已检查缓存容量和配置配额 | | |
| 已检查上游 allowlist 和 Repository Grant | | |
| 已检查 OIDC issuer、audience 和 HTTPS 配置 | | |
| 管理员已检查缓存收集状态 | | |

## 批准与回滚

| 字段 | 值 |
| --- | --- |
| 批准人 | |
| 批准 UTC 时间 | |
| 上一镜像 digest 或 tag | |
| 回滚负责人 | |
| 恢复手册 | docs/recovery-runbook.zh-CN.md |
| 偏差和已接受风险 | |
