# 快速入门

[English](getting-started.md) · [返回中文 README](../README.zh-CN.md)

本指南会启动完整开发环境：一个 `standalone` Gateway、负责元数据与协调的
PostgreSQL、承担 S3 兼容字节面的 RustFS，以及 Vite Console。整个栈不需要 Redis、
Kafka、Elasticsearch、外部消息队列或服务发现组件。

## 前置条件

- 支持 Compose 的 Docker
- Node.js 24 或更高版本，以及 npm
- GNU Make
- OpenSSL
- 默认开发栈建议预留约 4 GiB 可用内存

后端开发需要 Go，并应使用 `go.mod` 声明的版本。只有 Kubernetes 工作流需要
`kubectl`。

## 1. 准备环境

```sh
make dev-bootstrap
```

`.env` 不存在时，该命令会基于 `.env.example` 创建文件，将权限设置为 `0600`，并
生成以下本地必需配置：

- `GATEWAY_POSTGRES_PASSWORD`
- `GATEWAY_ADMIN_TOKEN`
- `GATEWAY_RESOLVER_TOKEN`
- `RUSTFS_ACCESS_KEY`
- `RUSTFS_SECRET_KEY`
- `RUSTFS_RPC_SECRET`

已有且不是占位符的值不会被替换。修改已有 `.env` 时，脚本会原子写入，并在
`.local-dev/environment-backups/` 保留回滚副本；生成的凭据不会输出到终端。

如果需要修改端口，或启用 OIDC、扫描器、出网代理、APT 签名器及 Worker 配置，继续
编辑 `.env` 即可。

## 2. 启动完整开发栈

```sh
make dev
```

该命令通过 Docker Compose 构建并启动 PostgreSQL、RustFS、数据库迁移和 Gateway。
Console 依赖缺失时会按 lockfile 安装，然后通过 checkout 专属的本地 supervisor 启动
Vite，并等待整个开发栈就绪。

默认本地地址：

| 功能 | 地址 |
| --- | --- |
| Console | <http://127.0.0.1:4173> |
| Gateway API | <http://127.0.0.1:8080> |
| 存活检查 | <http://127.0.0.1:8080/livez> |
| 就绪检查 | <http://127.0.0.1:8080/readyz> |
| RustFS S3 API | <http://127.0.0.1:9000> |
| RustFS Console | <http://127.0.0.1:9001> |

`.env` 中的端口覆盖会体现在 `make dev-status` 输出中。

## 3. 创建第一个仓库

1. 打开 Console。
2. 从本地 `.env` 读取 `GATEWAY_ADMIN_TOKEN`，在管理员登录入口使用该令牌。
3. 进入“制品库”，选择“创建制品库”，然后选择 Hosted、Proxy 或 Group，以及当前已
   支持的制品格式。
4. 创建完成后，从仓库详情页复制客户端配置；页面会给出正确的协议根路径和认证方式。

Hosted 用于直接向 Gateway 发布制品，Proxy 用于读取通过白名单约束的上游，Group 用于
把有序的 Hosted 与 Proxy 成员合并为一个客户端入口。各格式差异和限制以
[协议兼容性基线](protocol-compatibility.md)为准。

## 日常命令

```sh
make dev-status       # 检查 Console、API 代理、存活与就绪状态
make dev-down         # 只停止当前 checkout 管理的 Console
make down             # 停止 Compose 服务，保留数据卷
make dev              # 再次启动完整开发栈
make test             # 执行共享本地回归门禁
```

`make dev-down` 会有意保留 Gateway 和数据服务。需要停止整个 Compose 栈时使用
`make down`；两个命令都不会删除 PostgreSQL 或 RustFS 数据卷。

## 常见问题

### `.env` 不存在或仍有占位符

执行 `make dev-bootstrap`。该命令可重复执行，不会轮换已有有效凭据。

### Console 依赖失效

先用 `make dev-down` 停止当前 Console，再执行 `npm --prefix console ci`，最后通过
`make dev` 重启。不要在 Vite 运行期间替换 `console/node_modules`。

### 本地端口被占用

在 `.env` 中调整 `GATEWAY_HTTP_PORT`、`GATEWAY_CONSOLE_PORT`、
`GATEWAY_POSTGRES_PORT` 或 RustFS 端口，重启对应服务后用 `make dev-status` 确认
实际地址。

### 检测到旧 MinIO 资源

当前本地运行时只支持 RustFS。保护逻辑发现旧 MinIO 容器或数据卷时会关闭失败，不会
删除或挂载这些数据。请先明确检查并保留旧数据，再显式移除或重命名旧资源；脚本不提供
原地迁移绕过开关。

### Gateway 存活但没有就绪

执行 `docker compose --env-file .env -f compose.yml ps`，检查 Gateway、PostgreSQL、
迁移任务和 RustFS。就绪检查会验证当前角色需要的存储依赖；存活检查只代表进程本身
仍在运行。

## 下一步

- [整体架构](../ARCHITECTURE.md)
- [文档总索引](README.zh-CN.md)
- [贡献流程](../CONTRIBUTING.md)
- [Kubernetes 开发部署](kubernetes-deployment.md)
- [恢复手册](recovery-runbook.md)
