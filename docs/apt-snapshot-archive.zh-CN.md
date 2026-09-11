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

命令在加载服务器配置之前执行，不连接数据库、对象存储或 signer，也不解包到文件系统。成功时退出码为 0，输出 JSON：`integrity: verified`、`signatures: not_checked`、`archiveDigest: sha256:<hex>`、快照/仓库 ID 和包/资产数量。归档错误返回 1，命令用法错误返回 2。

Console 提供与 curl 等价的完整流程：仓库管理员打开 Hosted APT 仓库的**签名快照**页签，再进入**灾备归档**页签，即可导出任一 visible 或 retired 快照，并在同一步拿到归档文件与其 `sha256:` 回执。该页签也能恢复归档，但只接受管理员在备份时独立保存的那份回执，绝不从本次上传字节现算摘要。脚本化恢复，以及没有 `crypto.subtle` 的场景（例如纯 HTTP 源站），仍以命令行导出与 `gateway apt-snapshot verify` 为准。

`integrity: verified` 表示完整字节、Debian 包身份、清单引用和 Release 索引闭合性通过检查；它不证明签名者可信。签名信任需另外使用操作员拥有的纯公钥 keyring 验证 InRelease 与 Release.gpg，并应用受信 fingerprint 策略。归档自带的 fingerprint 不能充当信任根。

## HTTP 与归档契约

- `GET /api/v2/repositories/{repositoryId}/apt/snapshots/{snapshotId}/archive` 要求 `repositories:admin`；普通 reader/writer 和匿名访问不能下载归档。
- 只导出 `visible` 或 `retired` 快照。跨仓库请求为 404，未发布状态或对象损坏为 409。归档包含管理元数据和包内容，响应使用 `Cache-Control: private, no-store`。
- 媒体类型是 `application/vnd.artifact-gateway.apt-snapshot.v1+tar`，响应提供下载文件名和精确 `Content-Length`。先验证所有对象及 Release 引用，再发送响应头；流式读取期间发生错误会中断 HTTP 响应。客户端必须检查下载是否完整并运行校验命令。
- tar 首项为 `manifest.json`，后续是按名称排序且去重的 `objects/sha256/<hex>`。同一快照状态下重复导出字节相同；快照从 visible 变为 retired 会改变清单中的状态，原包和签名资产保持不变。
- 清单版本为 `artifact-gateway.dev/apt-snapshot-export/v1`。清单包含快照标识与签名证据、包身份/发布者/时间、各协议路径的摘要和大小；没有对象存储内部 key、signer 凭据或私钥。
- 校验拒绝非普通文件、未知/重复/缺失对象、无序对象、大小或摘要不符、未知 schema/字段、错误包身份、缺失索引、截断结尾和归档后的附加数据。最大清单 128 MiB，10,000 个包、50,016 个资产、50,000 个去重对象；单对象不超过 1 GiB，Release/InRelease 不超过 16 MiB，Release.gpg 不超过 1 MiB。

## 可信的同仓库恢复

备份入库前执行 `gateway apt-snapshot verify snapshot.tar`，把输出的 `archiveDigest` 保存在独立、可信的备份记录中。OpenPGP 签名覆盖 Release 和索引，**不覆盖**归档清单中的仓库 UUID、快照 ID 或发布者信息；独立保存的完整归档 SHA-256 用于固定这些字段。恢复时不能用本次不可信上传重新计算摘要，并把它作为授权凭据。

在待恢复 Gateway 上同时配置以下变量，公钥文件由操作员独立管理：

```sh
GATEWAY_APT_RESTORE_TRUSTED_FINGERPRINTS=<可信主密钥指纹>
GATEWAY_APT_RESTORE_TRUSTED_PUBLIC_KEYS_FILE=/run/secrets/apt-restore-public-keys.asc
```

恢复默认禁用。指纹集合必须与公钥环精确对应，拒绝私钥文件。容器部署通过 Compose override 只读挂载公钥文件。这两项配置独立于 `GATEWAY_APT_SIGNER_*`，无需 signer endpoint、token、私钥或签名 RPC。沿用现有 RSA 公钥策略和受控轮换的密钥环数量限制。

```sh
# EXPECTED_ARCHIVE_DIGEST 来自备份时独立保存的可信记录。
curl --fail-with-body --show-error \
  --request POST \
  --header "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
  --header 'Content-Type: application/vnd.artifact-gateway.apt-snapshot.v1+tar' \
  --header "X-Artifact-Archive-Digest: $EXPECTED_ARCHIVE_DIGEST" \
  --data-binary @snapshot.tar \
  "$GATEWAY_URL/api/v2/repositories/$REPOSITORY_ID/apt/snapshots/restore"
```

目标必须已经是 active 的 APT Hosted 仓库，并保有**原仓库 UUID**；仅重新创建同名仓库不能代替身份恢复。仓库配置和授权应通过[恢复手册](recovery-runbook.zh-CN.md)单独备份。

签名身份必须匹配已验证公钥的 UID；历史参考 signer 省略 UID 注释的 `Name <email>` 标签也可接受并原样保留。

导入依次验证独立摘要、归档结构、每个对象和真实 `.deb`、InRelease 与 Release.gpg 两份可信签名，以及签名证据。随后从真实包重建当前 v1 publisher 的规范 Packages、gzip、by-hash 和 Release 字节，与全部签名索引比对。不接收任意第三方仓库归档，也不接收语义相同但编码不同的索引；仅让清单摘要自洽不能偷偷加入未经签名的包。

对象意图和容量预留先持久化，再写对象。包版本、成员、pool 路径、快照可见性及成功审计在同一数据库事务中提交。失败不会留下部分包元数据或新可见快照；废弃对象由现有 APT 生命周期 worker 回收。崩溃尝试在一小时后通过快照锁保护的过期流程处理，每次重试使用独立尝试 ID，旧清理任务不会删除活跃恢复所需的对象。

- 要求仓库管理员权限、独立保存的摘要，以及指定媒体类型。
- 不存在的快照仅在其 suite 序号高于当前可见序号、且身份/坐标/pool 路径均无冲突时变为可见。已存在且内容完全一致的 visible 或 retired 快照可以重放并修复对象；不会重新提升 retired 快照。快照状态不变时，重新导出的归档逐字节相同。
- 写对象之前和最终提交时均检查配额；恢复预留容量也约束普通 APT 上传与发布。每次成功记录 `apt.repository_snapshot.restore`，包含归档摘要、尝试 ID 和原始签名证据。
- 每个 Gateway 实例最多同时处理两个恢复请求，tar 上限 64 GiB；应据此准备临时磁盘。成功或失败后删除临时文件。仍受上述对象、清单及签名限额约束，规范化包 control 元数据总量上限 128 MiB。
- 错误包括：400 归档格式或完整性失败、404 仓库不匹配、409 未配置信任或元数据/状态冲突、412 独立摘要不匹配、413 超过大小限制、415 媒体类型错误、422 签名不可信、429 并发已满、507 配额不足。

追加迁移 `000116_native_apt_archive_restore.sql` 保存恢复尝试与对象意图，历史迁移保持不变。快照删除、保留和重新签名恢复见[生命周期说明](apt-hosted-lifecycle.zh-CN.md)；晋级/复制仍属于后续阶段。APT Hosted 保持操作员预览，生产 KMS/HSM 私钥保管尚未完成。

## 遗留实现的分阶段迁移

父项 [#30](https://github.com/ccsert/artifact-gateway/issues/30) 固定对照如下：

- 阶段一开始时的主线基线：`eb302a1bdf3bb9f3cd0ba20a7dd30e0895362323`。
- 保留的旧分支：`codex/apt-hosted-complete-20260818`，`02721dbf058599f54b4968032463885e0067b678`；实现提交 `7fec72de`，后接旧文档提交。

| 旧实现范围 | 处理与当前契约 |
| --- | --- |
| export.go / archive.go / 导出 API | [#36](https://github.com/ccsert/artifact-gateway/issues/36)：保留确定性归档思路，适配现有包成员模型，补全流式失败、长度、归档结尾与包身份检查；Memory/PostgreSQL 增加按 snapshot ID 读取资产，无新迁移。 |
| import.go / 签名信任恢复 | [#37](https://github.com/ccsert/artifact-gateway/issues/37)：同仓库精确恢复：独立备份摘要与公钥、规范签名索引校验、原子元数据、配额、幂等、并发及持久化对象回收。 |
| lifecycle.go / snapshot.go / 持久化生命周期 | [#38](https://github.com/ccsert/artifact-gateway/issues/38)：单独移植删除、保留和恢复触发的新签名快照；不得覆盖主线已经使用的 000107/000108，需要时追加当时主线之后的迁移。 |
| promotion.go / replication.go / scanner 与隔离适配 | [#39](https://github.com/ccsert/artifact-gateway/issues/39)：按当前扫描身份、准入和 Worker 契约分 PR；目标仓库必须重建并签名元数据，不能以源 Release 作为目标权威。 |
| 旧 OpenAPI、生成代码、文档与公开能力声明 | 以当前主线为准重新生成或重写。舍弃旧 Hosted release-candidate 公开声明，保留当前 H3/KMS/HSM 与 Group 聚合签名边界；不回退主线错误包装、Go/Maven 或 Console 修复。 |

旧分支不会随第一阶段完成而删除。只有其有价值行为全部迁移或明确弃用，并记录恢复 SHA 后，才考虑清理。

## 验收入口

`make native-apt-e2e` 中的 Hosted 门禁运行真实 Debian 客户端：发布、重复导出、导出 retired 快照、PostgreSQL/RustFS 备份恢复、signer 离线导出，并比较恢复前后归档字节。最后从归档重建测试目录，在 `--network none` 的 Debian 容器中用操作员公钥验证两份签名，执行 `apt-get update` 和安装。此外，把 PostgreSQL/RustFS 恢复到保留原仓库身份的空基线，禁用 signer，通过管理 API 连续恢复归档两次，验证重新导出逐字节一致，并从恢复后的 Gateway 安装。

`make integration-test` 覆盖 PostgreSQL/RustFS 跨实例导出、中断恢复的对象回收、并发恢复、精确重放和对象修复。Go 测试覆盖权限/仓库隔离、流式中断、损坏/缺失对象、重复/拼接/截断归档与离线 CLI；OpenAPI 契约和生成客户端随主线检查重跑。
