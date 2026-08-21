# Artifact 扫描器契约

[English](artifact-scanner-contract.md) | [文档索引](README.zh-CN.md)

`internal/scanning` 是 Artifact Gateway 与外部安全扫描器之间的受控边界。它不执行用户配置的命令，也不允许扫描器直接修改 Repository 状态。

进程启动时通过 `GATEWAY_SCANNER_ENDPOINT` 启用。管理员可为不可变 Artifact 排入 scan；`scan` Worker 解析协议自有资产、流式发送给 adapter，并以乐观并发只合并扫描器拥有的 intelligence 字段。Hosted Repository 还可选择在新发布后异步排队。

## 配置

| 变量 | 默认值 | 含义 |
| --- | --- | --- |
| `GATEWAY_SCANNER_ENDPOINT` | 空 | 启用扫描的 endpoint |
| `GATEWAY_SCANNER_NAME` | `artifact-scanner` | 写入漏洞摘要的有界扫描器身份 |
| `GATEWAY_SCANNER_TOKEN` | 空 | 发往 endpoint 的 Bearer Token |
| `GATEWAY_SCANNER_TIMEOUT` | `2m` | 每次扫描 1s-30m deadline |
| `GATEWAY_SCANNER_HEALTH_ENDPOINT` | 空 | 可选健康与漏洞库元数据 endpoint |
| `GATEWAY_SCANNER_HEALTH_TIMEOUT` | `2s` | 健康检查 1s-30s deadline |
| `GATEWAY_SCANNER_DATABASE_MAX_AGE` | `24h` | 漏洞库最大年龄 1m-720h |
| `GATEWAY_SCANNER_MAX_RESPONSE_BYTES` | `524288` | JSON 1 KiB-8 MiB |
| `GATEWAY_SCANNER_MAX_ARTIFACT_BYTES` | `21474836480` | 逻辑 Artifact 上限，硬上限 1 TiB |
| `GATEWAY_SCANNER_FORMATS` | 扫描器支持的全部格式 | 逗号分隔 allowlist |

除 localhost/loopback 外，两个 endpoint 必须 HTTPS，且不能含 userinfo、query 或 fragment。缺少 scan endpoint 时任何其他 scanner 设置都会被拒绝。只缺 health endpoint 时扫描可用，但健康与数据库新鲜度为 unknown。

启动日志和诊断只显示脱敏状态、身份、版本、时间和格式，不暴露 endpoint 或 Token。

### 内置 Trivy 参考 adapter

可选 Compose `scanner` profile 构建 `Dockerfile.scanner`，以固定 digest 的 `aquasec/trivy:0.72.0` 运行非 root `reference-scanner`。它无 Linux capability，与 Gateway 共用网络 namespace，只监听 `127.0.0.1:18082`，不暴露 host/Docker network port。

本地 `.env` 可配置：

```dotenv
COMPOSE_PROFILES=scanner
GATEWAY_SCANNER_ENDPOINT=http://127.0.0.1:18082/v1/scan
GATEWAY_SCANNER_HEALTH_ENDPOINT=http://127.0.0.1:18082/v1/health
GATEWAY_SCANNER_NAME=trivy-reference
GATEWAY_SCANNER_FORMATS=maven,raw,npm,pypi,go,conan
GATEWAY_SCANNER_TOKEN=replace-with-a-local-scanner-token
GATEWAY_SCANNER_TIMEOUT=10m
GATEWAY_SCANNER_MAX_ARTIFACT_BYTES=1073741824
```

Sidecar 不接受请求中的命令参数。它把每个 asset 写入私有临时目录并复查 size/SHA-256，运行固定 `trivy filesystem` 漏洞/许可证分析，并从同一原生报告生成 CycloneDX。

CycloneDX 按内容寻址保存在有容量上限的 `gateway-scanner-sboms` volume，Gateway 只记录受 Bearer 保护的内部 URL。响应包含有界 license、severity summary 和最多 200 条 finding；超出默认响应上限时回退为完整 severity summary。

Trivy 数据库缓存在 `gateway-trivy-cache`，可使用 `GATEWAY_EGRESS_PROXY` 下载。License 按 SPDX ID 聚合，超过 100 个唯一 ID 明确失败，避免不完整 allowlist 判定。

参考 adapter 支持 Maven、Raw、npm、PyPI、Go、Conan。它明确拒绝 OCI，因为 manifest/layer blob 集不等同于应用 whiteout 后的 root filesystem；格式感知的外部 OCI scanner 仍可实现通用契约。

部署侧 `REFERENCE_SCANNER_*` 限制 loopback、固定命令、scan/health timeout、Artifact/output/SBOM 容量和并发。Compose 默认并发 1、PID 256、CPU 2、内存 4 GiB、tmpfs 1.5 GiB，镜像根只读；增大 Artifact 或并发必须同步评审容器限制。

`make reference-scanner-smoke` 验证真实 sidecar 的 UID、只读根、资源、tmpfs、volume、网络约束，并执行真实 Trivy/CycloneDX；修改 adapter、pin、镜像或 profile 时运行。

## 持久执行

使用调用方拥有的幂等 key 排队：

```http
POST /api/v2/repositories/{repositoryId}/artifact-scans
Authorization: Bearer <token with repositories:intelligence>
Idempotency-Key: ci-build-1842-scan
Content-Type: application/json

{"coordinate":"com.example:widget:1.2.3","digest":"sha256:<64 lowercase hex characters>"}
```

返回 `202` lifecycle job；同 key/body 返回原 job，不同身份返回 `409`。格式必须在 allowlist。能力发现分别报告手动 `artifactScanning` 和发布 hook `publicationScanning`，Proxy cache 或无发布 hook 的格式不会显示虚假自动扫描开关。

Repository Security Policy 可设置 `autoScanOnPublish: true`，在 Maven、OCI、Raw、npm、PyPI、Go、Conan Hosted 新发布可见后排入同一任务。只影响未来发布；调度 best effort，失败不回滚发布，并写审计。稳定 `publish-scan:<sha256>` 防止协议重试重复任务。

Worker 必须包含 role `worker`、kind `scan`，并使用相同 scanner 配置。任务复用 PostgreSQL lease、retry、cancel、run-now。结果只替换 SBOM、license、vulnerability 等 scanner-owned 字段；签名和 provenance 在并发更新时也保持不变。

Resolver 在打开对象前用 Repository 元数据验证 coordinate/digest。Maven SNAPSHOT 选择对应 timestamped build；OCI 遍历 manifest/index 及 config/layer；npm/Raw/PyPI 解析不可变文件；Go 解析 info/mod/zip；Conan 解析 recipe/package revision。

Hosted 均支持；已缓存的 npm/PyPI/Go Proxy 可扫描。Maven/OCI/Raw/Conan Proxy 使用 legacy cache index，在专用 adapter 完成前 enqueue 时拒绝。扫描从不获取可变上游内容。

当前 intelligence 身份为 coordinate+digest；若两个 Maven SNAPSHOT 主 digest 相同会共享身份，直到模型增加 build number 前无法区分不同 ancillary file。

## 逻辑 Artifact 输入

一次扫描包含 Repository ID、format、coordinate、SHA-256 digest，以及一个或多个有 path/size/digest/media type/stream opener 的 asset。Raw/npm 常为一个对象，OCI/Maven/Conan/PyPI 可为多个；对象键与存储凭证绝不作为 metadata 发送。

HTTP adapter 对每个 asset 流式复算 SHA-256，拒绝截断、超限和变更。默认最多 256 assets、总计 20 GiB、两分钟请求和 512 KiB 响应；构造时可在硬边界内调整。

## HTTP adapter

请求使用 `POST multipart/form-data`、`X-Artifact-Scanner-Schema: v1` 和 `X-Artifact-Scanner-Accept-Schema: v2, v1`。首 part `metadata` 为 JSON，声明 Repository、格式、坐标、摘要和每个 `asset-N` 的 path/digest/size/mediaType，后续 part 按声明名称流式发送，不在进程内缓存完整 Artifact。

响应为严格 JSON `v1` 或协商后的 `v2`。可包含 SBOM 引用、license 和 vulnerability status/count/findings。未知字段、非法 URL/digest、负计数、超限集合、非 JSON 与尾随值均拒绝。Gateway 记录自己配置的 adapter name 和完成时间，不信任扫描器自报身份或时间。

旧 Gateway 未发送 accept header 时，scanner 必须返回不含 findings 的严格 v1；新 Gateway 接受 v1/v2，但 v1 带 findings 无效。

v2 findings 可选，若存在则必须与 severity 计数完全一致，identity 不重复，最多 1000 条且服从响应字节上限。`affected` 至少有一个漏洞；`clean/not_scanned` 计数必须为零。该不变量在 scanner 边界和管理 intelligence 写入边界都执行。

## 健康与漏洞库新鲜度

配置 health endpoint 后，Gateway 使用同一 Bearer 发起有界 GET，接收严格 v1 的 `healthy|degraded|unhealthy`、可选 scanner/database version 和数据库 `updatedAt`。响应最多 64 KiB，拒绝未知字段与未来超过五分钟的时间，且不跟随 redirect、不传播错误体。

Gateway 本地生成 `checkedAt` 并按最大年龄判断；健康 scanner 配旧数据库会降级。管理员诊断和 Console Dependency Panel 展示脱敏状态、格式、版本和新鲜度。Scanner 健康是运维证据，不是进程 readiness，不因临时故障中断 Repository 读取写入。

## 传输策略

外部必须 HTTPS；凭证只能 Bearer；拒绝 userinfo/query/fragment 与 redirect；scanner error body 不进入生命周期或操作员响应。报告不能替换 publisher signature/provenance。

Quarantine 是独立治理工作流。Conan 只能隔离 recipe revision 分发锚点，package revision 仍可扫描。自动隔离和扫描结果驱动的读取阻断不属于当前契约。

## 持久状态与补偿

每个 scan 是 lifecycle job，payload 保存 format/coordinate/digest，任务本身是权威状态。查询接口返回最新状态，`never` 表示从未创建；终态后可用新幂等 key 再排，active job 不重复。

`artifact-scans:reconcile` 枚举可见发布并与最新 job 对比，为缺失身份使用稳定 key、重试 failed/cancelled、保留 active/completed。排序优先 actionable，使有界重复调用可遍历大仓库。即使未开启自动扫描也可显式历史 backfill，调用受 `repositories:intelligence` 保护并有 limit。

Console Scanning Tab 分开展示手动和发布扫描能力、协议身份选择器、近期任务和补偿操作。默认从 `artifact-identities?purpose=scan` 获取历史版本及 Conan recipe/package revision；手输仅为高级恢复路径。

未配置 scanner 时页面仍显示但禁用 mutation，并提示 endpoint、format allowlist 和 Worker kind。Endpoint 与 Token 永不返回浏览器。扫描异步且 best effort，失败不回滚发布，也不会自动隔离或改变读取策略。
