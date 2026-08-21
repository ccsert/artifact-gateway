# 分布式部署

[English](distributed-deployment.md) · [文档索引](README.zh-CN.md)

Artifact Gateway 使用 PostgreSQL 保存元数据和后台任务，使用共享的 S3 兼容对象存储
保存 Asset 字节。Gateway 进程本身不保存必须由其他实例读取的制品状态，因此可以
横向扩展。

## 节点角色

同一个镜像通过 `GATEWAY_NODE_ROLES` 选择运行职责：

| 角色 | 职责 | 对外路由 |
| --- | --- | --- |
| `api` | 协议处理、管理 API、公开搜索 | 完整 Gateway API |
| `scheduler` | 周期扫描、抢占管理员计划并创建 retention/reclaim/cache/audit 任务 | 仅运维端点 |
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

`GATEWAY_WORKER_FORMATS` 支持 `maven`、`oci`、`raw`、`conan`、`npm`、`pypi`、`apt`；
其中 `apt` 当前只执行管理预览上传的 `reclaim`，不代表 Hosted 协议已可用。任务类型支持
`promotion`、`replication`、`retention`、`reclaim`、`intelligence`、`deletion`、
`scan`、`recovery`、`cache`、`audit`、`webhook`。`intelligence` 负责处理晋升成功后被延迟的
制品情报复制；`scan` 只在该节点配置了 `GATEWAY_SCANNER_ENDPOINT` 时启动，其格式
由独立的 `GATEWAY_SCANNER_FORMATS` 控制（包含仅代理的 `go`），可以与普通生命周期
Worker 隔离部署。`webhook` 从 PostgreSQL durable outbox 领取全局投递，不受
`GATEWAY_WORKER_FORMATS` 限制。未设置过滤器时，worker 处理全部适用格式和任务类型。

格式和任务过滤器只限制 Worker，Scheduler 始终为全部格式发现工作。缓存回收任务
按 OCI、Raw、Conan 分开领取；Maven 缓存条目在读取时执行过期判断，因此没有
独立的 Maven 缓存回收任务。

## 部署约束

1. 所有节点必须使用同一个 PostgreSQL 数据库和同一个 S3 bucket。当前自带部署基线使用 RustFS。
2. 迁移必须由独立的 migration job 在启动副本前完成，Gateway 进程不负责竞争执行迁移。
3. 每个实例设置稳定的 `GATEWAY_INSTANCE_ID`，用于日志和故障定位；Gateway 会为每次
   进程启动生成独立会话 ID，因此重复配置实例 ID 也不会覆盖节点会话。
4. 多副本部署时按副本数分别预算 primary、cache coordinator、artifact lock 和
   notification pool。默认每个实例上限为 `32 + 8 + 4 + 2 = 46` 条连接；应按
   PostgreSQL 的 `max_connections` 降低 `GATEWAY_DATABASE_MAX_OPEN_CONNS`、
   `GATEWAY_DATABASE_COORDINATOR_MAX_OPEN_CONNS` 与
   `GATEWAY_DATABASE_ARTIFACT_LOCK_MAX_OPEN_CONNS`，并为迁移和管理连接留余量。
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
副本，`scheduled_tasks` 使用 `FOR UPDATE SKIP LOCKED` 单次抢占并提前推进
`next_run_at`，下游任务同时使用包含计划和投递 ID 的幂等键。停机恢复只补投一次，
不会按遗漏周期连续补跑。Worker 可以按格式独立扩容，任务领取通过
`FOR UPDATE SKIP LOCKED`、advisory lock 和 lease token 防止重复提交。

每个进程都会将实例 ID、启动会话、角色、Worker 格式/任务过滤器和最近心跳写入
`runtime_node_sessions`，同时维护 `runtime_nodes` 兼容投影。正常关闭会将对应会话
立即标记为 `offline`；异常退出依赖心跳超时，30 秒未更新标记为 `stale`，超过 2 分钟
标记为 `offline`。长期离线记录默认保留 7 天，可通过以下变量调整：

```env
GATEWAY_RUNTIME_NODE_RETENTION=168h
GATEWAY_RUNTIME_NODE_PRUNE_INTERVAL=1h
```

管理员通过 `GET /api/v2/runtime/nodes` 可同时查看节点清单和集群健康摘要。摘要会提示
没有在线 API、Scheduler 或 Worker、重复实例 ID，以及过期心跳。该清单和摘要用于运维
可见性，不参与任务领取决策，任务表中的租约仍是唯一事实来源。

`GET /api/v2/diagnostics` 在同一管理员边界下汇总当前构建、运行角色、依赖可用性、
节点健康和仓库后台队列。响应只包含固定状态与脱敏详情，不返回数据库/S3 地址、
环境变量、令牌或凭据，可从 Console 的“任务中心 / 系统诊断”复制为支持材料。

## 单机回退

本地开发和小规模安装继续使用：

```env
GATEWAY_NODE_ROLES=standalone
```

该模式不需要额外队列或服务发现组件，且保留原有内存和部署开销。
