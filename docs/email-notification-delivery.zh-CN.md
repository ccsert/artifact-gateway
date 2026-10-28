# 管理员测试邮件交付

[English](email-notification-delivery.md) | [文档索引](README.zh-CN.md)

已批准的小规模部署路线是有限内置通道：Gateway 负责版本化邮件模板和有界
SMTP 发送，PostgreSQL 负责持久收件人快照、测试交付状态、幂等与 Worker fencing。
Prometheus/Alertmanager 可负责外部可用性监测，但增加组件和路由配置。Gateway
或数据库停机时，内置通道不能报告自身失联。决策见
[#211](https://github.com/ccsert/artifact-gateway/issues/211) 与
[#212](https://github.com/ccsert/artifact-gateway/issues/212)。

本通道支持**管理员显式发起的合成测试邮件**及
[仓库逻辑配额告警](repository-quota-alerts.zh-CN.md)。本地挂载和 5xx 观测不入
邮件队列；其评估与 Console 配置页仍待后续。
[#229](https://github.com/ccsert/artifact-gateway/issues/229)、
[#230](https://github.com/ccsert/artifact-gateway/issues/230) 的完整范围未交付；
浏览器截图不能代表 Gmail、Outlook 或 Apple Mail 客户端验收。

## 部署契约

未配置文件时默认关闭。`GATEWAY_EMAIL_CONFIG_FILE` 指向运维管理的绝对路径
JSON 文件，启动时离线读取，拒绝未知键。以下仅为合成文档样例：

```json
{
  "enabled": true,
  "host": "smtp.example.test",
  "port": 465,
  "mode": "implicit_tls",
  "from": "gateway@example.test",
  "approvedIPs": ["192.0.2.10"],
  "consoleOrigin": "https://console.example.invalid"
}
```

这些地址不是可用中继。真实 SMTP 与收件人须另行作部署决定。管理 API 不能
提交中继地址、认证、头部或任意正文。

- `mode` 仅允许 `implicit_tls` 或 `starttls_required`。必须 TLS 1.2 及以上，
  验证证书链和主机名，禁止明文回退。
- 每个 DNS 解析结果须属于 `approvedIPs`，直接连接获准 IP，TLS 仍验证 `host`。
  可明确允许私有中继 IP；拒绝 loopback、link-local/metadata、组播和未指定地址。
  DNS 变化须更新配置并重启。
- 可选 `caFile` 为私有 PEM 信任根的绝对路径。可选 `authFile` 为含
  `username`、`password` 的普通 JSON 文件绝对路径，权限限文件所有者
  （0600 或更严格）。仅在 TLS 验证后认证，内容不进入日志或 API。
  原子替换文件可轮换，Worker 每次尝试重新读取。
- 通过现有部署秘密管理配置 `GATEWAY_SETTINGS_ENCRYPTION_KEY`。收件人使用
  AES-GCM 和目标专属关联数据加密；存在排队快照时保持加密密钥稳定。
- 可选 `consoleOrigin` 必须是明确的 HTTPS origin，不能含凭据、查询或 fragment。
  服务端固定追加 `/system?tab=diagnostics`，不用请求 Host 或事件自带 URL。
  未配置则省略按钮。
- API、scheduler 与 email worker 须使用一致的中继、From、Console origin、
  加密密钥、认证与信任配置。独立 Worker 使用
  `GATEWAY_NODE_ROLES=worker`、`GATEWAY_WORKER_KINDS=email`；空 kind 过滤包含邮件。

## 可选 Compose 样例

`compose.yml` 保持邮件未接线。仅在 `.env` 设置 `GATEWAY_EMAIL_CONFIG_FILE`
不会挂载或传递它；需明确加入 `compose.email.yml`。该 overlay 固定容器内路径
`/etc/gateway-email/relay.json`，要求设置 `GATEWAY_EMAIL_CONFIG_DIR` 与已有
settings 加密 key，只读挂载目录，使用 `create_host_path: false`，目录须预先存在。
[仓库内配置文件](../examples/email/relay.json) 保持关闭，无中继或凭据。overlay
要求 Docker Compose 支持 `!reset`，验收夹具还使用 `!override`。

用现有秘密管理流程准备目录。镜像使用 UID/GID 65532；Linux 宿主机以下命令
只安装关闭状态的文件：

```sh
sudo install -d -o 65532 -g 65532 -m 0750 /etc/artifact-gateway/email
sudo install -o 65532 -g 65532 -m 0640 examples/email/relay.json /etc/artifact-gateway/email/relay.json
```

私有部署 `.env` 保持 0600，设置
`GATEWAY_EMAIL_CONFIG_DIR=/etc/artifact-gateway/email`。使用单独生成的 32-byte
`GATEWAY_SETTINGS_ENCRYPTION_KEY`（新安装可用 `openssl rand -hex 32`），与
admin/resolver、数据库、S3/RPC 和 SMTP 凭据独立。已有加密设置或 outbox 快照
时保留原 key，重新生成会失去解密能力。不要提交、打印 key 或展开后的 Compose
环境。Docker Desktop 上须从镜像 UID 验证所有权与可读性，不能假设宿主权限映射。

单进程时在私有 `.env` 设置 `GATEWAY_NODE_ROLES=standalone`：

```sh
docker compose --env-file .env -f compose.yml -f compose.email.yml config --quiet
docker compose --env-file .env -f compose.yml -f compose.email.yml up -d --build --wait gateway
```

分角色时设置 `GATEWAY_NODE_ROLES=api`，再选择 profile：

```sh
docker compose --env-file .env -f compose.yml -f compose.email.yml --profile email-roles config --quiet
docker compose --env-file .env -f compose.yml -f compose.email.yml --profile email-roles up -d --build --wait gateway gateway-scheduler gateway-email-worker
```

profile 提供一个 scheduler 和仅处理 email 的 worker，与 API 共用基础依赖、
revision、配置目录和 key；后台角色不发布宿主机端口。这个小样例支持邮件测试与
配额告警；其他持久任务需要时另配 worker，并按全部进程核算 PostgreSQL 连接池。
升级先停 writer 并完成单一 migration job；此 overlay 不编排升级。

启用已授权中继时，按上文契约替换 `relay.json` 为完整有效配置。可选 `caFile`、
`authFile` 必须使用容器内 `/etc/gateway-email/...` 路径。私有 auth JSON 保持
0600，UID 65532 所有且可读；可信 CA PEM 须可读，目录仅允许授权运维访问。
overlay 不挂载 SMTP 服务，也不生成凭据。配置、CA 或 key 变化后重启全部所选
角色。目录挂载支持原子轮换 auth 文件，worker 每次尝试重新读取。管理员确认目标
及版本后明确发起合成测试，再核对 delivery 与中继接受状态。

未指定配置路径或明确 `enabled: false` 时关闭。已指定的文件缺失、不可读、格式
错误、未知字段、非法中继/CA 或 auth 非私有权限时，进程启动失败；Gateway 日志
报告 `invalid configuration`，错误详情脱敏。settings key 缺失/非法则表现为 capability
`encryption_key_unavailable`。Compose `--wait` 与 `/readyz` 验证进程依赖，
不证明邮件 sender 健康。

`GET /api/v2/email-notifications` 只报告响应 API 进程的配置/key 就绪。API-only
可报告 `ready` 并入队，sender 仍可能无人运行。未配置 worker 不领用、不发送；
持有不同有效 key 的 worker 在 SMTP 前以 `encryption_key_unavailable` 重试。
未配置 scheduler 会记录 `notificationCode: email_disabled`、无 delivery；之后
修好配置也不补发该事件。多个 evaluator 不协商配置，领用到期评估者使用自己的
配置。各角色必须一致并分别核对，节点心跳不能证明中继/key 一致。此样例不新增
集群 sender readiness 或配置同步能力。

可重复门禁不读取 `.env`，不操作已有 stack：

```sh
make email-compose-render-check
make email-compose-check
```

完整门禁构建当前真实镜像，使用独立唯一项目启动一次性 PostgreSQL、RustFS、
API 与后台角色；TLS SMTP sink 只在私有 internal network，不能外发。仅 API
使用临时 loopback 宿主端口。生成的凭据/证书、数据与容器退出时清理。验收覆盖
关闭配置、启动失败/auth 权限、API-only pending、worker 缺配置/key 不同、两个
正确 worker、实际 HTML/纯文本 MIME 接受、RCPT 550/dead、scheduler 缺配置
永久抑制、正确配额交付与 standalone 交付。基础路径接受三封合成邮件，拒绝项无 DATA。
同一隔离栈继续验收 agctl 完整流程：目标/规则默认停用、明确启用、真实 member 与
强制改密会话对 16 个管理操作及 wait 的拒绝、私有输入与输出脱敏、dry-run 无写入
快照，以及缺通道/key、无 sender 的 pending。回环代理在真实 test/replay 已提交后
扣留响应至 CLI 超时；通过回读及原 test key/版本恢复，不新增队列行。TLS sink
可接收 DATA 后丢弃最终确认，重试保留 `possibleDuplicate`，并实际观测重复 MIME。

配额 warning → critical → resolved 使用真实 Raw 字节及未修改的 15 秒 scheduler
节奏。仅停止自有 scheduler，等待超过 30 秒采样年龄策略后，CLI 暴露 `stale` 并
保留 critical；重启后仍需新鲜恢复样本。更新目标后，已有规则继续固定旧版本，
记录 `target_changed`，不会悄悄跟随新配置发送。给 Python 入口增加
`--evidence-dir output/email-check-1` 可保存源码哈希、已检查秘密的 CLI JSON/退出码
及自有资源清理报告；输出目录必须尚不存在。最终 JSON 报告实际 MIME 数量。
本机 Docker 默认地址池已被占用时，可用 `--fixture-subnets INTERNAL INGRESS`
为此次一次性夹具选择两条明确预留、互不重叠的私有 IPv4 子网。默认运行仍使用
Docker 的地址分配；覆盖只写入临时 Compose 文件。

此门禁不代表真实 SMTP provider、收件箱/邮件客户端、生产、Kubernetes 部署或
集群 sender 健康验收；已有 sender/PG 契约测试覆盖更广 TLS/重试/fencing 矩阵。

## Console 操作流程

打开**系统运行 → 邮件通知**（`/system?tab=email`）。平台管理员可创建停用目标、
编辑名称/语言、替换只写收件人，并明确确认启用/停用。旧地址不可读取；编辑时
地址留空保留。保存不发测试。目标版本变更会使旧队列交付停止，配额规则需重新
确认路由；在途邮件不能撤回。

预览提供警告、严重、恢复三态，中英文 HTML 及等价纯文本。固定合成数据无发送。
浏览器先重建惰性布局，再放入不允许脚本、导航、表单或外部资源的隔离沙箱。
预览链接禁用；浏览器效果不能替代真实邮件客户端兼容验收。

已启用目标及部署通道就绪才能**发送合成测试…**。必须确认名称、ID、版本、语言、
场景与真实入队影响；无法读取旧收件地址。不确定请求重试保持目标/版本/场景和
同一 UUID 标识；冲突必须取消、刷新后重新确认。最新 50 条结果在页面可见时每
30 秒刷新，显示固定安全错误、尝试次数、下次时间、可能重复及明确 SMTP 接受
语义。手动重放仍使用管理员 API。

地址只保存在当前表单，不入 URL、日志或持久浏览器存储；成功、取消、导航、
冲突或权限阻断时清除。CAS 冲突仅保留名称/语言安全草稿，必须显式刷新最新
目标版本再保存。权限或强制改密错误整页阻断；显式成功刷新才恢复。

主机可选验收需迁移后的隔离数据库，名称以 `_test` 结尾：

```sh
EMAIL_CONSOLE_BROWSER_E2E=1 TEST_DATABASE_URL='<isolated-test-connection>' \
  go test -count=1 -tags=integration ./internal/app -run '^TestPostgresEmailConsoleBrowserTLS$' -v
```

依赖已安装 Console dependencies 与 Playwright Chromium；启动本机自有 API、
4196 Vite（可用 `EMAIL_CONSOLE_BROWSER_PORT` 改端口）及绝不转发邮件的 TLS SMTP
接收端。实际验证 UI/API/PG 新建、编辑、CAS、明确测试、accepted multipart MIME
和永久收件拒绝，再覆盖 320/390/1440px、中英明暗共 36 套三态预览。无生产收件人
或 SMTP secret。截图在 `.impeccable/review/` 留作本地证据；该主机测试显式开启，
不能把默认容器集成的跳过说成通过。

## 管理员 API

全部端点要求平台管理员，拒绝强制改密 Session，响应为 `Cache-Control: no-store`。

| 端点 | 行为 |
| --- | --- |
| `POST /api/v2/email-notifications:preview` | 固定 `scenario`、`locale` 离线预览，返回 subject/HTML/text/模板版本，从不发送 |
| `GET /api/v2/email-notifications` | 安全就绪状态：`ready`、`email_disabled`、`encryption_key_unavailable` |
| `GET/POST /api/v2/email-targets` | 列出/新建单收件人目标，默认停用，收件人只写 |
| `GET/PUT /api/v2/email-targets/{targetId}` | 查询/更新，更新须 `If-Match`，省略 recipient 保留密文 |
| `POST /api/v2/email-notifications:test` | `targetId`、`scenario`、目标 `If-Match` 与 UUID `Idempotency-Key`；入合成队列后返回 202 |
| `GET /api/v2/email-deliveries` | 最新安全状态，`limit` 1–100，默认 50 |
| `GET /api/v2/email-deliveries/{deliveryId}` | 状态和版本 |
| `POST /api/v2/email-deliveries/{deliveryId}:replay` | 仅 dead，须 `If-Match`、启用且未变更的目标及中继 |

语言为 `en`、`zh-CN`，场景为 `warning`、`critical`、`resolved`。拒绝未知字段
与地址/头部注入。目标只返回 `recipientConfigured`，不返回地址或密文。
状态只返回有限错误码、尝试次数与时间，不含 SMTP 原始回复、主机、凭据或正文。
目标变更、显式测试和重放只以 ID 记审计。

同幂等键、同描述符返回原记录；换目标/版本/场景则 409。PostgreSQL 原子限制
全局滚动一分钟最多五次新测试/重放、最多 1,000 条活动交付；重复请求不多占名额。
版本冲突 412，停用目标或非 dead 重放 409，限流 429，中继停用/无密钥/队列满 503。
重放保留事件 ID、收件人密文、内容描述符和可能重复标记。目标变更须新建显式测试。

## 队列只读诊断

管理员可调用 `GET /api/v2/email-queue/diagnostics`，响应进程关闭邮件时也可用。
该端点执行相同管理员及强制改密检查，返回 `Cache-Control: no-store`。GET 不发送、
不领取、不重放、不探测 SMTP，也不更新队列；不增加 Console 页面或 CLI 命令，
保持默认关闭发送。

`scope: whole_retained_queue` 包含全部保留的测试及仓库配额交付，包括终态历史，
不从最近 50 条状态估算。PostgreSQL 在一个 MVCC 语句快照中聚合，并仅采样一次
数据库 `clock_timestamp()`（`sampledAt`、`clock: database`）。Memory 使用一次
store 锁和自身时钟（`clock: memory_store`），范围仅该实例而非集群。快照之后
提交的并发变化不包含在本次结果。

五个状态计数互斥且合计为 `total`。其他计数与状态及彼此可重叠，不能求和：

- `futureBackoff`：pending/retrying 且 `nextAttemptAt` 在未来；正常退避本身
  不能证明队列停滞。
- `validLeases` / `expiredLeases`：delivering 的租约分别晚于 / 不晚于
  `sampledAt`；缺少到期时间的不属于两类中的任一类。
- `awaitingCancellation`：已撤销的 pending/retrying（无论是否到期），以及
  租约过期的已撤销 delivering。
- `awaitingExhaustion`：已达八次尝试且到期的 pending/retrying 或租约过期的
  delivering；可与取消重叠。
- `executableCandidates`：到期 pending/retrying 或过期 delivering，尝试少于八次，
  无自动取消，且投影 claim 的取消/耗尽清理后没有同规则活动前序事件。
- `predecessorBlocked`：其他条件满足、仅被此前序事件阻塞的候选；不含已经被
  次数、取消或到期条件排除的行。

候选对应领取加行锁前的资格；其他事务可能持锁，或在采样后领取/完成。
`oldestExecutableWaitSeconds` 使用采样时钟，取候选自身 `nextAttemptAt` 或过期
租约以来的最大秒数；无候选时为 null。它不重建前序事件清除时刻，也不证明持续
具备领取资格。

`failureClasses` 对全部保留行的当前非空错误码聚合，最多八类：`configuration`、
`transport`、`temporary_rejection`、`permanent_rejection`、`outcome_unknown`、
`cancelled`、`attempts_exhausted`、`unknown`。未知/旧错误只归 unknown，绝不回显。
响应不包含邮件身份、收件人、SMTP 地址、key 摘要、原始错误、正文或租约令牌。
`lastSmtpAcceptedAt` 是保留行中最新的持久 `acceptedAt`，由持有效租约的成功完成
操作在最终 DATA 正回复后记录；无此证据为 null。它不证明收件箱送达或当前 worker
活动；`updatedAt` 也不是 worker 活动证据。尚无持久角色心跳或预期角色配置，
`workerHealth` 始终为 `unknown`；不能推断 worker 健康、无人运行、集群配置一致
或完整停机。响应通过 `evidenceLimitations` 再次说明这些限制。

## 投递语义

状态为 `pending`、`delivering`、`retrying`、`accepted`、`dead`。每次领取使用
数据库时间、`FOR UPDATE SKIP LOCKED`、新 fencing token 和 30 秒租约。重启生成
新的 Worker Session。过期领取可以重领，旧 token 无法完成。SMTP 单次总时限
10 秒，并受领取调用前取得的本机 monotonic 起点加 25 秒的保守预算限制，避免把
数据库 wall clock 当成本机截止时间。

最多八次尝试，5 秒指数退避，上限一小时。网络失败与 SMTP 4xx 重试；5xx 或
无效内容终止。TLS/认证错误使用固定码和有限次数。发送前发现目标停用或版本变化
则 dead；已经在途的邮件可能完成，停用不能撤回。

DATA 最终正回复仅表示 **SMTP 已接受**，不等于进收件箱或已读。正文发出后丢失
最终回复为 `outcome_unknown`，重试可能重复。过期在途租约也保守标记
`possibleDuplicate`，此标记跨重试和重放保留。不可变版本 1 合成描述符使用稳定
Message-ID、时间与 MIME boundary，并持久快照 From/Console origin，配置变化不改正文；不承诺 exactly-once。

HTML 使用转义语义值、内联关键样式、presentation table、600px 最大宽度以及
窄屏/深色增强。严重级别同时以文字和颜色区分；multipart/alternative 包含等价
UTF-8 纯文本与 HTML。没有外部图片、字体、跟踪像素或附件。版本 1 模板须继续
支持已排队描述符的重放。终态历史保留在 PostgreSQL，归档/保留策略待后续；查询
与活动积压有界，本批不构建通用邮件或监控平台。

## 验证与下一批

测试通过公开 HTTP、Store 和 Sender 边界，使用隔离 PostgreSQL 与自有 TLS SMTP
收集器，收集器从不转发。覆盖重启恢复、并发全局限流、Worker fencing、重试耗尽、
安全重放、TLS 信任/降级失败、实际双语 multipart MIME 和管理员权限。
`make integration-test` 包含这些测试。

[有限仓库配额评估器](repository-quota-alerts.zh-CN.md)已将逻辑不可变事件与邮件
交付在同一事务创建。新合成预览使用版本 2，已排队版本 1 仍保持原文案与渲染。
显式本地挂载/进程 5xx 的评估与 Console 配置、真实邮件客户端验收及真实部署继续后续；
S3/NAS 池容量保持 unknown。见[运维信号](operational-signals.zh-CN.md)和
[#211](https://github.com/ccsert/artifact-gateway/issues/211)。
