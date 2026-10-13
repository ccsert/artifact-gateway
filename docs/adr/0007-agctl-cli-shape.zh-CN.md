# agctl：单一客户端二进制及其横切契约

[English](0007-agctl-cli-shape.md) · [文档索引](../README.zh-CN.md)

状态：已接受

## 背景

远程平台管理此前没有一等公民的客户端契约：

- `scripts/` 下的 curl 脚本各自拼 URL、处理 token、自定义退出码，没有共享的
  错误模型；
- `gateway` 子命令（`version`、`backup`、`snapshot-import`、`preflight`、
  `evidence`、`theme`、`apt-snapshot`）的定位是贴着服务端进程运行，而不是访问
  远程部署；
- Console 面向浏览器交互，会话存放在浏览器存储里，脚本与 agent 缺少稳定的
  调用面。

管理契约本身已经完整——`/api/v2` 的 OpenAPI 源已发布，服务账号凭据已经是
CI 的正规路径——缺的是客户端，不是新的服务端能力。

## 备选方案

### 1. 在 `gateway` 二进制里增加远程客户端子命令

否决。客户端不应加载服务端配置或携带服务端依赖；把本地运维命令（以服务端
权限和存储运行）与远程 bearer token 客户端混在一个二进制里，会让两者的安全
模型互相污染。

### 2. 采用第三方命令框架（cobra）

评估过。标准库 `flag` 与仓库先例（`internal/evidence/cli.go`）一致且零新增
依赖；代价是 help 需要手写，补全要等到后续。决定：骨架用标准库 `flag`。仅当
help/补全的维护成本超过框架自重时才重新评估，并把变更记录在本文件。

### 3. MCP 桥或第二套管理 CLI

否决。网关本身讲 REST；CLI 就是 agent 接口。再造桥或平行的管理命令树只会
复制认证、分页与退出码语义，不带来新的观测者。

### 4. 带 undo/redo 的 REPL

部分否决。写操作直接落在服务端，客户端 undo 语义不成立；回滚走服务端自己的
恢复端点。交互式 shell（上下文、历史、补全，不含 undo）留在后续命令范围内。

## 决定

**一个二进制，一份契约。** 独立的 `cmd/agctl` 二进制是 CI、平台管理与 agent
共用的唯一客户端。它直接对已发布的 `/api/v2` 契约编程，复用管理类型的生成
产物（`internal/admin/openapi`）；绝不加载 `internal/config` 或服务端依赖。

**配置优先级。** 显式 flag > 环境变量（`AGCTL_SERVER`、`AGCTL_TOKEN`、
`AGCTL_PROFILE`）> `~/.config/agctl/config.yaml`（0600，多 profile）中选中的
profile > 用法错误（退出码 2）。Profile 选择顺序为 `--profile`、
`AGCTL_PROFILE`、`current_profile`、`default` profile、只含一个 profile 的
文件。

**退出码即契约。** 0 成功；1 通用失败；2 用法错误；3 批量部分成功；4 未找到
（404）；5 未认证或缺凭据（401）；6 无权限（403）；7 冲突或前置条件
（409/412）；8 连接或超时。缺少必需输入时命令在任何请求发出之前以退出码 2
失败，且从不交互提问。

**唯一 JSON 信封。** `--json`（或 `-o json`）恰好打印一个文档：
`{schemaVersion, ok, data, error, meta}`。`error` 含
`{code, httpStatus, message, hint, requestId}`；列表为
`{items, nextCursor}`，来源没有服务端分页时 cursor 为 null，`meta.pagination`
说明真实模式。`schemaVersion` 从 1 起，仅在破坏性变更时递增。人类输出走
stdout，诊断走 stderr；`--verbose` 打印请求方法与 URL，敏感值做脱敏——token
不会进入任何输出。

**客户端行为。** 管理 API 之上的薄封装：bearer 认证、一个整体超时、仅对幂等
请求在传输失败、429 与 502/503/504 时重试，指数退避并尊重 `Retry-After`。
写请求没有端点声明的幂等键时不重试。服务端的 problem 文档被解析进错误信封，
request id 随错误透出以便支持排查。

**自省是一等功能。** `agctl version`、`agctl info`（健康、就绪、调用者身份、
格式目录）与 `agctl commands --json`（命令树，逐命令记录资源对象、场景标签、
阶段、端点与写/dry-run/重试/wait 能力）让脚本与 agent 可以发现 CLI 能力。

**秘密处理边界。** 一次性秘密（签发的 token、API key）由服务账号命令的承接
单治理：没有显式安全目的地时，它们不进入 stdout、JSON、stderr、历史或重试
缓存。本骨架为它已经输出的所有内容确立脱敏规则。

## 影响

CLI 骨架交付 `version`、`info`、`commands` 三个命令；其余命令面按可独立合并
的切片陆续到达，只有实现了才会出现在 `agctl commands --json` 中。分发
（多平台二进制、`go install`、容器镜像）与 OpenAPI→命令的契约漂移守卫是各自
独立的单；在它们落地前，`make cli-build` 是受支持的本地构建方式。退出码与
JSON 信封是机器契约——后续命令只能扩展信封，不得挪用退出码。框架选型是唯一
暂定项；其余变更必须通过本文件的新决定，而不是静默修改。
