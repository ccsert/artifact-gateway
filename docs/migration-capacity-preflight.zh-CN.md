# 迁移容量预检

[English](migration-capacity-preflight.md) | [文档索引](README.zh-CN.md)

Gateway 二进制提供离线预算命令，处理一份固定、已规范化的迁移库存。本新增能力属于
`Unreleased`，已经发布的 v0.5.0 二进制不包含它。入口复用现有运维 preflight，独立于
计划中的 `agctl` CLI。

```sh
gateway preflight capacity --input plan.json --format json
```

命令只读一个普通 JSON 文件（最多 8 MiB），向 stdout 输出 JSON 报告后退出。不加载
Gateway 运行配置，不访问源端或目标，不启动服务、预留容量、设置配额、上传制品、删除
数据或应用保留规则。证据获取和验证由操作者单独执行；本批不新增管理 HTTP 路由。

## 结果与证据

| 退出码 | `status` | 含义 |
| --- | --- | --- |
| 0 | `sufficient` | 所有相关量已知，且在本次证据快照中满足预算。 |
| 1 | `insufficient` | 至少一个已知的独立约束已超限。 |
| 2 | 不输出报告 | 参数错误、文件不可读/超限、JSON 无效或有歧义、未知字段，或报告输出失败。 |
| 3 | `unknown` | 证据不完整、不一致、过期、版本不支持或已取消。 |

已确定的独立超限优先于其他维度的 unknown。未知数字序列化为 `null`，不会变成零。
所有数值都是非负、有符号 64 位的**整数字节**，不是带小数的 GiB；JSON 小数、超出
int64 范围的数字、重复 JSON 字段（含大小写别名）和尾随 JSON 值均拒绝。字段名为
ASCII，字段值可使用 UTF-8。负值或加法溢出使相关完整总量 unknown。

`knownMinimumProjectedBytes` 和 `knownMinimumFreeBytes` 保留已证明的非负项下界。
它们不替代缺失总量：完整总量仍是 `null`，下界满足预算也不能判 sufficient；但下界已
超已知配额或可用字节时仍判 insufficient。下界本身若超 int64，数值为 `null`，但已
确定超过所有可表示的有限上限。已验证且已计量的逻辑引用字节之和不能大于快照
`usedBytes`，矛盾的仓库证据返回 unknown。

报告包含 `schemaVersion: 1`、UTC `checkedAt`、`inventoryId`、原始输入文件的完整
SHA-256（`inputSha256`）、按仓库 ID 排序的逻辑结果，以及单独列出峰值组成的存储
结果。不回显对象 key、内容摘要、文件路径、解析错误片段或依赖凭据。输入文件、其校验
和及报告保存在受控迁移证据目录；使用不含秘密的 opaque ID。`inventoryId`、仓库、
目标和 pool ID 是 1–128 个 ASCII 字母/数字或 `. _ : -`，首字符为字母/数字；逻辑/
对象 key 非空、最多 4096 字节，不能含 NUL、CR、LF。内容摘要使用完整
`sha256:<64 位十六进制>`。

`snapshot.targetId` 必须与计划 `targetId` 相等。所有观察必须来自该目标、该固定库存
及一个物理 pool，并在操作者声明的证据区间内采集。`observedAt` 非零且不晚于检查
时刻；`validUntil` 晚于检查及观察。证据有效窗口由操作者明确给出，工具不自定期限，也
不会认证输入观察的真实性。

`sufficient` 表示快照预算通过，不保证未来一定有空间，也不是容量预留。并发写入、
源端漂移或配额变化可立即使它失效；首个导入写入前及目标变化后重新采集证据，实际写入
仍由既有运行时配额门禁决定。

## 第 1 版规范化输入

可从[合成示例](examples/migration-capacity-plan.json)开始。示例时间固定且会过期；
实际批次需更新为真实观察时间并替换证据。未获取的数据保留缺省/`null`/`unknown`，
不能补成零或假定为 `absent`。

| 字段 | 契约 |
| --- | --- |
| `schemaVersion` | 只支持 1，其他版本返回 unknown。 |
| `inventoryId`, `complete` | 固定批次身份及显式完整枚举；未完成或未命名库存返回 unknown。工具不枚举 Nexus，也不推测未列出的对象。 |
| `references` | 每个计划逻辑引用给出 `repositoryId`、`logicalKey`、目标 `objectKey`、完整预期 `digest` 和 `size`。 |
| `snapshot.repositories` | 目标容量 API 的 `repositoryId`、`usedBytes`、`quotaBytes`。`quotaBytes: 0` 按既有 API 表示不限制；缺省配额表示 unknown。 |
| `snapshot.objects` | 按目标物理 `key` 的观察。`absent` 必须确认不存在；`verified` 需匹配完整字节摘要及大小；未知/缺省不能扣减物理增长。 |
| `snapshot.references` | 按 `repositoryId` 和逻辑 `key` 的仓库成员观察，状态同上；物理存在不能证明该仓库已有引用。 |
| `snapshot.freeBytes` | 同一个物理 pool 已知可用字节；不是逻辑仓库容量或 S3 bucket 用量。 |
| `peak` | 匹配的 `storagePoolId` 及显式 `downloadBytes`、`uploadBytes`、`backupBytes`、`restoreBytes`、`headroomBytes`；不需要的项可显式为零，省略表示 unknown。 |

容量 GET 沿用该 API 已要求的仓库读取权限。例如，只保存计划需要的字段：

```sh
curl --fail --silent --show-error \
  -H "Authorization: Bearer $GATEWAY_READ_TOKEN" \
  "$GATEWAY_URL/api/v2/repositories/$REPOSITORY_ID/capacity" \
  | jq '{repositoryId, usedBytes, quotaBytes}'
```

自行记录采集时间及目标身份；权限拒绝、GET 失败或物理空间无法获得均是缺失证据，不能
按零计算。离线命令自己不发认证请求。`verified` 表示之前已完成全字节 SHA-256 验证，
不能只凭 HEAD、大小、HTTP 2xx、S3 multipart ETag，或从库存复制摘要后手工打标。

## 逻辑增量与物理峰值

逻辑增量按 `(repositoryId, logicalKey)` 去重，并沿用格式现有容量契约。多个 OCI tag
指向的同一个 blob，在同仓库只计一次；新仓库引用相同 blob 仍占其逻辑配额。已有且已
验证的仓库引用逻辑增量为零。Proxy 缓存、别名及其他格式必须有正确的规范化映射；映射
不明确时不能据此声明库存完整。

物理增量按**该 pool 内计划目标对象 key** 去重；只有大小、摘要相同且已验证存在的
物理对象才扣减为零。同摘要的不同物理 key 分别计量：OCI blob key 可共享，原生 OCI
manifest key 含仓库和镜像名，Maven 发布 key 不能假设全局按摘要去重。重复逻辑或物理
身份的内容冲突返回 unknown；相关身份的重复快照观察也有歧义，返回 unknown。

存储需求为：

```text
唯一新增物理字节
  + 同时下载/暂存字节 + 同时上传字节
  + 额外备份副本字节 + 隔离恢复副本字节 + 余量字节
```

暂存预算需包含选择的并行度。既有已用空间已经从可用 `freeBytes` 中扣除，不再次相加。
各组成保守视为在同一个 pool 同时存在；对象存储、spool 和备份若在不同文件系统，只能
使用可证明的共同 pool 或报告 unknown。第 1 版不建模多个 pool，也不发现 S3 的物理
剩余空间。缺失/未验证对象的字节不能按零处理。

本切片仅接收规范化证据。源库存采集、导入、实时目标重查、容量预留、完整 agctl、配额
修改及清理属于独立后续工作；不把 `objectCount` 改成组件数，也不改容量与授权契约。
