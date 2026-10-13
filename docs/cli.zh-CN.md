# agctl 命令行

[English](cli.md) · [文档索引](README.zh-CN.md)

`agctl` 是面向 CI 流水线、平台管理与 agent 的网关命令行客户端。它用 bearer
凭据直接对 `/api/v2` 下的管理 API 编程。当前可用命令为 `version`、`info` 与
`commands`；其余命令面按可独立评审的切片陆续交付，只有实现后才会出现在
命令树中。客户端形态与机器契约记录在
[ADR 0007](adr/0007-agctl-cli-shape.zh-CN.md)。

## 构建

```sh
make cli-build          # 输出 dist/agctl
dist/agctl version
dist/agctl info --server https://gateway.example.com --token "$GATEWAY_TOKEN"
```

构建使用 `go.mod` 声明版本对应的本地 Go 工具链。正式分发与多平台二进制另行
跟踪。

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
profile，最后是只定义一个 profile 的文件。

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
等价。`--dry-run` 与 `--yes` 作用于写命令；当前可用命令均为只读，接受但忽略
这两个 flag。

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

## 安全说明

- 凭据不会进入任何输出：verbose 日志、错误消息与 JSON 信封均不例外；
  `Authorization` 头永不打印。
- 认证失败使用标准 HTTP 说明；非结构化错误正文不回显，结构化 problem 消息脱敏。
- HTTP 重定向直接报告，不自动跟随，避免凭据被转发到其他端点或明文连接。
- 服务端 `Retry-After` 要求的等待时间不会缩短；剩余预算不足时，请求超时取消等待。
- 配置文件权限必须为 0600；权限过宽时命令直接拒绝。
- `--insecure` 关闭 TLS 证书校验，仅用于受信测试网络；优先使用有效证书。
- 一次性秘密（签发的 API key、token）由服务账号命令的承接工作治理：没有显式
  安全目的地时，它们不会写入 stdout、stderr 或历史（见
  [ADR 0007](adr/0007-agctl-cli-shape.zh-CN.md)）。
