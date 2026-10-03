# 运维信号来源与容量边界

[English](operational-signals.md) · [文档索引](README.zh-CN.md)

容量证据需要来源、单位、观察时间与适用范围。Repository 用量、Gateway 进程可见的
文件系统与对象存储/NAS 物理池是不同资源，不能相互替代空闲容量。

## 已有信号

| 信号 | 来源与单位 | 时间、重启与解释 | 权限与范围 |
| --- | --- | --- | --- |
| Repository 用量与配额 | 管理容量 API；按格式读取 PostgreSQL 元数据；字节与对象数 | 查询时计算。`quotaBytes = 0` 表示不启用逻辑配额。引用与格式记账决定 `usedBytes`，它不是物理对象存储清单。 | 既有 Repository 管理权限；Group 是视图，不拥有额外物理池。 |
| HTTP 数量与延迟 | `/metrics`；固定请求类与状态类；请求数与秒 | 进程 counter 重启归零。采集有自身时间戳，counter 本身不定义错误率窗口或最小样本策略。没有请求不能证明依赖健康。 | 既有 metrics 暴露边界。仅 Worker/Scheduler 的进程不提供包协议或管理 API。 |
| 运行时、数据库与队列 | `/metrics` 与管理员诊断；Go 内存字节、数据库连接池统计与队列数量 | 进程 gauge/counter 或数据库观察，不能解释成文件系统字节或任务完成证据。 | 既有端点权限；部署访问设置的变更是独立操作。 |
| 运行节点 | 管理员节点清单与诊断；实例、启动 session、角色与心跳 | 正常停止标记对应 session 离线；心跳年龄区分 stale/offline，旧 session 不是当前进程。 | 清单报告已观察的角色。没有外部预期实例计划，不能证明所有计划部署的实例都存在。 |
| 离线容量预检 | 显式版本化计划与清单证据；逻辑及物理字节预算 | 报告评估输入证据，不是持续物理容量采样。 | 本地操作员流程，参见[迁移容量预检](migration-capacity-preflight.zh-CN.md)。 |

来源实现包括 [Repository 容量](../internal/repository/postgres_capacity.go)、
[HTTP/运行时指标](../internal/app/request_metrics.go)、
[队列与计数指标](../internal/app/repository_metrics.go)、
[诊断](../internal/app/diagnostics_api.go)与
[容量预检](../internal/preflight/capacity.go)。不能用有限运行日志推算请求总数，不能将
Repository 逻辑用量求和当作后端物理池，也不能把 pending/中断操作视为备份或迁移成功；
这些操作需要受支持的结果契约与新鲜度检查。

## 可选本地容量诊断

可选 `GATEWAY_LOCAL_CAPACITY_MOUNTS` 接受 JSON 对象，只允许 `temporary`、`logs`、
`backups` 三个别名，每项为当前进程 namespace 中显式指定的绝对目录。以下为合成路径示例：

```sh
GATEWAY_LOCAL_CAPACITY_MOUNTS='{"temporary":"/explicit/temp","logs":"/explicit/logs","backups":"/explicit/backup"}'
```

默认空配置，不推断系统临时、日志或备份目录。启动仅验证语法，不验证目录存在或容量。
输入最多 16 KiB，每个路径最多 4096 字节，最多三个别名。重复/未知键、非字符串或空
路径、相对/未规范化路径、NUL 与尾随 JSON 均以固定错误拒绝，错误不回显输入。
目录不可用不会阻止启动，对应观察为 unknown。

每个进程生命周期只持有一个 observer。API 与 standalone 进程只通过既有管理员
`GET /api/v2/diagnostics` 暴露快照，保留强制改密门禁；仅 Worker/Scheduler 的进程不
增加管理端点。`localCapacity` 是兼容滚动升级的可选新增字段，Console 将旧节点缺失
此字段显示为未知。诊断使用 `Cache-Control: no-store`。不增加容量 metrics、集群轮询、
部署挂载、凭据、配额或告警。

原生 provider 打开指定目录且不跟随最后一段符号链接，然后在该目录句柄上查询文件系统
元数据。Linux/Darwin 均要求目录读取权限，不提权、不改变权限。父路径中的符号链接
遵循正常 OS 解析，管理员须选择可信路径。不读目录项或文件内容。Linux 支持 ext 系列、XFS、Btrfs、tmpfs 与 overlayfs；
Darwin 支持本地 APFS 与 HFS。已知网络文件系统返回 `unknown`，不支持的类型/平台与
读取失败也返回 `unknown`。受支持的可见文件系统仍可能由 VM、overlay 或共享存储池
提供，支持采样不能证明后端位于本地。

有效测量采用内核总块数与非特权进程可用块数，按原生分配单位换算成字节。Linux 优先
采用 `f_frsize`，缺失时采用 `f_bsize`；Darwin 采用 `f_bsize`。可用字节为零是合法的
满盘结果。身份缺失、无效几何或字节数超出有符号 64 位范围均为 unknown，不返回容量
数值。内核接口见 [statfs(2)](https://man7.org/linux/man-pages/man2/statfs.2.html)。

| observer 状态 | 含义 | 容量字段 |
| --- | --- | --- |
| `available` | 本 observer 挂载 namespace 中不超过一分钟的有效样本 | 总字节、可用字节与实际采样时间 |
| `not_configured` | 此别名未指定路径 | 缺失 |
| `unknown` | 固定原因，如远端/不支持文件系统、读取失败、测量无效、溢出、超时或取消 | 缺失 |
| `stale` | 存在过期成功样本，但当前刷新不能完成 | 仅保留旧采样时间，字节缺失 |

Console 显示固定别名、进程挂载视角、来源/单位、快照检查时间、采样时间、状态与固定原因。
未配置/失败容量显示未知且字节缺失；过期样本仅显示旧时间。有效满盘样本才能显示可用
零字节。普通刷新失败保留旧快照并提示，授权失效则清除数据。

快照检查时间不会刷新底层采样时间。缓存结果最多复用十五秒，之后尝试刷新。一份快照的
总等待预算至多一秒，与配置别名数量无关。阻塞的系统调用可能仍在途：observer 每个
别名只允许一个查询，每个进程总计最多三个，并发请求共享查询。取消不为阻塞任务另建替代查询。
一个别名失败不会抹掉另一别名的有效结果。

同一目录句柄的私有内核身份可以正向关联同一可见文件系统上的别名。`SharedWith` 只
包含固定别名；观察结果不包含路径、设备号、文件系统 ID 或原始错误。关联只适用于
当前 observer 进程的挂载 namespace。不同或未知身份不能证明物理池彼此独立，也没有
可求和的池总量。

例如，同一文件系统中的临时目录与日志目录共享所报告的空闲容量。容器 overlay 的容量
是该容器可见的视角，不能证明宿主磁盘、S3 桶或 NAS 池的空闲容量。远端后端物理容量
在选定受支持来源及其部署边界之前仍为 unknown；此 observer 不引入凭据或远端 adapter。
不承诺取消内核 syscall 或在关闭时等待其完成，进程退出释放其 OS 资源。

## 验证边界

observer 测试通过合成 provider 覆盖共享身份、满盘、远端/不支持来源、无效单位、溢出、
脱敏、并发阻塞查询、取消、刷新失败与过期样本。原生测试只查询测试自有临时目录，拒绝
不可读目录、文件/最后一段符号链接，并验证测试自有 sentinel 的字节、权限、大小与修改时间不变：

```sh
go test -race -count=1 ./internal/localcapacity
```

HTTP 测试覆盖管理员/改密门禁、未知字节缺失、重复超时资源上限与生成 OpenAPI 响应。
Console 用合成响应覆盖桌面/手机几何、本地化、unknown/stale、重复刷新、刷新失败及
授权失效。Linux 原生证据仅使用自有可重建挂载；超时由合成阻塞 provider 触发，不是
实际卡住的内核 syscall。

这些测试不验证生产挂载、NAS/S3 物理容量、配额执行、
错误率策略、预期部署实例或备份/迁移完成。这些仍是 #210 的独立验收。本切片不改变
告警、配额、部署或通知配置。#210 的其余信号保持开放，本切片只关闭其显式本地诊断子 Issue。
