# Artifact Gateway 领域词汇

[English](CONTEXT.md) | [文档索引](docs/README.zh-CN.md)

Artifact Gateway 是多格式制品库，通过原生包协议存储、治理和分发软件制品。本页定义代码、API、Console 和文档共同使用的术语。

## Repository 模型

**Repository（仓库）**：持久、格式专用的命名空间，拥有制品策略、授权，以及 Hosted 字节或一个已配置上游。避免称为 Registry、bucket、remote。

**Hosted Repository**：可见制品与元数据由 Artifact Gateway 持有的 Repository。避免称为本地缓存或 Proxy。

**Proxy Repository**：从一个已配置且在 allowlist 中的上游解析制品，并可保留 read-through 缓存。避免称为 Mirror。

**Group**：Hosted 与 Proxy Repository 的有序、格式专用视图，不拥有制品字节。避免称为 Repository Group 或 virtual repository。

**Artifact（制品）**：Repository 中客户端可见的版本、manifest 或路径解析，规范身份由格式决定。制品字节不可变；OCI tag、Raw path 等协议引用可以指向新的不可变身份。避免笼统称为 File 或 package。

**Artifact Identity**：一个可见、不可变且本地可解析 Artifact 的协议规范坐标与 SHA-256 摘要对。管理客户端按操作目的向 Repository 获取身份，不从浏览投影重建坐标。只有上游元数据但未缓存字节的 Proxy 结果不具备扫描或分发身份。

**Asset（资产）**：属于 Artifact 的一个不可变字节对象，例如 Maven JAR、OCI blob 或 Conan package file。

**Browse Node（浏览节点）**：Repository 目录树使用的只读、格式感知导航投影。节点可以表示合成目录、命名空间、组件、版本或 Asset，但它不是 Artifact Identity，也不代表对象存储中的物理目录。节点 ID 与分页游标由服务端签发且对客户端不透明。避免称为目录 owner、对象存储目录或客户端拼装坐标。

**Raw Path Reference（Raw 路径引用）**：Repository 本地、可变的规范路径，原子指向一个已验证的不可变内容对象。标准 PUT 会替换当前映射；路径与 SHA-256 摘要对才是治理、晋级和复制使用的不可变身份。不要称为不可变 Raw 坐标或对象存储 key。

**Service Account**：CI 或外部应用使用的稳定非人类授权 principal。Grant 绑定 `service-account:<id>`，凭证轮换时不变；Service Account 没有全局角色。不要把它与 API Key、机器人用户或凭证混称。

**Service Account Credential**：可撤销、会过期并认证为父 Service Account 的 secret。轮换期间可重叠多份；明文只在创建时返回且从不持久化。

**Publication（发布）**：把已验证 staged Artifact 原子变为读者可见的转换。避免简化为 upload 或 commit。

**APT Publication Session**：为一个 `.deb`、一个 suite 和一个 component 预留配额且幂等的可见前流程。身份来自上传包 control file；staged session 不是 APT 客户端读取面。

**APT Repository Snapshot**：拥有生成索引、Release 元数据、签名、包路径及唯一可见性开关的不可变 suite 视图。只有 `visible` 快照参与读取，`building` 和 `failed` 均不可见。

**Tombstone（墓碑）**：让曾经可见的 Artifact 不可解析，同时允许延迟回收字节的持久记录。不是 hard delete。

**Quarantine（隔离）**：绑定 Repository 本地不可变 Artifact Identity 的版本化治理判定。它阻止晋级和复制，但不改变生命周期；默认也不改变协议读取。Release 只解除分发阻止，不执行恢复或重新发布。

Conan 以 recipe revision 及其完整可见 package closure 作为分发身份；package revision 仍有独立扫描和生命周期身份，但不能单独隔离。

## 分发与运维

**Promotion（晋级）**：在不改变源 Artifact 的前提下，经过策略控制并可审计地在另一个 Hosted Repository 创建可见 Artifact。不是 move 或 overwrite。

**Promotion Request**：快照一个可见源 Artifact 并指定目标 Hosted Repository 的幂等管理指令，会成为持久生命周期任务，本身不是晋级后的 Artifact。

**Promotion Snapshot**：请求记录的不可变源身份，包括 Repository、格式、坐标和摘要。Worker 创建目标前复查其仍可见且未隔离。

**Replication（复制）**：向配置目标异步、带检查点地复制可见元数据和字节。持久计划保留源坐标与摘要，以便发布前复查隔离。聚合 PyPI 版本的文件成员变化会将计划停在 `replication_snapshot_changed`，精确幂等重放会在发布完整版本前刷新检查点。

**Operational Event（运维事件）**：与治理状态转换在同一事务产生的不可变事实。首批事件记录隔离与释放，不能从 Audit 记录重建。

**Webhook Subscription**：管理员管理的 HTTPS 目标、加密 HMAC secret、启用标记和事件过滤。禁用只停止新投递，不清除历史。

**Webhook Delivery**：一个事件和一个订阅之间持久的至少一次投递状态，包含租约、有界重试、终态 `dead` 和保留事件身份的显式重放。

**Quarantine Read Policy**：版本化 Hosted Repository 策略，决定隔离 Artifact 的协议读取是保持兼容还是失败关闭。默认关闭且独立于晋级准入；Group 不得绕过更高优先级的隔离身份。

**Retention Policy（保留策略）**：决定可见 Artifact 何时变为 tombstoned 的版本化规则，不等于垃圾回收。

**Orphan Collector（孤儿收集器）**：延迟维护流程，仅回收没有可见 Artifact、活跃发布或复制租约引用的字节，不等于保留任务。
