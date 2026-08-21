# 架构图

[English](architecture-diagrams.md) · [整体架构](../ARCHITECTURE.md) · [PostgreSQL 能力](postgresql-capabilities.md)

视觉总览图用于快速理解组件与部署选择；下方可执行 Mermaid 图继续作为精确、可评审的
架构关系事实来源。

## 系统视觉总览

![Artifact Gateway 轻量级系统架构](assets/artifact-gateway-system-architecture.png)

## 系统与存储边界

```mermaid
flowchart LR
    subgraph clients[客户端]
        Console[Web Console]
        Native[OCI、Maven、npm、PyPI、Go、APT、Conan、Raw 客户端]
    end

    Gateway[Artifact Gateway<br/>原生协议与管理 API]
    PG[(PostgreSQL 16<br/>元数据、授权、审计、<br/>任务、租约与锁)]
    S3[(S3 兼容对象存储<br/>经过校验的不可变字节)]
    Upstream[白名单约束的上游仓库]
    Scanner[可选扫描器 / 签名适配器]

    Console --> Gateway
    Native --> Gateway
    Gateway -->|事务与协调| PG
    Gateway -->|内容寻址对象| S3
    Gateway -->|Proxy 读取| Upstream
    Gateway -->|有界适配器调用| Scanner
```

PostgreSQL 是唯一数据库与协调服务；S3 兼容对象存储是独立字节面，本地开发由 RustFS
提供。

## 紧凑单机与角色拆分

![Artifact Gateway 单机与分布式部署拓扑](assets/artifact-gateway-deployment-topology.png)

```mermaid
flowchart TB
    subgraph standalone[紧凑部署]
        One[一个 Gateway 进程]
        OneAPI[API]
        OneScheduler[Scheduler]
        OneWorker[Worker]
        One --- OneAPI
        One --- OneScheduler
        One --- OneWorker
    end

    subgraph distributed[同一镜像拆分部署]
        APIs[API 节点 × N]
        Scheduler[Scheduler 通常 × 1]
        Workers[Worker × N<br/>按格式和任务类型过滤]
    end

    PG[(共享 PostgreSQL)]
    S3[(共享 S3 兼容对象存储)]

    OneAPI --> PG
    OneScheduler --> PG
    OneWorker --> PG
    OneAPI --> S3
    OneWorker --> S3

    APIs --> PG
    Scheduler --> PG
    Workers --> PG
    APIs --> S3
    Workers --> S3
```

默认运行方式是 `standalone`。角色拆分只改变运行位置，不改变数据库、任务或对象存储
契约，也不会额外引入消息队列和服务发现。

## 发布与可见性边界

```mermaid
flowchart LR
    Upload[原生客户端上传] --> Stage[对象存储暂存字节<br/>协议不可见]
    Stage --> Verify[校验 digest、归档身份<br/>与格式契约]
    Verify --> Lock[PostgreSQL 事务内<br/>锁定坐标和容量]
    Lock --> Publish[原子提交引用与<br/>可见元数据]
    Publish --> Read[协议与 Console 读取]

    Stage -. 事务或校验失败 .-> Intent[未引用对象 intent]
    Intent --> Recheck[宽限期与引用复核]
    Recheck --> Reclaim[从对象存储回收字节]
```

制品字节可以先于发布写入对象存储，但只有完成校验并提交 PostgreSQL 事务后，元数据才
对客户端可见。事务失败不代表对象一定可以删除；回收任务会等待宽限期并重新检查持久化
引用。

## 持久化后台任务

```mermaid
sequenceDiagram
    participant A as API / Scheduler
    participant P as PostgreSQL
    participant W1 as Worker A
    participant W2 as Worker B
    participant O as 对象存储

    A->>P: INSERT job ON CONFLICT DO NOTHING
    P-->>W1: LISTEN/NOTIFY 唤醒提示
    P-->>W2: LISTEN/NOTIFY 唤醒提示
    par 并发领取
        W1->>P: FOR UPDATE SKIP LOCKED
        W2->>P: FOR UPDATE SKIP LOCKED
    end
    P-->>W1: 任务 1 + lease token
    P-->>W2: 任务 2 + lease token
    W1->>O: 校验 / 复制 / 回收
    W2->>O: 校验 / 复制 / 回收
    W1->>P: fenced checkpoint 与终态
    W2->>P: fenced checkpoint 与终态
    Note over W1,W2: 通知丢失后由轮询恢复任务
```

`LISTEN/NOTIFY` 只降低延迟，不是任务事实来源。持久化任务行、`SKIP LOCKED`、租约过期
和 fencing token 共同处理 Worker 或数据库连接消失后的恢复。
