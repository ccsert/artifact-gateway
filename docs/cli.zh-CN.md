# agctl 命令行

[English](cli.md) · [文档索引](README.zh-CN.md)

`agctl` 是面向 CI 流水线、平台管理与 agent 的网关命令行客户端。它用 bearer
凭据直接对 `/api/v2` 下的管理 API 编程。当前 checkout 提供 `version`、`info`、
`commands`、`shell`、`auth login|logout|whoami|change-password` 与
`config profile add|list|use|remove`；其余命令面按可独立评审的切片陆续交付，只有实现后才会出现在
命令树中。客户端形态与机器契约记录在
[ADR 0007](adr/0007-agctl-cli-shape.zh-CN.md)。

## 安装

`agctl` 是单一客户端二进制，版本跟随 Release 在 `VERSION` 文件中固定的值；CI
流水线、平台管理与 agent 运行同一份构建。它不携带任何服务端配置或秘密：连接设置
来自 flag、环境变量或下文介绍的 profile 文件。

### Release 附件

当前 checkout 的发布构建会为后续包含本批次的 Release，在 Gateway 附件旁
产出各平台客户端附件：

```text
artifact-gateway-agctl_<version>_linux_amd64.tar.gz
artifact-gateway-agctl_<version>_linux_arm64.tar.gz
artifact-gateway-agctl_<version>_darwin_amd64.tar.gz
artifact-gateway-agctl_<version>_darwin_arm64.tar.gz
```

客户端附件只包含 `agctl` 以及 `VERSION.txt`、`INSTALL.txt`；服务端二进制、
healthcheck、迁移与环境模板留在 `artifact-gateway_<version>_<goos>_<goarch>`
附件中。`SHA256SUMS` 覆盖该 Release 的全部附件，解压前先校验：

```sh
sha256sum -c SHA256SUMS --ignore-missing   # macOS 使用 shasum -a 256 -c SHA256SUMS
tar -xzf artifact-gateway-agctl_<version>_<goos>_<goarch>.tar.gz
./agctl version
```

`agctl version` 输出客户端版本与 revision，与 Release tag 核对后再使用。已发布的
`v0.9.0` 仅包含 CLI 骨架与源码本地构建，未纳入本次 auth/profile/分发批次，也没有
独立 agctl 附件。这些附件需等后续包含本批次的 Release；合并代码不会发布它们。

### Go module 安装

```sh
go install github.com/ccsert/artifact-gateway/cmd/agctl@<tag>
```

`<tag>` 是 `go.mod` 声明 `github.com/ccsert/artifact-gateway`（即源码仓库路径，见
[ADR 0008](adr/0008-go-module-path.zh-CN.md)）的 Release tag（`v<VERSION>`）。本变更
之后的首个发行是最早满足条件的 tag；`v0.9.0` 及更早版本声明的是此前无法解析的旧路径，
不能这样安装。module 模式安装以 tag 作为版本，但 Go 工具链不为 `pkg@version` 构建
记录 VCS 信息，因此 `agctl version` 的 revision 显示为 `unknown`；需要 revision 时请
使用 Release 附件或镜像。发布满足条件的 tag 并验证公开 GitHub 安装仍由
#347 和 #128 跟踪发行验收。

### 容器镜像

客户端镜像由当前 checkout 构建，供在 CI 任务内执行 `agctl` 的流水线使用。它是
distroless static 镜像，含 CA 证书、无 shell，且只包含客户端二进制——Release
workflow 仍然只发布 Gateway 与 Console 镜像，因此按需要的版本在本地构建：

```sh
make cli-docker-build                                     # docker build -f Dockerfile.agctl -t artifact-gateway-agctl:dev .
docker run --rm artifact-gateway-agctl:dev version
docker run --rm -e AGCTL_SERVER=https://gateway.example.com -e AGCTL_TOKEN="$GATEWAY_TOKEN" \
  artifact-gateway-agctl:dev info
```

`make cli-docker-build` 会把 `VERSION` 文件的值与当前 Git revision 作为构建参数
传入。镜像内使用 profile 文件时，把它挂载到 nonroot 家目录：
`-e HOME=/home/nonroot -v "$HOME/.config/agctl/config.yaml:/home/nonroot/.config/agctl/config.yaml:ro"`。

### 本地构建

```sh
make cli-build                             # 输出 dist/agctl_<goos>_<goarch>
dist/agctl_darwin_arm64 version
dist/agctl_linux_amd64 info --server https://gateway.example.com --token "$GATEWAY_TOKEN"
```

`make cli-build` 用 `go.mod` 声明版本的本地 Go 工具链交叉编译 linux 与 darwin
的 amd64/arm64，并用与 Release 附件相同的 `-ldflags` 注入 `VERSION` 文件的值与
当前 Git revision。在 checkout 内执行 `go install ./cmd/agctl` 会把同一客户端
安装到 `$GOBIN`；没有注入时它报告开发版本而非 Release 版本。

## 配置

所有设置的解析优先级固定：

1. 显式 flag（`--server`、`--token`、`--profile`）；
2. 环境变量（`AGCTL_SERVER`、`AGCTL_TOKEN`、`AGCTL_PROFILE`）；
3. `~/.config/agctl/config.yaml` 中选中的 profile。

需要服务端的命令在缺少服务端配置时，会在发出任何请求之前以退出码 2 失败。
`--server` 是网关根 URL；管理端点会自动加上 `/api/v2` 前缀。

配置文件支持多个 profile，且不得对其他用户可读（权限 0600）——否则命令会
失败并提示 `chmod 600`：

```yaml
current_profile: production
profiles:
  production:
    server: https://gateway.example.com
    token: replace-with-a-service-account-token
    insecure: false
```

Profile 选择顺序：`--profile`、`AGCTL_PROFILE`、`current_profile`、`default`
profile，最后是只定义一个 profile 的文件。该文件由 profile 命令管理、无需手工
编辑：`agctl config profile add|list|use|remove`；每次写入都以 0600 权限原子
替换文件。

已存 token 只用于与 profile 绑定目标一致的实际 server。用 `--server` 或
`AGCTL_SERVER` 覆盖为不同目标时不会复用该 token；需用 `--token` 或
`AGCTL_TOKEN` 显式提供新目标的凭据。主机名与默认端口的等价写法保留绑定，
不同 IPv6 authority、接口 zone 与 base path 大小写不会共享凭据。

## 交互式 shell

`agctl shell` 是可选的终端前端，与脚本和 agent 共用同一 `RunCLI` 命令注册表。
stdin 和 stdout 都必须是 TTY；管道输入或重定向输出以退出码 2 拒绝。
自动化仍使用普通命令。提示符显示实际 profile、已脱敏的 server URL 和仓库上下文。

```text
agctl --profile production shell
agctl [production | https://gateway.example.com | repo:]> use repo repository-id
agctl [production | https://gateway.example.com | repo:repository-id]> artifact list
agctl [production | https://gateway.example.com | repo:repository-id]> set json on
agctl [production | https://gateway.example.com | repo:repository-id]> repo list
agctl [production | https://gateway.example.com | repo:repository-id]> set dry-run on
agctl [production | https://gateway.example.com | repo:repository-id]> set verbose on
agctl [production | https://gateway.example.com | repo:repository-id]> use profile staging
agctl [staging | https://staging.example.com | repo:]> exit
```

- `use repo ID` 仅向声明了 `--repo` 的命令提供默认值；`use repo -` 清除该上下文。
  命令上的显式 flag 优先于会话默认值。
  即使已选择会话仓库，`artifact search --global --q QUERY` 仍搜索所有可读取仓库。
- `use profile NAME` 为本会话提供 `--profile` 并清除仓库上下文，不修改持久化
  `current_profile`。既有 flag 和环境变量优先级（包括 server/token 覆盖）保持生效。
- `set json|dry-run|verbose on|off` 切换 sticky 默认值。启动时的 `--json`
  （或 `-o json`）、`--dry-run` 和 `--verbose` 用于初始化。单条命令可用
  `--json=false`、`--dry-run=false` 或 `--verbose=false` 覆盖。各命令沿用普通模式的
  JSON envelope 与 schema；提示符和交互通知属于终端 UI，整个会话不是 JSON 流。
- Tab 补全命令词、`use profile` 的名称，以及通过当前凭据授权仓库列表读取的
  `use repo` ID（读取最多等待两秒）。
- 上下方向键召回 `~/.config/agctl/history` 中的命令路径。历史刻意省略**全部参数**，
  包括仓库/profile 选择、密码/token flag、私有正文和 secret-output 路径；从不记录
  命令输出或一次性秘密。最多保留 500 条，以 0600 权限原子替换；拒绝符号链接、
  非普通文件、其他用户可读文件和超过 1 MiB 的文件。历史不安全或不可用时显示通知，
  禁用持久化。

输入支持单双引号和反斜杠转义，参数按字面处理，不做环境变量展开、管道或命令替换。
各命令接收隔离的空 stdin：`--password-stdin`、`--token-stdin` 及其他 stdin 读取
立即失败，不会吞掉后续 shell 命令。使用支持的私有文件 flag，或退出 shell 后通过
管道执行 stdin 命令；不要把秘密或输入正文粘贴成 shell 命令。不会新增密码提示。
权限校验、危险命令必需的 `--yes` 确认和 dry-run 支持与普通命令一致；`--yes` 必须
逐条显式提供，不会由 sticky 设置启用。

`exit`、`quit` 或空提示符处的 Ctrl-D 正常退出。Ctrl-C 清除当前输入，或取消正在
执行的**客户端请求/等待**并返回提示符；不承诺远端 job 被取消，也不会回滚已提交的
写入。关闭终端结束会话。不提供 undo/redo 或通用仓库 undelete；恢复需使用实际可用
的制品/快照恢复端点。`--dry-run` 只为支持预演的命令生成计划。

## 全局 flag

| Flag | 含义 |
| --- | --- |
| `--server URL` | 网关根 URL，例如 `https://gateway.example.com` |
| `--token TOKEN` | 管理 API 的 bearer token |
| `--profile NAME` | `~/.config/agctl/config.yaml` 中的 profile |
| `--json` | 输出带版本号的 JSON 信封而非人类文本 |
| `-o table\|json` | 输出格式（二者同时给出时 `--json` 优先） |
| `--timeout DURATION` | 含重试在内的整体请求超时（默认 `30s`） |
| `--verbose` | 向 stderr 打印请求方法与 URL；凭据永不打印 |
| `--insecure` | 跳过 TLS 证书校验 |
| `--dry-run` | 只规划写操作、不提交（写命令） |
| `--yes` | 确认提示一律视为同意（写命令） |

全局 flag 可放在命令名前后：`agctl --json info` 与 `agctl info --json`
等价。`--dry-run` 与 `--yes` 作用于写命令，只读命令接受但忽略这两个 flag；
无法无副作用地预演的写命令会在任何变更之前以用法错误（退出码 2）拒绝
`--dry-run`，命令树对这类命令报告 `dry-run: unsupported`。

## 输出契约

人类模式把结果写到 stdout、诊断写到 stderr。命令从不交互提问：缺少必需输入
时在首个请求之前以退出码 2 失败，因此 `agctl` 可安全用于流水线与 agent 循环。

使用 `--json` 时，stdout 恰好包含一个文档——失败时同样如此：

```json
{
  "schemaVersion": 1,
  "ok": false,
  "data": null,
  "error": {
    "code": "access_denied",
    "httpStatus": 403,
    "message": "not allowed",
    "hint": "the credential is valid but lacks the required permission for this operation",
    "requestId": "1f0c..."
  },
  "meta": null
}
```

- `schemaVersion` 从 1 起，仅在本 schema 发生破坏性变更时递增。
- 服务端响应携带 problem 文档时，`error.code` 即服务端错误码；本地失败使用
  `usage_error`、`credentials_missing`、`connection_error`、
  `partial_failure` 或 `internal_error`。`secret_not_delivered`（退出码 1）表示
  一次性秘密已签发但未能写入 `--secret-output`，`data` 给出需要吊销的 ID。
- 未到达服务端的失败，`error.httpStatus` 为 null。
- `error.code` 为 `wait_timeout` 表示有界的 `--wait` 用完了时间（退出码 8）；
  操作仍在服务端继续。`agctl status` 以 `unhealthy` 失败，或在 `--strict` 下
  以 `degraded`、`unknown` 失败（退出码 1）。
- `email delivery wait` 对 `dead` 投递报告 `delivery_failed`（退出码 1）。
  这是客户端判定的终态结果，`error.httpStatus` 为 null；`data` 保留实际完整投递
  记录，包括失败码和重复风险。等待不重放、不修改该投递。
- 列表统一为 `{"items": [...], "nextCursor": null}`；数据来源没有服务端
  分页或列表已读到末尾时 `nextCursor` 为 null，`meta.pagination.mode` 说明
  真实模式：本地命令树和一次返回全部内容的端点为 `none`；服务端分页为
  `cursor`，附带 `pageSize`、已读页数 `pages` 和 `complete`；没有 cursor 的
  窗口为 `bounded`，附带 `limit`，只有窗口未满时才给出 `complete: true`；
  偏移窗口为 `offset`，附带 `limit`、`offset` 和服务端的 `total`。服务端没有
  给出的总数不出现，绝不写成 0。
- `meta.scope` 记录一页对自身覆盖范围的说明，例如全局检索的
  `searchedRepositories`，或浏览页所属的 Group。
- `access check --require` 在要求的权限未被授予时以 `not_permitted`（退出码 6）
  失败，判定说明仍在 `data` 中。
- `--follow`（`runtime logs` 和 `audit list`）配合 `--json` 输出 NDJSON：每行一个
  带版本的事件，`{"schemaVersion": 1, "type": "entry", "data": …}`；可能漏读
  时输出 `gap` 事件；最后恰好一个终止事件 `{"type": "end", "data": {"reason",
  "events", "resume"}, "error": …}`。流必须带 `--duration` 或 `--max-events`
  （否则退出码 2），不会无限运行。`reason` 为 `duration` 或 `max_events`
  （退出码 0）、收到信号时为 `interrupted`（退出码 3）、运行日志会话变化时为
  `scope_changed`（退出码 7）、审计轮询未读完所有页时为 `poll_budget`（退出码 3），
  或 `error`（按错误的退出码）；`resume` 给出新流
  可以继续的位置。流开始之前的用法错误仍是普通的单个信封。
- 失败但仍有内容需要报告时放在 `data`（以及 `meta`）中：列表中途停止前已读
  到的页（退出码 3）、等待超时时已受理的 ID 和最后读到的状态（退出码 8），
  或客户端无法确认结果的写操作的 `{"outcome": "unknown", "readBack": …}`。
  仓库、Group 和制品的写操作在 HTTP 成功但 JSON 无法解析、为空或缺少必要字段时，
  也会报告结果未知。创建保留原幂等键以便安全重放；更新和删除需要先回读状态再
  决定是否重试。

`--verbose` 在 stderr 上解释请求（`> GET https://…`、`< 204`），URL 中携带
凭据的部分会被脱敏；token 本身不会打印，认证失败的响应正文同样不会。

## 退出码

| 退出码 | 含义 |
| --- | --- |
| 0 | 成功 |
| 1 | 通用失败 |
| 2 | 用法错误（flag 错误、缺少输入、无效配置） |
| 3 | 批量部分成功 |
| 4 | 未找到（HTTP 404） |
| 5 | 未认证或缺凭据（HTTP 401） |
| 6 | 无权限（HTTP 403） |
| 7 | 冲突或前置条件失败（HTTP 409/412） |
| 8 | 连接失败或超时 |

## 命令

### `agctl version`

打印客户端版本、revision 与 Go 版本；`--json` 的 data 为
`{version, revision, goVersion, modified}`。

### `agctl info`

探测部署并按来源逐行报告：

- `GET /livez` 与 `GET /readyz`——可达性与就绪状态；
- `GET /api/v2/identity`——调用者的 actor、kind、role 与 administrator；
- `GET /api/v2/formats`——部署提供的格式档案。

未配置凭据时，身份与格式探测报告 `unauthenticated` 而不是失败，因此 `info`
可以作为登录前的探测命令；token 被拒绝时报告 `unauthorized`。服务器作出任何
应答时命令退出码为 0；不可达时退出码为 8。`--json` 的 data 为 `InfoReport`，
逐来源给出 `status`（`ok`、`not_ready`、`unauthenticated`、`unauthorized`、
`forbidden`、`error`）与细节。
`server` 字段为实际请求端点，移除 userinfo、查询参数与 fragment。失败探针保留
可用的 `code`、`requestId` 与可操作的 `hint`；人类输出显示相同的诊断信息。

### `agctl commands`

打印命令树。`agctl commands --json` 为每条命令返回用法、flag 与契约元数据：
资源对象、场景标签、交付阶段、真实端点、是否改变状态、最低权限，以及分页、
dry-run、重试、wait 和 follow 能力。脚本与 agent 通过该树发现已安装客户端的能力。

### `agctl auth login`

用本地用户名与密码换取会话 token（`POST /auth/login`），并存入显式选择的
profile。密码来自有界 stdin（`--password-stdin`）；缺失、为空或超限都会在发出
任何请求之前以退出码 2 失败。未给出 `--profile`（或 `AGCTL_PROFILE`）时命令
拒绝存储 token，除非用 `--secret-output FILE` 指定一个新的私有文件来存放（与所有
一次性秘密规则相同：请求前检查、0600 权限、从不覆盖、从不打印；`--profile` 与
`--secret-output` 互斥）。客户端从不打印 token。已绑定其他服务器的 profile 会在请求前被拒绝（包括 `--server` 或
`AGCTL_SERVER` 覆盖的情形）；未绑定服务器的 profile 在存 token 时会绑定到本次
实际登录目标，token 之后不会被发往错误部署。服务端报告 `mustChangePassword`
时，命令会存储 token、退出码 0，并指引运行 `agctl auth change-password`；它
绝不自动改密，且该账号在改密前访问管理端点会得到 403。

### `agctl auth logout`

请求服务端退出登录（`POST /auth/logout`），并从所有存储该 token 的 profile
中移除它。该端点对每种凭据都返回 204，但只撤销本地用户会话，因此命令随后会
探测凭据状态（`GET /auth/session`）而不是假定已撤销：`credentialInvalidated`
报告凭据是否仍可认证——机器凭据或静态管理员 token 会报告"仍可认证"，必须从
其各自的管理面吊销。`--local-only` 跳过服务端调用，只遗忘本地凭据。通过
`--token` 或 `AGCTL_TOKEN` 提供的 token 不会从任何地方被移除。

### `agctl auth whoami`

显示调用者身份（`GET /api/v2/identity`）与一页有界大小、可读的仓库
（`GET /api/v2/repositories`，`pageSize=200`）。`--json` 的 data 为
`{actor, kind, role, administrator, oidc, repositories: {count, names,
truncated}}`；当仓库数量超过该页或示例名单时 `truncated` 为 true。缺少凭据时
在发出任何请求之前以退出码 5 失败。

### `agctl auth change-password`

使用本地用户会话修改调用者本人的密码（`POST /auth/change-password`）。两个
密码都来自有界 stdin，各占一行（先是当前密码，再是新密码）。成功后匹配的已
存 token 会被移除，并要求重新执行 `agctl auth login`；被拒绝的修改保留已存
token。该端点只接受本地用户会话。

### `agctl config profile`

无需手工编辑即可管理 `~/.config/agctl/config.yaml`：

- `agctl config profile add NAME --server URL [--token-stdin] [--insecure]`
  创建或更新 profile；不带 `--server` 时已有 profile 保留其 server，
  `--token-stdin` 从 stdin 读取一行有界秘密且永不回显。更换 server 时会丢弃
  已存 token，除非 `--token-stdin` 提供了新目标的凭据——旧部署的 token 绝不会
  被发往新部署；
- `agctl config profile list` 列出所有 profile 及当前选中项；
- `agctl config profile use NAME` 设置 `current_profile`；
- `agctl config profile remove NAME` 删除 profile，并在它正是 `current_profile`
  时将其清空。

Linux 和 macOS 上的写操作通过进程间锁串行化完整的读取、修改和替换，
然后以 0600 权限原子替换文件。稳定的同目录 `config.yaml.lock` 在句柄关闭或
进程退出时释放；等待其他写操作超过十秒后失败，调用方可重试。
已存在的宽松权限文件会被拒绝并提示 `chmod 600`。
profile 和 login 输出会去除 URL 的 userinfo、查询参数与 fragment。

Windows 锁实现已经通过编译检查，但当前 POSIX 权限校验不支持 Windows 私有
profile 持久化：Windows 文件 mode 无法证明所需的 0600 保护。该平台请使用不落盘的
`--server`/`--token` 或 `AGCTL_SERVER`/`AGCTL_TOKEN`；本批次不宣称完成 Windows
profile 运行验收。

### `agctl repo`

通过 `/api/v2/repositories` 管理仓库。仓库按 ID 指定（`agctl repo list` 会显示）。

- `agctl repo list [--page-size N] [--cursor TOKEN]` 读取当前主体可见仓库的
  一页（默认 50，范围 1–200）；空列表是成功，不是无权限。用 `--cursor` 传入
  `nextCursor` 继续读取。`--all` 在 `--max-pages`（默认 20）和一个总体
  `--timeout` 内跟随 cursor；触及预算或第一页之后失败时以退出码 3 结束，
  并给出已读条目、`complete: false` 和可续读的 cursor。
- `agctl repo show ID` 显示一个仓库；代理配置中的凭据只报告“已配置”。
- `agctl repo create NAME --format FORMAT [--type hosted|proxy] [--endpoint
  URL] [--allowed-host HOST]… [--anonymous-read]
  [--maven-strict-publication]` 创建仓库（平台管理员）。每次创建都发送
  `Idempotency-Key`，同一次调用的重试复用它，因此丢失的响应不会造成第二个
  仓库。结果未知（连接失败、5xx）时命令会打印该 key：带
  `--idempotency-key KEY` 重跑会重放原结果，同一 key 搭配不同请求会以退出码
  7 被拒绝。上游凭据、出站代理和 OCI Bearer 交换暂不由本命令提供，请使用
  Console 或 API。
- `agctl repo update ID [--anonymous-read=true|false]
  [--maven-strict-publication=true|false] [--endpoint URL] [--allowed-host
  HOST]…` 只把给出的设置作为条件 `PATCH` 发送。`If-Match` 取
  `--if-match VERSION`，未给出时取更新前刚读到的版本；并发修改会以 412
  （退出码 7）失败且不覆盖任何内容。更新从不自动重试。Hosted 仓库只接受
  匿名读取和 Maven 发布策略，其他设置由服务端拒绝（退出码 1）。
- `agctl repo delete ID --yes [--wait]` 发起异步删除。缺少 `--yes` 时在任何
  请求之前以退出码 2 失败。202 表示已受理而非完成：`data` 为 `{id, status:
  "accepted", repositoryState: null, terminal: false}`。协议访问立即停止，
  每分钟运行一次的删除 Worker 把仓库从 `deleting` 推进到 `deleted`。
  `--wait` 回读仓库直到 `deleted`（`status: "deleted"`、`terminal: true`）；
  此时 `--timeout` 约束整条命令，请预留一分钟以上。超时以退出码 8 结束，
  给出 ID 和最后读到的状态；服务端继续删除，不会重发或取消。已删除的仓库仍可
  读取，作为审计锚点；再次删除返回 404。

`create`、`update`、`delete` 的 `--dry-run` 是客户端计划
（`mode: "client-plan"`）：在本地校验输入，可以读取当前仓库（`update` 读版本，
`delete` 读状态），并打印将发送的请求以及哪些已检查、哪些未检查。它从不提交
写入，因此不能声称服务端一定会接受。

### `agctl group`

通过 `/api/v2/groups` 管理 Group。一个 Group 引用同一格式的多个仓库；在每种
仓库类型内部，成员的配置位置决定解析顺序。Group 按 ID 指定。

平台管理员可以读写所有 Group。对 Group 引用的每个仓库都有管理权限的主体可以
读取它（`show`、`members list`、`resolution`）并在 `list` 中看到它；写入则要求
对变更前后的成员都有管理权限。其他主体得到 404，如同 Group 不存在，因此只管理
部分成员的 Group 对其不可见。

- `agctl group list [--page-size N] [--cursor TOKEN | --all [--max-pages
  N]]` 的分页方式与 `agctl repo list` 相同。
- `agctl group show ID` 显示 Group 及按配置位置排列的成员；
  `agctl group members list ID` 只显示成员（`meta.pagination.mode` 为
  `none`：该端点不分页）。
- `agctl group resolution ID` 显示协议解析器读取成员的顺序（先 hosted 后
  proxy，再按配置位置），附带仓库名称，以及因缺失、未激活或格式不符而排除的
  成员数。它不查询制品，也不代表任何读者的访问决定。
- `agctl group create NAME --format FORMAT --member REPOSITORY_ID…
  [--anonymous-read]` 创建 Group，`--member` 的顺序即配置位置。与
  `repo create` 相同，每次创建都发送 `Idempotency-Key` 并在重试时复用，结果
  未知时打印该 key 供 `--idempotency-key` 使用。
- `agctl group update ID [--name NAME] [--anonymous-read=true|false]` 读取
  Group，带上修改后整体发回（`PUT`），保留格式和成员；`If-Match` 取
  `--if-match VERSION` 或刚读到的版本，412（退出码 7）不覆盖任何内容。
  修改成员请用 `agctl group members set`。
- `agctl group members set ID --member REPOSITORY_ID…` 按给出的顺序替换
  成员，以 `--if-match` 或刚读到的版本为条件。服务端返回更新后的 Group；较旧的
  服务端只返回成员列表，此时会再读取 Group 以报告新版本。若回读失败或返回不完整的 Group，命令报告失败并保留已确认写入的成员
  列表与回读提示，不会重复写入。
- `agctl group delete ID --yes` 删除 Group，成员仓库保留。删除是同步的
  （`data` 为 `{id, deleted: true}`），且从不重试，因为响应丢失后的重复请求
  会返回 404。

`update`、`members set` 和 `delete` 从不自动重试。Group 写操作的
`--dry-run` 与 `agctl repo` 一样是客户端计划。

### 仓库内容与格式视图

内容读取遵循仓库的读取授权：无权读取的仓库返回 403（退出码 6），全局检索则
只覆盖可读仓库，空结果是成功。仓库通过 `--repo` 按 ID 指定。

- `agctl artifact search --repo ID [--q TEXT]` 检索一个仓库；
  `agctl artifact search --global --q TEXT [--format FORMAT]` 检索全部可读仓库，
  并在 `meta.scope.searchedRepositories` 中报告检索了多少仓库。
  `--match prefix|contains` 选择服务端匹配方式（缺省为前缀）。两者都用
  `--page-size`、`--cursor` 和 `--all` 分页。
- `agctl artifact browse --repo ID [--parent NODE_ID]`（也可用
  `agctl repo browse ID`）列出服务端浏览树的一层；节点 ID 不透明、带签名并会
  过期。`agctl group browse ID` 浏览 Maven 或 Raw Group 中可读成员的并集，并在
  `meta.scope` 中给出 Group 及其候选成员。
- `agctl artifact identities --repo ID --purpose scan|distribution` 列出扫描或
  晋升可接受的规范坐标与摘要。该端点没有 cursor：`--limit`（1–200）限定窗口，
  窗口已满时可能还有更多。
- `agctl artifact usage --repo ID [--limit N] [--offset N]` 以偏移窗口显示各资源
  的下载次数，并附带服务端总数。
- `agctl artifact intelligence show` 和 `agctl artifact quarantine show` 读取
  某个 `--coordinate` 与 `--digest` 的记录；404 表示没有记录。
- `agctl oci images|manifests` 和 `agctl maven coordinates` 是格式视图；
  `oci manifests` 需要 `--name IMAGE`，并包括没有标签的 manifest。
- `agctl conan references --repo ID [--q PREFIX]` 与 `conan recipe-revisions
  --repo ID --reference NAME/VERSION/USER/CHANNEL [--q QUERY]` 使用服务端
  游标，沿用检索的 `--all` 页数与时间预算；两个视图都在 `meta.scope` 记录
  本次仓库和筛选范围。
- `agctl conan package-ids --repo ID --reference NAME/VERSION/USER/CHANNEL
  --recipe-revision REVISION` 与 `conan package-revisions`（另需
  `--package-id ID`）一次返回选定范围的完整列表，分页为 `none`，不虚构游标、
  页大小或总数。
- `agctl conan package-revision delete` 必须给出上述精确选择条件、
  `--revision REVISION` 和 `--yes`。`--dry-run` 只读取目标并说明尚未验证的条件。
  成功必须同时取得 HTTP 204 确认与相同 package 的回读列表，且目标修订已不在
  列表中；响应不完整、回读失败或目标仍可见时返回 `outcome: unknown` 和精确
  回读命令。删除需要写权限，回读另需读权限；重复墓碑请求可安全重试。单纯不在
  列表中不能证明已有墓碑，确认或不再可见也不等于物理字节已回收。
- `agctl artifact list|show|delete --repo ID` 针对 Maven Hosted 发布流程记录的
  制品，其他格式返回 404。`list` 一次返回全部可见制品（`meta.pagination.mode`
  为 `none`）。`show` 附带制品的情报和隔离记录，每项报告为 `recorded`、
  `none`、`not_permitted` 或 `unavailable`——未读取到的绝不报告为健康。
  `delete ARTIFACT_ID --repo ID --yes` 为制品打墓碑（202；`serverState` 是服务
  端的删除状态）；重复请求会再次被受理，因此和其他幂等请求一样会重试。
- `agctl repo capabilities ID` 列出 Hosted 仓库所属格式支持的操作。
- `agctl repo connect ID` 按仓库格式打印客户端配置（`.npmrc`、`pip.conf` 与
  `.pypirc`、Maven `settings.xml` 与 `pom.xml`、`GOPROXY` 与 `.netrc`、
  `docker login`、Raw `curl`、Conan remote、APT `sources.list` 与 `auth.conf`、
  Cargo `config.toml`），Hosted 仓库附带发布片段。凭据一律是占位符
  `${ARTIFACT_GATEWAY_TOKEN}`，已配置的 token 绝不写进片段。Maven Hosted 的
  读取与发布使用 `/repository/maven/<repository>/`，Proxy 指引使用
  `/maven/<repository>/`。URL 取 CLI 所连
  服务端，去掉凭据、查询参数和片段。PyPI `.netrc` 使用 pip 匹配的纯主机名，
  索引 URL 仍保留显式端口。

### 审计与运行状态

这些命令只读，不改变任何内容。

- `agctl audit list [--actor A] [--operation O] [--outcome O] [--repository
  R] [--group G] [--format F] [--from TIME] [--to TIME]` 用 cursor 分页读取审计
  日志（`--page-size`、`--cursor`、`--all`）。`--bounded [--limit N]` 改为从
  `GET /audits` 读取最多 500 条的一个窗口，它没有 cursor 也不支持时间范围。
  `--csv` 输出带表头的 RFC 4180 CSV。审计日志记录授权决定、拒绝、授权与模板写入
  和 Group 解析，并非每次管理写入；审计记录带有 `agctl` 失败时报告的请求 ID。
  `--follow` 流式输出新记录：审计记录没有序号，因此每次轮询重读两分钟的回看窗口
  并跳过已投递的记录；不带 `--from` 时从现在开始，带上时先输出 `--from` 以来的
  记录。一次轮询读了十页仍有后续页时，先输出 `gap`，再以 `poll_budget`
  （退出码 3）结束，不投递该轮不完整的数据，也不推进到未读历史之后。
  此时应缩小过滤范围，或用 `audit list --all` 回读历史。`end.resume.from`
  保留包含边界的回看窗口下限，带同样过滤条件恢复时可能重放记录，包括同一
  时间戳的记录，消费者应去重；延迟超过两分钟回看窗口才到达的记录不在此轮询
  保证内。
- `agctl runtime logs [--level L] [--component C] [--request-id ID]
  [--trace-id ID] [--keyword TEXT] [--from TIME] [--to TIME | --window D]`
  从应答进程的内存中读取一页（最新在前，`--limit` 最多 100）：日志只属于一个
  实例和会话，重启即丢失（见 `meta.scope`）。`nextCursor` 配合 `--before`
  翻到更早的条目。`--follow` 先输出 `scope` 事件，保留本地实例、会话、保留范围
  与来源元数据（空流也有），再按时间正序输出这一页，用服务端 cursor 跟随。
  每次请求最多读取剩余事件预算，结束游标不会越过本页尚未投递的条目。
  用 `end.resume.afterCursor` 配合
  `--after-cursor CURSOR --follow --duration D` 和同样过滤条件即可续读；
  `--after-cursor` 不带 follow 时读取一页正序记录。
  cursor 属于一个进程会话，进程重启或换了实例应答时，流以 `scope_changed`
  结束。未读条目被覆盖时报告为 `gap`。
- `agctl status` 探测 `/livez`、`/readyz` 和诊断信息，逐项给出结论：依赖、运行
  节点（附问题代码）、扫描器和失败的队列任务。总体结果取最差的一项——
  `unhealthy`、`degraded`、`unknown`、`healthy`；不健康时退出码 1，加
  `--strict` 时只要不是 healthy 就退出 1。没有凭据或无权读取诊断时，这些检查为
  `unknown`，绝不报告为健康。扫描器不健康或不可达时，普通 `status` 也会失败；
  扫描器数据库过期仍为降级，使用 `--strict` 时失败。
- `agctl diagnostics`、`agctl runtime nodes` 和 `agctl overview show` 打印服务端
  文档（JSON 输出原样透传，新增字段也能到达脚本），并附带人类可读摘要。
  诊断摘要保留各本地挂载的状态、原因、字节数、实际采样时间及共享别名，并说明
  观测范围和单位；缺失值仍为 unknown。
- `agctl lifecycle-job list [--repo ID | --limit N]` 读取一个仓库的最新任务，
  最多 100 条；或默认读取全局最新 500 条的窗口。服务端先截取全局窗口，再过滤
  仓库权限，因此返回不足一页甚至空集合都不能证明完整。两个来源均报告
  `mode: bounded`；全局完整性保持未知，单仓库不足 100 条时才报告完整。
  `agctl lifecycle-job show JOB_ID` 在同一窗口中查找任务；不在窗口中的任务以
  退出码 4 结束，并说明查找的范围。运行、重试和取消任务暂不属于本命令族。

### 授权

授权按行写入，以主体和资源前缀为键；不提供整表替换。仓库通过 `--repo` 按 ID
指定。

- `agctl grant list --repo ID` 输出仓库的授权以及授权集版本（来自 `ETag`），
  该版本是 `template apply` 的条件；`agctl grant list --all-repositories` 列出
  调用者管理的全部仓库上的授权。
- `agctl grant upsert --repo ID --principal P --scope S… [--resource-prefix
  X]` 创建或替换一条授权，不影响其他授权；同一行可以重复发送，因此会重试。响应
  必须包含写入的这一行和新版本，否则结果为未知。`--dry-run` 读取当前这一行，
  报告 `create`、`replace` 或 `unchanged`。
- `agctl grant delete --repo ID --principal P [--resource-prefix X] --yes`
  删除一条授权。不会重试：重复请求返回 404，而这与授权本就不存在时的应答相同。
- `agctl role list|show` 和 `agctl template list|show` 读取目录（平台管理员）。
- `agctl role create NAME --scope SCOPE... [--description TEXT]` 创建命名的
  仓库 scope 集。`agctl role update ID [--name NAME] [--description TEXT]
  [--scope SCOPE...] --yes` 保留未指定字段；指定 scope 时替换整个 scope 集。
  `--description=` 清空描述。
- `agctl template create NAME --grants-file FILE [--description TEXT]` 创建
  授权配方。`agctl template update ID [--name NAME] [--description TEXT]
  [--grants-file FILE] --yes` 保留未指定字段；提供文件时替换配方中的全部授权。
  文件必须是单个 JSON 数组，最多 500 条主体/资源前缀唯一的授权，每条包含不重复
  的有效 scope；拒绝未知字段和 `null`。`[]` 显式清空配方。完整请求须满足服务端
  128 KiB 大小上限。例如：

  ```json
  [{"principal":"service-account:ACCOUNT_ID","scopes":["repositories:read"],"resourcePrefix":"releases/"}]
  ```

- 角色/模板更新先读取完整目录项，以其版本作为 `If-Match`，也可显式传
  `--if-match VERSION`。412（退出码 7）返回拟议的替换内容，并尝试回读最新的
  `data.current` 供审阅；从不重新提交写入。回读失败时，`data.readBack` 指明读取
  命令。`--dry-run` 读取现有目录项，给出前后内容、版本和仅影响目录的范围，不写入。
- `agctl role delete ID --yes` 和 `agctl template delete ID --yes` 删除目录项。
  `--dry-run` 只读取，不删除。现有删除 API 不支持 `If-Match`；读取后目录项仍可能
  改变，删除命令不接受 `--if-match`。六条目录写命令都不自动重试；创建没有幂等键。
  无法确定写入结果时返回 `data.outcome: "unknown"` 和回读命令，操作者审阅后再
  决定是否重复。

目录 CRUD 要求活跃、无需改密的平台管理员；待授权和 member 账号返回 403
（退出码 6）。创建的 dry-run 只验证本地输入，不证明权限或名称可用；更新/删除
的 dry-run 使用受保护的读取，也不证明后续写入必然获准。目录项 ID 是 UUID，
这些命令不编辑内置账号级别 `none`、`member`、`admin`。命名 scope 集不是账号
级别，也不是角色分配。任一目录的创建、编辑或删除都不改变既有仓库授权；应用
模板仍是单独、明确的操作。

- `agctl template apply TEMPLATE_ID --repo ID --yes` 用模板的授权替换仓库的
  全部授权，以 `--if-match` 或刚读到的授权集版本为条件；412（退出码 7）不替换
  任何内容，且从不重试。`--dry-run` 读取授权和模板，列出将新增、删除、变更和
  保留的每一条授权。
- `agctl access check --repo ID [--actor A [--role member|admin]]
  [--resource PATH]` 说明读、写、管理、情报和匿名读取各自的判定来源与原因。
  `--require PERMISSION`（可重复）在任一权限不被允许时退出码 6，供 CI 和
  agent 自检。说明其他主体需要平台管理员；`--role` 模拟该主体的全局角色。
  本人没有读取或情报权限时，服务端以 404（退出码 4）隐藏仓库存在性；拿到判定
  矩阵后，`--require` 发现权限被拒时才退出码 6。
- `agctl access anonymous show` 读取匿名读取的平台开关；
  `agctl access anonymous set --enabled=true|false` 以 `--if-match` 或刚读到的
  版本为条件替换它，从不重试。开启需要 `--yes`，因为它作用于所有允许匿名读取的
  仓库和 Group。

### 最小权限服务账号自检

准备已有的活跃服务账号及其凭据，签发凭据由 #133 单独承接。在环境中设置
`AGCTL_SERVER`、`REPO_ID`、`SERVICE_ACCOUNT_ID`、`ADMIN_TOKEN` 和
`SERVICE_ACCOUNT_TOKEN`；只有管理员写入授权。以下剧本仅允许读取
`releases/` 下的资源，写入仍被拒绝：

```sh
AGCTL_TOKEN="$ADMIN_TOKEN" agctl grant upsert --repo "$REPO_ID" \
  --principal "service-account:$SERVICE_ACCOUNT_ID" \
  --resource-prefix releases/ --scope repositories:read
AGCTL_TOKEN="$SERVICE_ACCOUNT_TOKEN" agctl access check --repo "$REPO_ID" \
  --resource releases/app.txt --require read --json
# 预期退出码 6：已有读取权限可以看见资源，但写入被拒绝。
AGCTL_TOKEN="$SERVICE_ACCOUNT_TOKEN" agctl access check --repo "$REPO_ID" \
  --resource releases/app.txt --require write --json
AGCTL_TOKEN="$ADMIN_TOKEN" agctl grant delete --repo "$REPO_ID" \
  --principal "service-account:$SERVICE_ACCOUNT_ID" \
  --resource-prefix releases/ --yes
# 若这是账号的唯一权限，撤销后预期退出码 4。
AGCTL_TOKEN="$SERVICE_ACCOUNT_TOKEN" agctl access check --repo "$REPO_ID" \
  --resource releases/app.txt --require read --json
```

### 服务账号、凭据与 API key

服务账号是供 CI 使用的机器主体（授权中写作 `service-account:<id>`）。其凭据和
平台 API key 带有一次性秘密：服务端只在签发的那次响应中返回秘密。

- `agctl service-account list` 分页读取账号。`agctl service-account show ID`
  通过读取列表查找账号，因为没有单个账号的端点：只有读完整个列表后才返回 404
  （退出码 4），`--max-pages` 先用完时退出码 3。
- `agctl service-account create NAME [--description TEXT]` 创建账号；不重试
  （端点没有幂等键，重复请求会对已创建的账号返回 409）。
  `agctl service-account update ID [--description TEXT] [--state
  active|disabled]` 以 `--if-match` 或从列表读到的版本为条件；禁用会让该账号的
  全部凭据失效。
- `agctl credential list --service-account ID` 和 `agctl api-key list` 从不
  显示秘密。
- `agctl credential issue --service-account ID --name NAME --secret-output
  FILE [--expires-in DURATION]` 和 `agctl api-key create NAME --secret-output
  FILE [--role member|admin]…` 只把秘密写入 `FILE`。缺少 `--secret-output` 时
  在任何请求之前退出码 2。请求前目的地必须不存在（文件和链接都不会被覆盖），并在
  其目录中创建私有临时文件；响应后秘密以 0600 权限写入、同步，再链接到目的地。
  输出只报告 ID、过期时间和 `secret: {path, mode, delivered}`，`--json` 和
  `--verbose` 下也不含秘密。签发从不重试，因为每次请求都会生成新秘密。秘密签发
  后无法写入时，命令以 `secret_not_delivered` 退出码 1 结束，任何消息中都不含
  秘密，并给出需要吊销的 ID；不会自动重新签发。`--dry-run` 检查目的地且不留下
  任何文件。
- `agctl credential revoke CREDENTIAL_ID --service-account ID --yes` 和
  `agctl api-key revoke ID --yes` 立即让秘密失效。吊销只记录一次时间并再次返回
  同一记录，因此会重试；记录保留在列表中，标为已吊销。

不中断地轮换 CI 凭据：把新凭据签发到新文件并部署，确认可用（例如
`agctl auth whoami`）后吊销旧凭据；此后旧秘密返回 401（退出码 5）。

### 用户与会话

本地用户有全局角色（`none`、`member`、`admin`）；`member` 本身不带任何仓库
权限，仓库权限来自授权（`user:<name>`）。

- `agctl user list [--search TEXT] [--role R] [--state S] [--limit N]
  [--offset N]` 读取带服务端总数的偏移窗口；`agctl user show ID` 读取一个用户。
- `agctl user create NAME --role R (--password-stdin | --password-file FILE)`
  创建用户。密码只来自有界 stdin 或私有普通文件的第一行（不得有组或其他用户
  权限，可用 `chmod 600` 设置），本地校验长度（8 个字符到
  72 字节），从不出现在输出、计划（`--dry-run` 显示占位符）、错误或 verbose
  日志中。创建不重试。
- `agctl user update ID [--role R] [--state S] [--display-name …] [--email …]
  [--description …]` 和 `agctl user reset-password ID (--password-stdin |
  --password-file FILE) [--must-change-password]` 以 `--if-match` 或刚读到的
  版本为条件，都不重试。服务端拒绝降级、禁用或删除最后一个活动管理员（409，
  退出码 7）。
- `agctl user delete ID --yes` 删除用户；用 `--state disabled` 禁用是可撤回的
  替代方式。
- `agctl user sessions list ID [--include-inactive]` 列出登录会话。
  `agctl user sessions revoke ID --session SESSION_ID --yes` 结束一个会话（会
  重试：重复结束无害）；`--all` 结束全部会话，以用户版本为条件且不重试。被结束
  会话的 token 立即返回 401。
- `agctl user identities list|link|unlink ID` 管理已链接的 OIDC 身份；链接需要
  Gateway 已配置该 issuer，解除链接需要 `--yes`。

### 发布

- `agctl publish raw SOURCE --repo ID [--to PATH_OR_PREFIX]` 通过
  `PUT /raw/{repository}/{path}` 把一个文件或整个目录树中的普通文件（不跟随链接）
  上传到 Raw Hosted 仓库。每个对象在本地计算 SHA-256 并作为 `Digest` 头发送，由
  Gateway 校验字节；响应的 ETag 必须对应同一摘要，否则该对象报告为 `unknown`。仓库
  中已有相同摘要的对象标为 `skipped`（通过 `HEAD` 读取；`--force` 仍会上传），因此
  部分失败后重跑是安全的。上传按 `--parallel`（默认 4）并发并从磁盘流式读取，单个
  对象上限 1 GiB。结果逐个列出对象的 `path`、规范 `coordinate`（每段按 Go 的
  `url.PathEscape` 转义，与检索结果一致）、大小、摘要和状态；任一对象 `failed` 或
  `unknown` 时退出码 3。此 CLI 使用无条件替换；原生 Raw HTTP 同时支持条件写入。真实 Raw/Maven 发布前会核对目标身份、活跃 Hosted 格式与服务端声明的 publish 能力。
- `agctl publish raw SOURCE --repo ID --resumable [--chunk-size BYTES]`
  创建原生恢复上传，分块发送并核对 offset（默认 4 MiB，最大 16 MiB），仅以整个文件的
  SHA-256 完成发布。每项返回 `uploadId`、最后确认的 `uploadOffset` 和 `readBack`；
  人类可读表格也显示 ID 和偏移。用 `--upload-id ID` 恢复单个文件，保留原始源文件、仓库
  和目标路径。PATCH 响应丢失后先查询同一上传的状态，再决定是否有限重放。创建和完成
  都不会盲目重发，完成响应丢失时只有最终路径的摘要和大小均一致才确认成功。无法确认的
  进度报告 `unknown`（退出 3），包括不确定 PATCH 后状态查询被拒绝。创建响应丢失且没有
  可用 ID 时仍为未知，不能盲目重新创建。Dry-run 不发送上传请求。本地散列完成后，能力
  查询与整个上传批次共享 `--timeout`；超时或 Ctrl-C 不会取消或删除服务端上传。发现、
  状态及结果回读需要仓库读取权限，上传需要写入权限。
- `agctl publish maven --repo ID --coordinate G:A:V --pom FILE [--file
  FILE]…` 通过发布会话发布一个 Maven 坐标：在 `Idempotency-Key` 下声明每个文件的
  大小和摘要，上传后提交。用同一 `--idempotency-key` 重跑会继续同一个打开的会话；
  已提交的会话不会被重复使用（退出码 7）。各步骤也有独立命令：
  `agctl publish create`、`agctl publish upload SESSION --file …`、
  `agctl publish commit SESSION` 和 `agctl publish status SESSION`。上传按内容寻址
  并会重试；提交不重试（重复提交返回 409），提交响应丢失时回读会话，状态为
  `committed` 即确认成功。
- 其他格式用各自的原生客户端发布；`agctl repo connect` 打印其配置。

### 配额告警与邮件

这些第二批（M2）命令需要平台管理员。

- `agctl email capability` 报告应答进程能否排队邮件（`ready`、`email_disabled`、
  `encryption_key_unavailable`）。ready 只表示已配置，不代表邮件能送达收件箱。
- `agctl email target list|show` 从不显示收件人，只显示 `recipientConfigured`。
  `agctl email target create NAME --locale en|zh-CN (--recipient-stdin |
  --recipient-file FILE) [--enabled]` 只从有界 stdin 或私有（0600）文件读取地址，
  并在包括计划在内的所有输出中脱敏。`agctl email target update ID` 以刚读到的版本
  为条件修改名称、语言、启用状态或收件人；未给出的字段按原值发回，因为该端点把
  省略的 `enabled` 当作停用。规则在更新前继续绑定原目标版本。
- `agctl email preview --scenario S --locale L [--html-output FILE]
  [--text-output FILE]` 渲染固定的合成数据，不入队；两部分必须写入不同文件。
- `agctl email test --target ID --scenario S --yes` 在 `Idempotency-Key`（UUID；
  `--idempotency-key` 可复用，同一 key 返回同一投递，因此会重试）下按目标当前版本
  排队一封真实测试邮件。服务端按 key、目标、目标版本和场景识别重复请求，因此
  结果未知时会报告发送所用的版本，之后的重试用 `--idempotency-key K --if-match VERSION`
  把它带回，而不是读取更新的版本。入队不等于送达：`agctl email delivery wait ID [--timeout D]`
  轮询直到中继接收（退出码 0）或 `dead` 失败（退出码 1、`delivery_failed`，
  `data` 保留实际记录），超时退出码 8 且投递仍在队列中——没有取消操作；
  `commands --json` 明确报告 `cancelSupported: false`。等待只接受目标投递 ID 的
  完整响应作为完成证据。
  `agctl email delivery list [--limit N]` 读取最新的投递（最多 100，无 cursor）；
  `show` 读取一条，包括尝试次数和 `possibleDuplicate`。
  `agctl email delivery replay ID --yes` 以版本为条件重新排队一条失败的投递，
  从不重试。
- `agctl quota-alert list|show|events` 读取规则、其状态及告警、严重、恢复事件和
  投递状态。事件只返回最新 50 条窗口（`meta.pagination.mode: bounded`、
  `limit: 50`），满窗口时完整性未知，也没有 cursor 读取更早历史。
  `agctl quota-alert create --repo ID --target ID` 必须给出全部阈值和
  时长（`--warning-bp`、`--critical-bp`、`--recovery-below-bp`、`--warning-for`、
  `--critical-for`、`--recovery-for`、`--max-sample-age`）；没有默认策略，规则
  默认停用，除非 `--enabled --yes`。`agctl quota-alert update RULE_ID` 以刚读到的
  版本为条件修改阈值、目标或启用状态，未改动的字段按原值发回；启用需要 `--yes`。
  `agctl quota-alert delete RULE_ID --yes` 有条件地删除。
- 503 且错误码为 `email_disabled`、`encryption_key_unavailable` 或 `queue_full`
  仅在此前没有可能已入队的尝试时属于确定拒绝（退出码 1）。此前响应丢失或结果
  不确定时，仍保留未知结果和原始幂等键、目标版本供回读恢复。携带原 key/version
  的恢复调用若在服务端查幂等记录之前被通道状态拒绝，也继续保留未知结果。

### Agent skill

`.agents/skills/agctl/SKILL.md` 向 agent 说明机器契约以及 CI 和管理菜谱。skill
中出现客户端没有的命令、参数或参数个数时，单元测试会失败。

## 验证

- `make cli-test` 离线运行命令入口与 `internal/agctl` 测试，CI 和 `ci-local`
  也调用相同目标；使用进程内的假服务端，覆盖命令
  解析、退出码、脱敏和 JSON 信封。golden 信封位于
  `internal/agctl/testdata/golden/`；有意修改输出后，用
  `go test ./internal/agctl/ -update` 重写并审阅差异。
- `make cli-e2e` 构建 `agctl` 并在真实、隔离的 Gateway 上运行：按
  `GATEWAY_ENV_FILE`（或 `.env`）在空闲端口上启动一个 Compose 项目，结束后
  删除。Docker 自动地址池耗尽时，可用 `CLI_E2E_NETWORK_SUBNET` 为该项目指定
  未占用的 RFC 1918 IPv4 /16 至 /28 网段；Docker 仍拒绝重叠网段。设置
  `CLI_E2E_SERVER` 和 `CLI_E2E_TOKEN`（管理员 token）时改为复用
  正在运行的 Gateway，CI 在 Console E2E 之后就是这样运行的；设置
  `CLI_E2E_RESOLVER_TOKEN` 会加入需要无仓库权限凭据的检查；设置
  `CLI_E2E_OIDC_ISSUER`（Gateway 已配置的 OIDC issuer，会规范化首尾空白和末尾斜杠）会加入身份绑定检查，
  隔离 Compose 模式自行配置合成 issuer，CI 在共享 Gateway 上配置一个。Agent 菜谱
  先用 `commands --json` 和 `info --json` 在同一真实 Gateway 核对 schema、能力与身份。
  角色/模板 CRUD 验证完整替换、412 回读、确认和预演无写入、变更审计，以及真实
  待授权/member/必须改密账号拒绝。隔离栈中最后一个本地管理员的会话会先被吊销
  并拒绝访问，账号随所属 Compose 数据卷销毁；复用 Gateway 且没有既有本地
  管理员时，会明确报告未执行“必须改密的 admin”场景，仍检查必须改密的 member。
  随后创建仓库、
  重放创建、更新（dry-run、真实更新、过期 `If-Match`）并在列表中找到它；
  用它和第二个仓库建 Group（dry-run、重放、未知成员、成员、解析顺序、带过期
  `If-Match` 的改名、重排、保留成员的删除）；创建和更新合成上游的 Maven Proxy
  （不读取上游）、验证能力与只读接入指引、拒绝过期版本并受理删除；通过原生协议发布一个 Raw 文件和
  一个 Maven POM，并检索、浏览、列出、查看和删除它们；检查无权限凭据被拒绝
  （6）、全局检索为空，且该拒绝能按报告的请求 ID 在审计日志中找到；为模拟的
  成员主体写入、检查、按前缀收窄并删除授权（被拒时 access check 退出码 6），在
  审计日志和有界 `--follow` 流中找到授权写入，应用模板（dry-run 差异、过期
  `If-Match`、应用），把匿名开关重设为原值并验证过期版本被拒，以分页和有界流
  读取运行日志，用 `--after-cursor` 续读，并验证并发单行授权保留不同主体的记录。
  隔离 Compose 模式还验证真实非管理员本人权限、模拟和写授权被拒、版本不变且
  没有变更类审计，以及撤销后隐藏仓库。读取 status、诊断、运行节点、概览和生命周期任务；再删除仓库（确认、dry-run、
  202、等待超时）并等待 `deleted`。还会运行 CI 剧本：服务账号只把凭据签发到
  0600 文件，用它读取仓库，轮换后旧秘密返回 401，撤销授权后失去访问；创建并吊销
  API key。新凭据只获得另一个 Maven 仓库的 read/write，创建并重放会话、上传和
  提交 POM/JAR、检索坐标，并通过原生端点逐字节回读两份文件；回读可按 actor/operation
  在审计中查到。撤销授权后新建会话返回 403，已提交制品仍保留。待授权用户升为
  member 后在授权前仍无仓库权限，其会话被结束后被拒，
  从文件重置密码，最后删除用户，上述每次变更（创建、角色更新、会话列出与吊销、
  重置密码、删除）都以管理员为 actor 记录在审计日志的 `users/<id>` 下；该用户
  为 member 时，其会话管理账号与身份均被拒绝（6）。为该用户链接一个 OIDC 身份（先 dry-run）并列出；同一 subject 链接到
  另一用户或再次链接到同一用户均为冲突（7），非已配置 issuer 被拒绝；解除绑定
  （`--yes`，先 dry-run）后再次解除返回 404（4），释放的 subject 可链接到另一用户，
  两次变更都能在审计日志中找到；任何输出中都不得出现 token 或密码。发布部分上传
  Raw 目录树（dry-run、上传、重跑时跳过、只重传改动的文件、逐字节回读、检索中的
  规范坐标），并通过会话发布两个 Maven 坐标（一步完成与分步执行，已提交会话被拒，
  重复提交返回 409）；用户用 `auth login --secret-output` 登录并使用该 token。同时检查错误
  token（5）、仓库不存在（4）、非法输入（2）和冲突（7）的退出码。
- `make cli-runtime-e2e` 始终新建自有 Gateway、PostgreSQL 与 RustFS，使用合成凭据
  和临时回环端口。重启该 Gateway 后核对日志会话变化，以及旧游标的有界
  `scope_changed` 结果；只停止自有 PostgreSQL，核对 unhealthy 状态与 not-ready
  info，恢复后重验就绪；只停用自有日志缓冲，确认返回 `503/log_buffer_unavailable`，
  不把不可用伪装成成功空列表。member、待审批和强制改密用户仍被拒绝；member
  读不到的诊断保留 unknown，`status --strict` 返回失败。CI 和 `ci-local` 调用
  同一目标，不复用已有开发栈。
- `make email-compose-check` 在带自有 TLS SMTP 的隔离 Gateway 上运行邮件命令：
  使用真实 PostgreSQL，验证目标/规则默认停用、明确启用、CAS、固定目标版本、
  所有写命令的 dry-run、私有收件人输入，以及两个输出流均不含合成秘密。
  真实 member 与强制改密管理员会在 16 个管理操作及 delivery wait 中被拒绝；
  缺通道/key、无 sender 的 pending 与有界 wait、test/replay 已提交但 HTTP 响应
  丢失、同 key 恢复及 SMTP 最终确认丢失均保留准确机器结果。真实 scheduler 按
  现有 15 秒节奏驱动 warning → critical → resolved；停止至超过 30 秒采样年龄
  策略后暴露 stale，且不会假称恢复。SMTP 未知确认后的重试会实际产生重复 MIME，
  幂等队列行不保证恰好一次投递。需要源码哈希、JSON/退出码和自有资源清理证据时，
  使用 `python3 scripts/email-compose-check.py --evidence-dir output/email-check-1`，
  输出目录必须尚不存在。夹具不读取 `.env`、不使用真实中继或已有开发栈。
- `make cli-contract-check` 校验命令映射表覆盖全部契约操作，且每个标为
  `implemented` 的行都对应声明了该行端点的已注册命令。

## 安全说明

- 凭据不会进入任何输出：verbose 日志、错误消息与 JSON 信封均不例外；
  `Authorization` 头永不打印。
- 认证失败使用标准 HTTP 说明；非结构化错误正文不回显，结构化 problem 消息脱敏。
- HTTP 重定向直接报告，不自动跟随，避免凭据被转发到其他端点或明文连接。
- 服务端 `Retry-After` 要求的等待时间不会缩短；剩余预算不足时，请求超时取消等待。
- 配置文件权限必须为 0600；权限过宽时命令直接拒绝。
- `agctl auth login` 只把会话 token 存入显式选择的 profile，否则在请求前拒绝；
  密码与 token 来自有界 stdin，不会被 `--json`、verbose 输出或错误渲染回显，
  并注册进失败脱敏。
- `--insecure` 关闭 TLS 证书校验，仅用于受信测试网络；优先使用有效证书。
- 一次性秘密（服务账号凭据和 API key）只写入 `--secret-output` 指定的新私有
  文件；缺少该参数时命令在请求前拒绝。它们从不进入 stdout、stderr、JSON、verbose
  输出、计划或错误文本（见 [ADR 0007](adr/0007-agctl-cli-shape.zh-CN.md)）。
  密码只来自有界 stdin 或文件，同样被脱敏。
