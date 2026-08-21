# Kubernetes 部署

[English](kubernetes-deployment.md) | [文档索引](README.zh-CN.md)

Artifact Gateway 提供 Kustomize base 和本地开发 overlay。该 overlay 已在 Docker Desktop 上验证，是可执行本地基线，不是生产拓扑。

## 本地快速开始

需要 Docker、`kubectl`、`jq`、`curl`、当前本地 Kubernetes context 和默认 `StorageClass`：

```sh
kubectl config current-context
make kubernetes-local-check
make kubernetes-local-up
make kubernetes-local-status
make kubernetes-local-verify
```

Console、API 和协议路由通过专用 `artifact-gateway-local` IngressClass 暴露在 `http://artifact-gateway.localhost`；`.localhost` 自动解析 loopback，无需修改 hosts。Console 直连 fallback 为 `http://127.0.0.1:18081`。

启动命令构建 Gateway、Console 和 APT 参考 signer 镜像，创建不把凭证写入渲染 manifest 的 Secret，执行全部迁移，等待 Workload，并通过 Ingress 验证 Console 和认证格式 API。

`make kubernetes-local-verify` 创建唯一 Raw Hosted Repository，写入带编码路径的对象，重启 Gateway Deployment，再读回 Repository 和精确字节；同时验证 Raw HEAD/Range、npm scoped encoded slash、OCI `/v2/`、APT 路由，以及 Traefik/nginx 对 encoded backslash/NUL 的拒绝。

这证明 PostgreSQL/RustFS 跨真实 Pod 替换的持久性并实际穿过代理边界。后续命令未提供覆盖值时会复用 namespace Secret 中已有的 PostgreSQL、RustFS、Resolver、管理员、设置加密和 APT signer 凭证，不会静默轮换为默认值。

一次性本地集群默认管理员 Token 为 `local-gateway-admin-token`。共享安装前必须通过 `K8S_LOCAL_*` 变量覆盖 PostgreSQL、RustFS、RPC、管理员、Resolver、设置加密和 APT signer secret。

设置 `K8S_LOCAL_SKIP_BUILD=1` 可复用已加载镜像。Helper 拒绝未知 context；只有显式确认目标并适配镜像加载与暴露方式后才能设置 `ARTIFACT_GATEWAY_ALLOW_NONLOCAL_K8S=1`，但本地 overlay 仍不适合生产。

```sh
make kubernetes-local-down
```

该命令删除 `artifact-gateway-local` namespace、专用 IngressClass、只读 cluster RBAC、PostgreSQL/RustFS PVC、APT signer key PVC 和全部本地数据。遗留对象存储 StatefulSet/PVC 不会原地修改；RustFS-only helper 会失败关闭，要求操作员显式移除或改名，且没有迁移 bypass。

## 本地拓扑

```text
artifact-gateway.localhost:80
       |
       v
Traefik local Ingress
       |
       v
Console nginx（SPA + 同源反向代理）
       |
       v
Artifact Gateway（standalone API + scheduler + worker）
       |                 |                 |
       v                 v                 v
PostgreSQL PVC       RustFS PVC      APT H2 signer sidecar
                                      |
                                      v
                                private-key PVC
                                （仅 loopback）
```

Console、Gateway 和固定 digest 的 Traefik 3.7.10 均以非 root 运行，根文件系统只读，移除 Linux capabilities，设置资源 request/limit 和 HTTP probe。Console/Gateway 不挂载 service-account token；Traefik 仅使用 overlay 声明的 namespaced Role 与只读 discovery RBAC。

H2 signer 是 Gateway Pod 中的加固 sidecar，无 Service/Ingress，只通过 loopback 通信，私钥独占 PVC，Bearer Token 位于 namespace Secret；它只是本地夹具。PostgreSQL/RustFS 是固定单副本依赖。有界临时 `/tmp` 支持流式上传 spool 而不写根文件系统。

Ingress 允许 npm scoped metadata 和 Raw 规范路径需要的 encoded slash，并在边缘拒绝 encoded backslash/NUL。迁移 init container 使用 Compose 相同的 append-only 文件，checksum 已匹配时安全跳过。

可选参考扫描器不属于最小 overlay。当前 smoke test 使用 Compose；如需 Kubernetes 扫描，先增加专用受限网络 Workload 并配置 `GATEWAY_ARTIFACT_SCANNER_URL`。

## Manifest 工作流

可复用 Workload 位于 `deploy/kubernetes/base`，本地服务、存储、配置与迁移位于 `deploy/kubernetes/overlays/local`。

```sh
kubectl kustomize deploy/kubernetes/overlays/local
make kubernetes-local-check
```

离线检查会渲染并解析 overlay，验证持久化、迁移、Ingress/RBAC、probe、容器加固，以及生产 nginx 和 Vite 对 APT 的转发。Live verify 增加路径编码、HEAD/Range、npm、OCI、APT 黑盒，并用 command fake 覆盖 context 拒绝、凭证校验、端口冲突、精确删除与调度逻辑。

## 生产部署路径

生产应把 base 作为输入，而不是部署 local overlay。生产就绪前必须提供：

- 外部托管、高可用且有备份的 PostgreSQL 和 S3 兼容存储；
- 单个 pre-deployment migration Job，而非每 API replica 一个 init migration；
- 分离的 `api`、`scheduler`、`worker` Deployment，并核算全副本数据库连接预算；
- TLS 与保持 streaming、Range、Authorization、大上传语义的 Ingress/Gateway；
- Secret Manager、凭证轮换且无默认本地 Token；
- NetworkPolicy、Quota、PDB、拓扑分散、自动扩缩和节点放置策略；
- 指标日志、告警、PostgreSQL/S3 恢复演练和升级回滚证据；
- 启用自动扫描时的专用 Worker 与网络受限 scanner；
- 公开 APT Hosted 前的外部 signer、托管密钥、轮换撤销、备份恢复、指标和告警。

即使资源齐备，发布就绪门禁仍为权威；Pod rollout 成功不等于生产证据。
