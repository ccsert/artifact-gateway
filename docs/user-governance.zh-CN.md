# 本地用户治理

[English](user-governance.md) | [文档索引](README.zh-CN.md)

Artifact Gateway 本地用户是由管理员管理、用于 Console 与管理 API 的账户，独立于 API Key、break-glass 静态 Token 和 OIDC 身份。

## 账户模型

每个账户拥有不可变 username，以及可变 display name、email、description、global role 和 active/disabled 状态。Username 大小写不敏感且唯一。安全状态包含最近成功登录/改密时间、连续失败次数与可选锁定截止时间、是否强制下次改密、单调递增 session version，以及每个本地或 OIDC session 的有界服务端元数据。

密码只保存哈希。改密和撤销全部 session 会提升 session version，使此前本地与链接 OIDC session 失效。每次登录还创建随机 session ID；记录只含用户、登录类型、客户端元数据和时间，不存 Bearer 或 provider token。因此管理员可撤销一个客户端而不影响其他 session。

被要求改密的用户只能调用 `POST /auth/change-password`，成功前不授予管理角色。本地密码至少 8 个字符、最多 72 字节；上限显式执行，因为 bcrypt 无法安全处理更长输入。

## 管理 API

所有 `/api/v2/users` 操作都要求管理员：

| 操作 | 行为 |
| --- | --- |
| `GET /users` | 按 username/display name/email 服务端搜索，按 role/state 过滤并 offset 分页 |
| `POST /users` | 创建档案、角色、初始密码和可选强制改密 |
| `PATCH /users/{userId}` | 用 `If-Match` 更新档案、角色或状态 |
| `POST /users/{userId}/password` | 重置密码、可要求再次修改并撤销 session |
| `POST /users/{userId}/sessions:revoke` | 用 `If-Match` 撤销全部本地与链接 OIDC session |
| `GET /users/{userId}/sessions` | 列出活跃 session；`includeInactive=true` 包含保留的已撤销/过期历史 |
| `DELETE /users/{userId}/sessions/{sessionId}` | 只撤销一个 session |
| `GET/POST /users/{userId}/identities` | 列出或绑定 provider issuer/subject |
| `DELETE /users/{userId}/identities/{identityId}` | 移除外部身份链接 |
| `DELETE /users/{userId}` | 永久删除账户 |

最后一个 active administrator 不得禁用、降级或删除。乐观并发失败返回 `412`，最后管理员保护返回 `409`。

## 外部身份

OIDC 身份独立存储，以规范化 issuer 和稳定 `sub` 关联本地账户。链接登录使用账户当前角色、状态、强制改密限制和 session version；禁用账户或撤销全部 session 会在下一认证请求使浏览器 session 失效。

管理员可显式关联/取消关联。OIDC 设置也可启用 JIT、选择默认角色并允许 `email_verified=true` 的唯一 email 关联；歧义匹配被拒绝。JIT 关闭时，未链接 subject 保持旧外部 principal，不静默绑定账户。

## 锁定策略

`GATEWAY_LOCAL_AUTH_MAX_FAILED_ATTEMPTS` 默认 5，范围 1-100；`GATEWAY_LOCAL_AUTH_LOCKOUT_DURATION` 默认 15m，范围 1 分钟至 24 小时。

锁定、禁用、不存在和密码错误返回相同登录错误，防止枚举。成功登录清除失败次数和截止时间；管理员重置密码也会清除。

## 审计与运维

成功/失败登录、自助改密、创建/更新/删除、管理员重置及 session 撤销均审计。Actor 是执行管理员，resource 是目标用户；列表与单 session 撤销使用 `user.session.list`、`user.session.revoke`。

过期 session 元数据保留 30 天，由 scheduler 每批最多 500 行删除。PostgreSQL 使用 locked/skip-locked batch，避免多个调度节点争抢。记录过期独立于签名 Token expiry、账户状态和 session version。

策略变更后验证：失败次数达到阈值会锁定；重置使旧 Token 失效；全部撤销使全部当前 Token 失效；单个撤销不影响其他 session；最后管理员不可禁用/删除；审计区分执行者和目标。

## 当前限制

删除永久而非可恢复墓碑；账户只有一个全局角色，尚无自定义角色和多角色；未实现密码过期或可配置复杂度；不支持 OIDC back-channel 或 IdP 发起登出。

未链接外部 principal 使用无状态浏览器 session，不在本地 session 清单。引入服务端 session ID 前签发的 session 在正常过期前保持兼容，但仍服从账户状态和 session version。
