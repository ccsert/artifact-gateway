# 仓库删除契约

[English](repository-deletion-contract.md) · [文档索引](README.zh-CN.md)

仓库删除是异步管理操作。

1. `DELETE /api/v2/repositories/{repositoryId}` 将 `active` 仓库改为 `deleting`，并返回
   `202` 和仓库表示。
2. 状态变化立即阻止该仓库的协议读写、Proxy 访问、Group 解析、搜索和晋升。仓库处于
   `deleting` 时重复删除是幂等操作。
3. `RepositoryDeletionWorker` 在进程启动时扫描一次，之后每分钟扫描。每个 `deleting`
   仓库通过受保护的幂等转换进入 `deleted`。扫描失败会让仓库停留在 `deleting`，以便
   下次重试。
4. `deleted` 是终态。仓库元数据行作为审计记录和外键锚点保留，但不再是可用仓库。

该 Worker 有意与各格式的对象回收器分离。制品字节采用内容寻址存储，可能被多个仓库
共享；物理回收必须继续经过格式专属引用检查和生命周期任务，不能在仓库状态转换时
直接删除共享对象。
