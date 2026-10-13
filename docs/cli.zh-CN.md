# agctl 命令行

[English](cli.md) · [文档索引](README.zh-CN.md)

`agctl` 是面向 CI 流水线、平台管理与 agent 的网关命令行客户端。它用 bearer
凭据直接对 `/api/v2` 下的管理 API 编程。当前可用命令为 `version`、`info` 与
`commands`；其余命令面按可独立评审的切片陆续交付，只有实现后才会出现在
命令树中。客户端形态与机器契约记录在
[ADR 0007](adr/0007-agctl-cli-shape.zh-CN.md)。

## 安装

`agctl` 是单一客户端二进制，版本跟随 Release 在 `VERSION` 文件中固定的值；CI
流水线、平台管理与 agent 运行同一份构建。它不携带任何服务端配置或秘密：连接设置
来自 flag、环境变量或下文介绍的 profile 文件。

### Release 附件

每个 Release 的 tag 为 `v` 加上 `VERSION` 文件的值，Release 在 Gateway 附件旁
发布各平台的客户端附件：

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

`agctl version` 输出客户端版本与 revision，与 Release tag 核对后再使用。当前最新
已发布版本为 `v0.8.1`，早于本 CLI，因此第一个包含这些附件的 Release 是从
`main` 切出的下一个 tag。

### Go module 安装

```sh
go install github.com/artifact-gateway/artifact-gateway/cmd/agctl@<tag>
```

`<tag>` 是包含 `cmd/agctl` 的 Release tag（`v<VERSION>`），路径是 `go.mod`
声明的 module path。该通道要求两者都可公开解析，目前尚不满足：最新 tag
`v0.8.1` 早于本 CLI；`github.com/artifact-gateway/artifact-gateway` 没有对应
仓库（`repository not found`）；改用仓库路径 `github.com/ccsert/artifact-gateway`
请求时，会因 tagged `go.mod` 声明的是另一个路径而报 module path 不匹配。上面的
命令是该通道的目标形态，不是当前可用的安装方式：当前可用的是 Release 附件与下文
本地构建，待包含 CLI 的 tag 在声明的 module path 上发布后该通道才生效。

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
  `partial_failure` 或 `internal_error`。
- 未到达服务端的失败，`error.httpStatus` 为 null。
- 列表统一为 `{"items": [...], "nextCursor": null}`；数据来源没有服务端
  分页时 `nextCursor` 为 null，`meta.pagination.mode` 说明真实模式（本地
  命令树为 `none`）。

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
dry-run、重试、wait 能力。脚本与 agent 通过该树发现已安装客户端的能力。

### `agctl auth login`

用本地用户名与密码换取会话 token（`POST /auth/login`），并存入显式选择的
profile。密码来自有界 stdin（`--password-stdin`）；缺失、为空或超限都会在发出
任何请求之前以退出码 2 失败。未给出 `--profile`（或 `AGCTL_PROFILE`）时命令
拒绝存储 token：安全的 `--secret-output` 目的地尚不存在，且客户端从不打印
token。已绑定其他服务器的 profile 会在请求前被拒绝（包括 `--server` 或
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
  `--token-stdin` 从 stdin 读取一行有界秘密且永不回显；
- `agctl config profile list` 列出所有 profile 及当前选中项；
- `agctl config profile use NAME` 设置 `current_profile`；
- `agctl config profile remove NAME` 删除 profile，并在它正是 `current_profile`
  时将其清空。

写入以 0600 权限原子替换文件；已存在的宽松权限文件会被拒绝并提示
`chmod 600`。

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
- 一次性秘密（签发的 API key、token）由服务账号命令的承接工作治理：没有显式
  安全目的地时，它们不会写入 stdout、stderr 或历史（见
  [ADR 0007](adr/0007-agctl-cli-shape.zh-CN.md)）。
