# 性能基线

[English](performance-baseline.md) · [项目中文 README](../README.zh-CN.md)

本文是一份可复现的工程快照，不是生产 SLA，也不代表项目已经公开发布。它只回答轻量级
核心栈的三个问题：Go 交付物有多大、静默运行保留多少内存，以及本地并发读取时资源与
吞吐如何变化。

## 结果摘要

在 2026-08-21 的提交 `7881539a` 上：

- 去符号静态 Gateway 二进制为 **Linux/arm64 28.88 MiB**、
  **Linux/amd64 31.83 MiB**；
- 包含 Gateway 和健康检查程序的 distroless 运行时镜像，未压缩内容为
  **36.06 MiB**；
- 健康且稳定后的 Gateway 静默内存平均为 **53.59 MiB**，Gateway + PostgreSQL +
  RustFS 完整核心栈平均为 **382.08 MiB**；
- 128 并发下，经鉴权并访问 PostgreSQL 的元数据读取达到
  **32,562 请求/秒、p95 9 ms**，Gateway 观测峰值为 **104.40 MiB**；
- 128 并发下，经鉴权、PostgreSQL 和 RustFS 读取 64 KiB Raw 制品达到
  **2,964 请求/秒 / 186.08 MiB/s、p95 53 ms**，Gateway 观测峰值为
  **103.50 MiB**；
- 397,000 个计量请求全部完成，ApacheBench 失败数为零；压测后 Gateway 仍通过
  liveness/readiness，日志中没有 `panic` 或 `fatal`。

这些数据支撑一个明确优势：Artifact Gateway 自身的 Go 进程足够精简，并且把
PostgreSQL 作为唯一的协调与数据库依赖。制品字节仍需要 S3 兼容的字节面，本次本地栈
使用 RustFS；Redis、Kafka、Elasticsearch 和额外消息队列都不属于核心基线。
这条边界背后的锁、队列、通知、搜索与可观测性原语详见
[PostgreSQL 能力](postgresql-capabilities.md)。

## 测试环境

| 项目 | 配置 |
| --- | --- |
| 日期 | 2026-08-21 |
| 源码 | 提交 `7881539a` |
| 宿主机 | macOS 26.5.2 (25F84)，Apple Silicon arm64，Mac15,9，128 GiB |
| Go | 1.26.6 |
| Docker | Docker Desktop 29.7.2，Linux arm64 VM |
| Docker VM 配额 | 12 vCPU，46.96 GiB |
| 容器限制 | 未显式设置 CPU 或内存限制 |
| 拓扑 | Gateway `standalone`、PostgreSQL 16 Alpine、RustFS |
| 网络路径 | 单一宿主机压测端经 loopback HTTP；无 TLS、无 Ingress |

基线只统计三个持续驻留的核心服务。采样前，一次性的数据库迁移和 RustFS 初始化容器
已经退出；可选扫描器和 APT 签名器 profile 未启用。

## 交付物体积

二进制使用生产 Dockerfile 的 `CGO_ENABLED=0`、`-trimpath` 和
`-ldflags='-s -w'`；运行时镜像使用非 root 的 distroless static 基础镜像。

| 交付物 | 架构 | 原始体积 | gzip 快照 |
| --- | --- | ---: | ---: |
| Gateway 静态二进制 | Linux/arm64 | 28.88 MiB | 9.32 MiB |
| Gateway 静态二进制 | Linux/amd64 | 31.83 MiB | 10.36 MiB |
| 健康检查静态二进制 | Linux/arm64 | 5.19 MiB | 2.10 MiB |
| 健康检查静态二进制 | Linux/amd64 | 5.57 MiB | 2.33 MiB |
| 运行时镜像（Gateway + 健康检查 + 基础层） | Linux/arm64 | 36.06 MiB | 13.50 MiB |

镜像原始体积来自 Docker 的未压缩内容大小；13.50 MiB 是本地
`docker save | gzip -1` 传输快照，不等同于镜像仓库中的精确压缩层大小。

## 静默内存占用

核心栈健康运行约两分钟后，再稳定等待 15 秒，然后每秒执行一次
`docker stats --no-stream`，共采样十次。

| 服务 | 最小值 | 平均值 | 最大值 |
| --- | ---: | ---: | ---: |
| Gateway | 53.34 MiB | 53.59 MiB | 53.93 MiB |
| PostgreSQL | 122.00 MiB | 122.47 MiB | 123.00 MiB |
| RustFS | 205.40 MiB | 206.02 MiB | 206.70 MiB |
| 核心栈平均值之和 | — | **382.08 MiB** | — |

这是本地稳定状态基线，不是最低内存保证。数据库与对象存储缓存、仓库数量、连接池、
可观测性配置和可选 Worker 都会改变稳态占用。

## 并发读取负载

测试创建了一个真实 Raw Hosted 仓库并写入随机 64 KiB 对象。ApacheBench 使用
HTTP/1.1 keep-alive 和临时管理员 Bearer token。正式计量前两个路径各预热一次：

- `metadata`：`GET /api/v2/repositories`，覆盖鉴权与 PostgreSQL 控制面；
- `raw-64k`：`GET /raw/perf-raw/performance/payload-64k.bin`，覆盖鉴权、
  PostgreSQL 元数据与 RustFS 字节面。

| 负载 | 并发 | 请求数 | 请求/秒 | p50 | p95 | p99 | 传输率 | 失败 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 元数据 | 1 | 5,000 | 3,479.12 | 0 ms | 0 ms | 1 ms | 0.97 MiB/s | 0 |
| 元数据 | 32 | 30,000 | 12,798.80 | 1 ms | 12 ms | 16 ms | 3.58 MiB/s | 0 |
| 元数据 | 128 | 300,000 | **32,562.20** | 3 ms | **9 ms** | 16 ms | 9.10 MiB/s | 0 |
| Raw 64 KiB | 1 | 2,000 | 766.33 | 1 ms | 2 ms | 2 ms | 48.10 MiB/s | 0 |
| Raw 64 KiB | 32 | 20,000 | 2,903.22 | 10 ms | 22 ms | 31 ms | 182.24 MiB/s | 0 |
| Raw 64 KiB | 128 | 40,000 | **2,964.35** | 42 ms | **53 ms** | 66 ms | **186.08 MiB/s** | 0 |

元数据结果受益于本机 loopback、小响应体和预热后的 PostgreSQL 缓存，只适合同一机器
上的横向对比，不能作为互联网入口容量承诺。

## 128 并发下的内存

两个高并发场景运行时约每秒采集一次 Docker 资源数据，因此采样间隔内的瞬时峰值可能
没有被捕捉。

| 负载 | Gateway 峰值 | PostgreSQL 峰值 | RustFS 峰值 | 各服务峰值之和 |
| --- | ---: | ---: | ---: | ---: |
| 元数据，c=128 | 104.40 MiB | 163.80 MiB | 219.00 MiB | **487.20 MiB** |
| Raw 64 KiB，c=128 | 103.50 MiB | 132.00 MiB | 231.70 MiB | **467.20 MiB** |

在 128 个并发客户端下，Gateway 观测峰值约 104 MiB，约为静默平均值的两倍。Docker
CPU 超过 100% 表示使用了多个 CPU 核心。压测结束 25 秒后缓存仍然是热的：Gateway
平均 83.24 MiB，完整核心栈平均 443.21 MiB，因此本文不宣称其立即回到冷启动静默值。

## 复现基线

前置条件为支持 Compose v2 的 Docker、Go 1.26.6、ApacheBench（`ab`）、curl、jq、
OpenSSL、Python 3、gzip 和标准 POSIX 工具。

```sh
make performance-baseline
```

命令会自动分配空闲 loopback 端口，创建隔离的 Compose project 和全新数据卷，构建镜像
与两个 Linux 架构的二进制，执行六组负载，将 CSV 证据写入最终打印的临时目录，并在
退出时删除隔离容器、数据卷和临时密钥。它不会复用或修改正常开发环境的 `.env` 与
数据卷。

如需只验证脚本或固定输出目录，可缩小请求量：

```sh
GATEWAY_PERF_OUTPUT_DIR=./performance-results \
GATEWAY_PERF_METADATA_LOW_REQUESTS=128 \
GATEWAY_PERF_METADATA_MID_REQUESTS=256 \
GATEWAY_PERF_METADATA_HIGH_REQUESTS=512 \
GATEWAY_PERF_RAW_LOW_REQUESTS=128 \
GATEWAY_PERF_RAW_MID_REQUESTS=256 \
GATEWAY_PERF_RAW_HIGH_REQUESTS=512 \
GATEWAY_PERF_IDLE_SAMPLES=2 \
make performance-baseline
```

修改请求量后的结果只能证明脚本可运行，不能视为本文快照的复现数据。

## 局限与下一阶段门禁

首份报告刻意不做生产容量声明：当前只有一台 arm64 笔记本、一台 Docker VM、一个
Gateway 副本、热读取和单一压测端；没有 TLS、Ingress、网络延迟、写入、缓存未命中、
扫描器、签名器和持续 soak。

把性能纳入正式发布门禁前，应在受控 Linux/amd64 Runner 上配置硬资源上限后复跑，
加入 TLS/Ingress 与远端存储，测量读写混合、Range 和大对象，执行 30–60 分钟 soak，
并对比单节点、角色拆分与多 Gateway 部署。
