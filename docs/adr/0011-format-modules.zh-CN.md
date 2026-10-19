# 显式格式模块与 Raw 纵向切片

[English](0011-format-modules.md) · [文档索引](../README.zh-CN.md)

状态：已接受；Raw tracer 已实现（#393），其余格式由 #281 跟踪

## 背景与备选方案

Raw 的 Hosted/Proxy 处理、Group 字节解析、扫描、保留和复制发布原来依赖
`internal/app` 包内私有符号。仅拆文件会保留这些依赖。另一种方案是让所有格式实现
包含全部操作的单一 `FormatModule`，但这会迫使未来格式实现不支持的方法，并接收
无关存储。

我们选择最小模块身份与独立查询的可选能力，按 ADR 0009 逐步迁出。历史管理接口与
URL 别名不约束设计；标准包协议、不可变身份、完整字节验证、授权与持久 fencing
继续作为约束。

## 模块与注册表

`internal/format.Module` 只提供 `Format()`。`format.New` 接收显式实例，拒绝空模块
（包括 typed nil）、空身份及重复格式，并拥有只读查询映射。没有全局可变注册或
隐式存储构造。`format.Capability[T]` 只查询指定已注册模块的能力；缺少模块或能力
时保持缺失。

`NativeRoutes` 提供协议路由。注册表在挂载前验证完整集合，包括重复路径、typed nil
handler 和无效 HTTP pattern。应用组合保留请求观测、凭据、共享授权/成员排序与
指标适配；模块拥有格式响应决策和 Hosted/Proxy/Group 执行。

`internal/format/raw` 的首个纵向切片实现以下能力：

| 能力 | 职责 |
| --- | --- |
| 原生路由 | `/raw/`、规范路径解析、Hosted PUT/可恢复完成、CAS、校验和、Proxy 缓存/回退与 Group 读取 |
| 档案与浏览 | 既有 Raw 能力档案、目录父节点校验、显示名称、规范搜索路径、有界搜索投影和用法身份 |
| 扫描 | 不可变路径/摘要校验以及只读 scanner Asset 流 |
| 保留与治理 | Raw 年龄/模式选择、墓碑和恢复；共享下载保护仍由保留协调器处理 |
| 晋升与复制 | Raw 晋升载荷、原生 checkpoint 快照与既有持久 worker 的目标发布 |
| 不可变发布 | 迁移编排使用的原生 Raw 路径校验、CAS 对象锁、暂存、仅新建可见性与回读 |
| 缓存与后台 | 既有 Raw 缓存回收及 reclaim/promotion/replication 调度器和 worker 启动 |

可选接口传递领域记录，不传生成的管理 DTO。管理 handler 在能力外保留鉴权、
响应/错误映射、签名游标、权限检查与审计。未迁出格式继续使用原有分发分支，随后
按可独立合并切片逐个迁移。Raw 在 HTTP/进程组合以及窄读取或生命周期服务边界显式
绑定，不补造缺失的 port。已接纳格式的档案描述协议能力，不代表可选 worker、缓存
或扫描器已经部署就绪。

## 窄接口与持久约束

Raw 原生读写依赖 `repository.NativeRawStore`、小型仓库/Group 查询接口、对象存储、
读取策略/隔离查询以及共享授权。扫描/发布回调、指标和诊断适配不暴露组合根。
迁移发布使用独立的四方法 Raw store port，不接收迁移台账或 SQL 实现。
`StageVerifiedPublication` 接收迁移协调器已完整核验大小与哈希的字节；对象存储
`PutVerifiedReader` 记录已验证元数据，不自行核验摘要。可见性提交和最终完整回读
仍是协调器独立执行的步骤。

Job/Replication/Publication 接口字段保留原始具体 store 作为动态值。不能从嵌入接口
重新包装 store，因为这会丢失 PostgreSQL 事务、租约/advisory lock、通知和隔离准入
接口。回收协调、发布 CAS、配额、迁移事务 fencing 与完整回读继续调用同一原生存储
操作，并传递原请求上下文。不引入 SQL 迁移或持久化 fallback。

`internal/artifacthttp` 拥有原先由 Raw 与 npm 共用的不可变对象 HTTP 响应，接收
格式无关元数据；`internal/uploadspool` 拥有尚未迁出格式也共用的有界暂存/分块组装。
Raw 不再为偶然共用 helper 依赖 npm/OCI。Raw 上游 URL 组装与传输策略也位于其模块，
传输 tracing 与测试 hook 显式注入。格式模块不 import app、其他格式或 PostgreSQL
实现，`make architecture-check` 检查该方向。

## 验证与后续

注册表通过真实路由挂载及无效注册测试验证。Raw 模块无需构造 Gateway，即可发布、
条件替换、可恢复完成、读取、扫描、规划保留、准备分发、墓碑/恢复和搜索。既有完整
Gateway 的 Raw/Proxy/Group、授权、隔离、失败诊断和发布后扫描测试仍保留。
PostgreSQL/S3 及共享 Memory/PostgreSQL 契约验证原生存储/发布边界，标准 curl 门禁
验证实际挂载路由。

本切片不完成 #281。Cargo、Go、npm、PyPI、Maven、Conan、OCI 和 APT 继续按独立格式
切片推进；静态接纳档案仍位于 `repository`，缓存控制的 SQL 投影属于 #282 持久实现
后续工作。共享成员授权、HTTP 契约，以及迁移清单/容量/租约编排仍归各自领域。
app switch 计数只是进度指标，不宣称已达到最终 ≤10 目标。本次不执行生产迁移、
切流或删除源数据。
