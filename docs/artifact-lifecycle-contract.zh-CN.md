# 制品生命周期契约

[English](artifact-lifecycle-contract.md) | [文档索引](README.zh-CN.md)

## 状态

每个 Artifact 处于以下三种生命周期状态之一：

| 状态 | 读取可见性 | 允许的转换 |
| --- | --- | --- |
| `staged` | 不可解析 | `visible`、由收集器回收 |
| `visible` | 可通过原生协议解析 | `tombstoned` |
| `tombstoned` | 不可解析 | 在回收前通过管理接口恢复为 `visible`，或由收集器回收 |

发布是唯一的 `staged -> visible` 转换。它在同一元数据事务中校验字节并写入格式坐标或引用。可变协议引用只能改指向另一个不可变且可见的 Artifact。

逻辑删除会移除可见坐标或引用并记录原身份，但不会同步删除对象字节。管理恢复是唯一的 `tombstoned -> visible` 转换，并且仅在所需对象仍可恢复时成功。收集器回收无引用对象后，该转换永久不可用。

Go Module 被逻辑删除后，其字节在默认 24 小时恢复窗口内继续占用 Repository 物理容量；延迟回收成功后才释放容量。

隔离不是第四种生命周期状态，而是基于不可变 Repository、格式、坐标、摘要身份的版本化本地治理记录。隔离对象仍可见，也可正常逻辑删除或回收；恢复后隔离仍然有效。

隔离始终阻止晋级与复制。Repository 的独立隔离读取策略关闭时，协议读取保持兼容；开启后，`GET`/`HEAD` 被拒绝且协议元数据隐藏该分发，但生命周期状态不变。解除隔离会恢复读取。

Conan 的隔离身份为 recipe revision，因为 recipe 与其可见 package revision 会原子晋级和复制；package revision 仍拥有独立的生命周期和扫描身份。

## Go Module 范围

一个 Go Hosted 模块版本是不可变生命周期单元，必须同时包含一个 `.info`、`.mod` 和 `.zip`。保留策略按规范化模块路径分组，应用版本数量与年龄规则，并写入与显式删除相同的 `module@version` 墓碑。

协议读取、Group 聚合、搜索和扫描身份查询会立即隐藏整个版本。晋级和复制保持这一完整发布单元。管理请求使用规范化 `module@version` 与 ZIP 摘要；准入会解析并校验三个不可变摘要。

晋级在另一个 Hosted Repository 中复用已验证的源对象键。复制对每种表示保存检查点，复制到目标专用键，并在三个对象及最终源快照均验证后发布目标元数据。两者都会协调源与目标分发身份、拒绝 Proxy 目标，并在原子发布前再次检查隔离状态。

调度器仅在默认 24 小时恢复窗口结束后才为 Go 墓碑创建持久回收任务。任务按内容对象键协调，校验当前墓碑代数，并在访问 S3 前持久化 `collecting` 围栏。

只有不存在共享该对象的可见 Go 版本时才删除字节，随后标记过期引用已回收并释放容量。共享可见引用会保留物理对象，但不会延长过期引用的恢复窗口。

逻辑删除和恢复按稳定顺序锁定三个对象键；任何表示处于 collecting 或 collected 时，恢复以 `ErrDisabled` 失败关闭。发布孤儿清理使用独立回收意图，不能绕过恢复窗口。

## 共享持久化

迁移 `000032_artifact_lifecycle.sql` 添加两个增量记录：

- `artifact_tombstones` 保存 Repository、格式、坐标、摘要与逻辑删除时间；Repository/格式/坐标唯一键保证幂等。
- `lifecycle_jobs` 是保留、晋级、复制和物理回收的持久幂等工作边界，支持语义 JSON 幂等、原子 claim 与终态完成/失败。

调度器和 Worker 通过 PostgreSQL 租约领取任务；格式与任务类型过滤允许建立边界清晰的独立 Worker 池。

## OCI 范围

`DELETE /v2/{repository}/{name}/manifests/{digest}` 保留现有 Registry V2 响应和读取语义。在同一 PostgreSQL 事务内，它会移除 tag 引用、写入 OCI Artifact Tombstone、释放对象意图供延迟回收，并删除 manifest 元数据。

之后通过 digest 或 tag 读取仍返回 `404`；墓碑属于管理和生命周期状态，不属于协议响应。本实现为 Raw、Maven 和 Conan 建立删除模式，但此范围尚不提供墓碑浏览、保留任务执行、晋级或复制 API。
