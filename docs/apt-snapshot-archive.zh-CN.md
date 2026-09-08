# APT 签名快照归档

[English](apt-snapshot-archive.md) | [文档索引](README.zh-CN.md)

APT Hosted 操作员预览支持导出已发布的签名快照，以及无需数据库或 signer 的离线完整性校验。归档保留原始 `.deb`、Packages/gzip/by-hash 索引、Release、InRelease 与 Release.gpg 字节，不重新签名。APT Hosted 的生产签名保管和公开兼容边界仍见[签名说明](apt-hosted-signing.zh-CN.md)。

## 导出与校验

使用仓库管理员身份，按固定快照 ID 下载。当前快照 ID 可从仓库 `apt/signing-state` 的 `currentSnapshot.id` 获取；操作员也可使用以前记录的 ID 导出已被新版本替代的 `retired` 快照。

```sh
curl --fail --show-error \
  --header "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
  "$GATEWAY_URL/api/v2/repositories/$REPOSITORY_ID/apt/snapshots/$SNAPSHOT_ID/archive" \
  --output snapshot.tar
gateway apt-snapshot verify snapshot.tar
```

也可从标准输入校验：

```sh
gateway apt-snapshot verify - < snapshot.tar
```

命令在加载服务器配置之前执行，不连接数据库、对象存储或 signer，也不解包到文件系统。成功时退出码为 0，输出 JSON：`integrity: verified`、`signatures: not_checked`、快照/仓库 ID 和包/资产数量。归档错误返回 1，命令用法错误返回 2。

`integrity: verified` 表示完整字节、Debian 包身份、清单引用和 Release 索引闭合性通过检查；它不证明签名者可信。签名信任需另外使用操作员拥有的纯公钥 keyring 验证 InRelease 与 Release.gpg，并应用受信 fingerprint 策略。归档自带的 fingerprint 不能充当信任根。

## HTTP 与归档契约

- `GET /api/v2/repositories/{repositoryId}/apt/snapshots/{snapshotId}/archive` 要求 `repositories:admin`；普通 reader/writer 和匿名访问不能下载归档。
- 只导出 `visible` 或 `retired` 快照。跨仓库请求为 404，未发布状态或对象损坏为 409。归档包含管理元数据和包内容，响应使用 `Cache-Control: private, no-store`。
- 媒体类型是 `application/vnd.artifact-gateway.apt-snapshot.v1+tar`，响应提供下载文件名和精确 `Content-Length`。先验证所有对象及 Release 引用，再发送响应头；流式读取期间发生错误会中断 HTTP 响应。客户端必须检查下载是否完整并运行校验命令。
- tar 首项为 `manifest.json`，后续是按名称排序且去重的 `objects/sha256/<hex>`。同一快照状态下重复导出字节相同；快照从 visible 变为 retired 会改变清单中的状态，原包和签名资产保持不变。
- 清单版本为 `artifact-gateway.dev/apt-snapshot-export/v1`。清单包含快照标识与签名证据、包身份/发布者/时间、各协议路径的摘要和大小；没有对象存储内部 key、signer 凭据或私钥。
- 校验拒绝非普通文件、未知/重复/缺失对象、无序对象、大小或摘要不符、未知 schema/字段、错误包身份、缺失索引、截断结尾和归档后的附加数据。最大清单 128 MiB，10,000 个包、50,016 个资产、50,000 个去重对象；单对象不超过 1 GiB，Release/InRelease 不超过 16 MiB，Release.gpg 不超过 1 MiB。

本阶段不提供归档导入接口，也不改变快照可见性、保留规则或数据库迁移。已有 PostgreSQL/RustFS 成对备份恢复继续使用[恢复手册](recovery-runbook.zh-CN.md)。

## 遗留实现的分阶段迁移

父项 [#30](https://github.com/ccsert/artifact-gateway/issues/30) 固定对照如下：

- 当前主线基线：`eb302a1bdf3bb9f3cd0ba20a7dd30e0895362323`。
- 保留的旧分支：`codex/apt-hosted-complete-20260818`，`02721dbf058599f54b4968032463885e0067b678`；实现提交 `7fec72de`，后接旧文档提交。

| 旧实现范围 | 处理与当前契约 |
| --- | --- |
| export.go / archive.go / 导出 API | [#36](https://github.com/ccsert/artifact-gateway/issues/36)：保留确定性归档思路，适配现有包成员模型，补全流式失败、长度、归档结尾与包身份检查；Memory/PostgreSQL 增加按 snapshot ID 读取资产，无新迁移。 |
| import.go / 签名信任恢复 | [#37](https://github.com/ccsert/artifact-gateway/issues/37)：单独评审同仓库精确恢复、双签名信任、配额、幂等、并发和对象回收；导入完整性成功不能代替可信签名。 |
| lifecycle.go / snapshot.go / 持久化生命周期 | [#38](https://github.com/ccsert/artifact-gateway/issues/38)：单独移植删除、保留和恢复触发的新签名快照；不得覆盖主线已经使用的 000107/000108，需要时追加当时主线之后的迁移。 |
| promotion.go / replication.go / scanner 与隔离适配 | [#39](https://github.com/ccsert/artifact-gateway/issues/39)：按当前扫描身份、准入和 Worker 契约分 PR；目标仓库必须重建并签名元数据，不能以源 Release 作为目标权威。 |
| 旧 OpenAPI、生成代码、文档与公开能力声明 | 以当前主线为准重新生成或重写。舍弃旧 Hosted release-candidate 公开声明，保留当前 H3/KMS/HSM 与 Group 聚合签名边界；不回退主线错误包装、Go/Maven 或 Console 修复。 |

旧分支不会随第一阶段完成而删除。只有其有价值行为全部迁移或明确弃用，并记录恢复 SHA 后，才考虑清理。

## 验收入口

`make native-apt-e2e` 中的 Hosted 门禁运行真实 Debian 客户端：发布、重复导出、导出 retired 快照、PostgreSQL/RustFS 备份恢复、signer 离线导出，并比较恢复前后归档字节。最后从归档重建测试目录，在 `--network none` 的 Debian 容器中用操作员公钥验证两份签名，执行 `apt-get update` 和安装。这是归档可用性证据，不是 Gateway 归档导入验收。

`make integration-test` 覆盖 PostgreSQL/RustFS 跨实例导出与完整性校验。Go 测试覆盖权限/仓库隔离、流式中断、损坏/缺失对象、重复/拼接/截断归档与离线 CLI；OpenAPI 契约和生成客户端随主线检查重跑。
