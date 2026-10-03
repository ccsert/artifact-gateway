# 离线备份清单验证

[English](backup-manifest-verification.md) | [文档索引](README.zh-CN.md)

此 **Unreleased 基础批次** 验证私有本地文件集的字节与操作者声明。它独立于
`agctl`，也独立于既有[固定 RustFS 物理备份演练](recovery-runbook.zh-CN.md)。

```sh
gateway preflight backup --input /private/backup/manifest.json --format json
```

命令读取文件、向 stdout 写 JSON 后退出，不加载运行配置、不访问 PostgreSQL/S3、
不枚举或停止 writer、不导出备份、不创建完整标记、不恢复数据库/对象、不创建凭据，
也不修改权限、配额或保留。输入必须是操作者控制的不可变本地文件集；并发修改会使
证据失效。

## 结果与一致性边界

| 退出码 | 整体 `status` | 含义 |
| --- | --- | --- |
| 1 | `invalid` | 已支持文件集的文件、库存或已确认 writer 区间与清单矛盾。 |
| 2 | 无可用报告 | 参数错误、清单不可读/权限非私有/过大、JSON 无效或歧义、报告输出失败。 |
| 3 | `unknown` | 字节验证完成、证据缺失/不支持或验证被取消。 |
| 0 | 无备份结果 | 仅帮助。本基础不会授予整体备份成功。 |

报告分别给出 `integrity`（`verified`、`invalid`、`unknown`）、`writerEvidence`
（`declared`、`invalid`、`unknown`）和 `consistency`（**始终 `unknown`**）。
`state: complete` 只是生产者声明，不能证明源枚举或身份、writer 全集、持续停写、
数据库/对象属于同一快照、schema 兼容或可恢复。dump 字节匹配不表示解析或恢复过
PostgreSQL dump；ledger checksum 也没有与源 migration 文件独立比对。

即使字节验证通过且声明完整，仍退出 3。不能据此重开写入、删除源数据、将备份标记
consistent 或批准恢复。独立的[真实传输入口](recovery-runbook.zh-CN.md)执行停写观察、
导出与隔离恢复；其验收不能从本验证器推断。

JSON 报告包含 `schemaVersion: 1`、不含秘密的 `backupId`、UTC `checkedAt`、原始
清单 SHA-256（`manifestSha256`）、原因码和已验证对象/字节/migration 数。
未成功报告的计数仅表示已验证前缀，不表示全备份总量或完整性保证。报告与文件集应保留
在受控证据目录。报告不回显路径、对象 key、writer ID、原始 parser/IO 错误、源坐标
或内容。使用非秘密 ID；即使不透明 ID 和规模也可能属于私有证据。

## Manifest 版本 1 与 2

JSON 字段名限 ASCII，值保留合法 Unicode。重复字段（包括大小写别名碰撞）、v1 未知字段、非法
UTF-8、孤立 UTF-16 surrogate 转义、尾随 JSON、小数/越界整数和根 `null` 均拒绝。
清单上限 8 MiB。缺失数量仍是 unknown，只有显式零才表示空对象集或零字节对象。
单一 ASCII 大小写字段变体保留既有解码器行为；两个名字映射同一字段则拒绝。
先读取整数版本 envelope，再应用 v1 body 规则，所以未知版本带未来字段/body 类型仍为
unknown；通用 JSON 语法/歧义及大小上限仍适用。显式负 v1 对象总量为 invalid，不当作缺失证据。

| 字段 | 必须满足的契约 |
| --- | --- |
| `schemaVersion`、`backupId`、`state` | 版本 1 或 2、不透明身份、`complete` 声明；不支持版本或未完成状态返回 unknown。 |
| `startedAt`、`completedAt` | 非零 RFC3339 时间；完成严格晚于开始且不晚于验证，记录为 UTC。 |
| `gateway` | `version`、40 位小写 hex `revision`、完整 `imageDigest: sha256:<64 位小写 hex>`；标识声明的软件，不证明兼容。 |
| `database` | `format: pg-custom-v1` 和指向 dump 字节的 `file`；不连接、解析 dump 或恢复。 |
| `schema` | `format: gateway-migrations-tsv-v1` 和指向下述原生 ledger 表示的 `file`。 |
| `objects` | `format: s3-bytes-v1`、`inventory` 文件引用及与流式库存匹配的非负 int64 `count`/`bytes`；RustFS 物理 tar 属于不同 profile，返回 unknown。 |
| `writers` | `scopeId`、显式 `inventoryDeclaredComplete` 和非空 `writers` 声明。 |

`backupId`、软件 `version`、scope/writer ID 为 1–128 个 ASCII 字母/数字或
`.`、`_`、`:`、`+`、`-`，首字符为字母/数字。每个文件引用包含 `path`、显式非负
整数 `size`、完整 `sha256: sha256:<64 位小写 hex>`。验证所有字节，包括空文件；
大小、multipart ETag 和对象数不足以证明摘要。

Migration ledger 是 UTF-8 TSV，精确表头为 `filename\tsha256`。后续各行是原生六位
数字前缀的 migration 文件名及 64 位小写 hex checksum，中间一个 tab；名字唯一且
严格排序，至少有一行 migration。以下仅为合成数据：

```text
filename	sha256
000000_synthetic.sql	cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
```

对象库存是 JSONL，每行一个对象，按唯一 `key` 严格排序。key 非空、至多 4096 个
UTF-8 字节，不含 NUL/CR/LF；每个 `file.path` 以 `objects/` 开头。下例摘要对应
合成字节 `abc`：

```json
{"key":"synthetic/a","file":{"path":"objects/one.bin","size":3,"sha256":"sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}}
```

Ledger 和库存文件各限 64 MiB，行长小于 64 KiB。对象文件使用固定读取缓冲。
在本地读取之间检查取消；不保证能中断阻塞文件系统或恶意并发替换文件。

## Writer 声明与文件系统边界

各 writer 包含 `id`、`stopConfirmed`、`stoppedAt`、`confirmedThrough`，ID 唯一。
已确认 writer 必须不晚于备份开始停写，声明持续停写覆盖至备份结束，并且不晚于本次
验证。缺失 scope/全集确认、无 writer 或某 writer 未确认，均为 `writerEvidence:
unknown`；身份重复或已确认区间矛盾则 invalid。

这些只是操作者断言，没有认证过的进程检查、分布式 fencing token 或外部 writer
发现。后续生产者必须记录 API、Worker 和其他所有真实写入方，包括不属于某个
Compose 项目的 writer。

清单父目录就是文件集根。在支持的 POSIX 基线上，根和文件不可给予 group/other
权限，通常为 0700/0600；命令不执行 chmod。清单及文件引用必须为普通文件，引用为
至多 4096 字节的规范相对路径，不含绝对路径、`..`、反斜杠、冒号、NUL/CR/LF。
`os.Root` 经嵌套目录也限制路径逃逸，最终文件 symlink 拒绝。这不能证明所有权、停止
并发修改或把挂载/恶意文件系统变成可信快照。

既有物理演练及其破坏性的隔离恢复 helper 不变。本验证器不将其 `SHA256SUMS` 当作
可移植 S3 manifest，不解压归档或修改数据库/对象。

`make backup-transfer-test` 通过公开 export/restore 入口执行真实 binary 与 digest-pinned OCI
**软件** profile，使用合成 PG/S3 和 Raw Group/grant 读回。固定镜像身份与剩余验收
边界见[恢复 runbook](recovery-runbook.zh-CN.md)。这不增加离线验证器的恢复声明，
也不表示 OCI 制品协议覆盖。

## v2 软件身份与真实传输入口

v1 保留仅镜像 imageDigest，不接受 artifact。v2 使用 gateway.artifact：kind 为
binary 或 oci-image，sha256 为完整摘要，platform 为 linux/amd64 或 linux/arm64；
同时提供 imageDigest 与 artifact 明确拒绝。v2 还支持可选 metadata 私有文件引用，
保存生产方的启动前表指纹。离线验证只检查格式与字节；真实软件/build/OCI 标签、完整
migration 兼容、停写观察和隔离恢复由 [恢复 runbook](recovery-runbook.zh-CN.md) 的
gateway backup export/restore 分别处理。preflight 整体一致性仍为 unknown，退出 3，
不能从其结果推断真实导出/恢复已验收。
