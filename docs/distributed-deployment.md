# 分布式部署

Artifact Gateway 使用 PostgreSQL 保存元数据和后台任务，使用共享的 S3/MinIO
保存 Asset 字节。Gateway 进程本身不保存必须由其他实例读取的制品状态，因此可以
横向扩展。

## 节点角色

同一个镜像通过 `GATEWAY_NODE_ROLES` 选择运行职责：

| 角色 | 职责 | 对外路由 |
| --- | --- | --- |
| `api` | 协议处理、管理 API、公开搜索 | 完整 Gateway API |
| `scheduler` | 周期扫描、创建 retention/reclaim/cache 任务 | 仅运维端点 |
| `worker` | 领取并执行持久化任务、复制和晋升 | 仅运维端点 |
| `standalone` | 同时启用以上三个角色 | 完整 Gateway API |

未设置 `GATEWAY_NODE_ROLES` 时等同于 `standalone`。`standalone` 不能和其他角色
组合，避免配置含义不明确。

Worker 可以继续按格式和任务类型缩小职责：

```env
GATEWAY_NODE_ROLES=worker
GATEWAY_WORKER_FORMATS=oci
GATEWAY_WORKER_KINDS=reclaim,replication
```

格式支持 `maven`、`oci`、`raw`、`conan`；任务类型支持 `promotion`、
`replication`、`retention`、`reclaim`、`deletion`、`recovery`、`cache`、`audit`。
未设置过滤器时，worker 处理全部格式和任务类型。

格式和任务过滤器只限制 Worker，Scheduler 始终为全部格式发现工作。缓存回收任务
按 OCI、Raw、Conan 分开领取；Maven 缓存条目在读取时执行过期判断，因此没有
独立的 Maven 缓存回收任务。

## 部署约束

1. 所有节点必须使用同一个 PostgreSQL 数据库和同一个 S3/MinIO bucket。
2. 迁移必须由独立的 migration job 在启动副本前完成，Gateway 进程不负责竞争执行迁移。
3. 每个实例设置唯一的 `GATEWAY_INSTANCE_ID`，用于日志和故障定位。
4. 多副本部署时按副本数降低 `GATEWAY_DATABASE_MAX_OPEN_CONNS`，并为 coordinator
   和 notification pool 预留连接。
5. `LISTEN/NOTIFY` 只是唤醒提示，任务表和租约才是事实来源；通知丢失后 worker
   仍会通过定时轮询恢复任务。

## 推荐拓扑

```text
               +-------------------+
clients ------>| api x 2            |
               +---------+---------+
                         |
                 PostgreSQL + S3
                         |
       +-----------------+------------------+
       |                                    |
  scheduler x 1                     workers x N
  all formats                       format-specific
```

API 节点可以使用负载均衡器扩容。Scheduler 一般部署一个副本即可；如果部署多个
副本，扫描写入必须依赖任务幂等键。Worker 可以按格式独立扩容，任务领取通过
`FOR UPDATE SKIP LOCKED`、advisory lock 和 lease token 防止重复提交。

每个进程都会将实例 ID、角色、Worker 格式/任务过滤器和最近心跳写入
`runtime_nodes`。管理员可通过管理 API 的 `GET /api/v2/runtime/nodes` 查看节点状态；
30 秒未更新标记为 `stale`，超过 2 分钟标记为 `offline`。该清单用于运维可见性，
不参与任务领取决策，任务表中的租约仍是唯一事实来源。

## 单机回退

本地开发和小规模安装继续使用：

```env
GATEWAY_NODE_ROLES=standalone
```

该模式不需要额外队列或服务发现组件，且保留原有内存和部署开销。
