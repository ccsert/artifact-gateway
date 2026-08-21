# 备份与恢复演练

[English](recovery-runbook.md)

在安装 Docker Desktop、已经配置 `.env`，并通过 `make up` 启动本地栈的工作站上执行
本演练。脚本会把备份保存在 `.artifacts/`，该目录不会进入源码版本控制。

## 目标

- RPO：两次成功执行 `scripts/backup-drill.sh` 的时间间隔。MVP 演练目标为 24 小时。
- RTO：从宣布开始恢复，到 `/readyz` 返回 204，目标为 30 分钟。

## 演练步骤

1. 记录 UTC 开始时间并建立备份必须保留的证据：创建或获取一个测试制品，记录预期审计
   记录，并通过原生协议客户端解析。验证 V2 时，记录 Raw 规范路径或 Conan revision
   坐标、成员 allowlist 决策，以及读取使用认证还是匿名。验证 Go Hosted 时，记录模块
   路径、版本及 `.info`、`.mod`、`.zip` 三种表示的摘要，然后使用全新 `GOMODCACHE`
   解析。涉及受管仓库时，记录 Grant Set ETag、一个拥有 `repositories:read` 的 Principal
   和另一个没有该权限的独立 Principal；禁止记录任何凭据。
2. 执行 `scripts/backup-drill.sh`，然后确认 PostgreSQL dump 与 RustFS tar archive 均通过
   `shasum -a 256 --check <backup-dir>/SHA256SUMS`。
3. 创建一次可逆的备份后变更，例如一次性 Group 或新增制品版本。记录该资源，并在可行
   时记录其唯一对象摘要；恢复后，它的元数据和无引用对象都必须不存在。
4. 记录 UTC 恢复开始时间，执行 `scripts/restore-drill.sh <backup-dir>`。
5. 确认 `curl -fsS -o /dev/null -w '%{http_code}' http://localhost:8080/readyz`
   返回 `204`；使用管理员 Token 查询 `GET /api/v1/audits`，并解析缓存制品。对 V2 数据，
   还要通过恢复后的 Gateway 解析所记录的 Raw 路径与 Conan 2 revision，确认审计记录仍
   保留格式、Actor、成员、缓存处置和结果。对 Go Hosted，使用另一个全新 `GOMODCACHE`
   再次下载，确认三种表示摘要仍与记录一致。

   通过管理或协议接口，以及对象存储中唯一对象摘要的直接查询，确认备份后变更已经
   消失。对受管仓库，确认记录的 Grant Set ETag 和 Principal 仍然存在：已授权 Principal
   可以读取所记录对象，未授权 Principal 收到该协议正常的拒绝响应。意外放行应作为
   安全事件处理：保持仓库下线、保留备份与审计证据，并由管理员恢复最后已知 Grant Set，
   然后才能重新开放流量。
6. 记录 UTC 完成时间、实测 RTO、用于计算 RPO 的备份时间戳，以及所有失败验证项。

## 安全边界

`restore-drill.sh` 会覆盖正在运行的 PostgreSQL 数据库和 RustFS 数据。只允许在隔离的
演练环境中执行，并且必须先保存所有需要保留的数据。脚本会在恢复两个存储时停止
Gateway，防止新元数据指向被中断状态下的对象。对象 archive 只对锁定的 RustFS 基线
有效；项目不再提供或支持旧对象存储迁移路径。
