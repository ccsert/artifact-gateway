# OIDC 浏览器单点登录

[English](oidc-sso.md) | [文档索引](README.zh-CN.md)

Artifact Gateway 支持两条 OIDC 凭证路径：CI/API 的 Bearer 校验，以及浏览器 Authorization Code + PKCE 登录。Console 在运行时读取 `GET /auth/oidc/config`，provider URL 与 client ID 不编译进前端 bundle。

## 必需配置

```dotenv
GATEWAY_OIDC_ISSUER=https://login.example.com/realms/acme
GATEWAY_OIDC_AUDIENCE=artifact-gateway-api
GATEWAY_OIDC_CLIENT_ID=artifact-gateway-console
GATEWAY_OIDC_CLIENT_SECRET=
GATEWAY_OIDC_REDIRECT_URL=https://gateway.example.com/auth/oidc/callback
GATEWAY_OIDC_SCOPES=openid profile email
GATEWAY_SETTINGS_ENCRYPTION_KEY=<32-byte-key>
```

机密 client 配置 secret；要求 PKCE 的 public client 留空。Issuer/JWKS 和 redirect 必须 HTTPS，本地 `localhost`、`127.0.0.1`、`::1` 例外。API-only 可同时留空 client ID 与 redirect；启用浏览器登录时两者必须一起配置。

环境变量是 bootstrap 来源。管理员可在 Console Authentication 保存运行时配置；首次创建 singleton，之后使用 `If-Match`。设置加密 key 以 AES-256-GCM 加密 client secret，响应只返回 `clientSecretConfigured`。

Discovery 的 `issuer`、`authorization_endpoint`、`token_endpoint` 必须匹配。Gateway 不存储 provider access/refresh token。验证 ID token 的 issuer、audience、signature、expiry、state、nonce 后，创建最多 12 小时的 HttpOnly `SameSite=Lax` session cookie。

链接本地账户时，cookie 包含随机 session ID，数据库只保存 ID、账户、登录类型、有界客户端地址/UA 和时间，不保存 cookie 或 provider token。管理员可查看并撤销单个 session；`POST /auth/logout` 先撤销当前链接 session 再清 cookie。

API Bearer 使用 `GATEWAY_OIDC_AUDIENCE`，浏览器 ID token 独立使用 `GATEWAY_OIDC_CLIENT_ID`，因此可配置不同 audience。

## 本地账户链接

验证后身份按规范化 issuer 和稳定 `sub` 绑定本地账户。管理员可从 Users 管理。Authentication 设置也可启用默认关闭的 JIT 创建、选择默认角色，并选择通过唯一且 `email_verified=true` 的 email 关联已有账户。

链接后，Bearer 和浏览器 session 使用本地账户当前角色和安全状态；禁用账户、要求改密或提升 session version 会在下一请求生效。浏览器还要求服务器端 session 活跃且未过期。JIT 关闭时，未链接身份保持外部 principal 与无状态浏览器 session。

### 先登记，再授权

若要让 SSO 登录的同事出现在 Gateway“用户”页面，同时避免登录即获得仓库权限：

1. 在“认证设置”中将“未绑定身份”设为“首次登录自动创建”，将“JIT 默认角色”设为“none · 待授权”。除非明确需要合并已有账户，否则保持“已验证邮箱自动关联”关闭。
2. 请同事重新登录。Gateway 按身份提供方的 issuer 和稳定 subject 创建一条本地用户及 OIDC 绑定；该账户没有本地密码，在“用户”页面显示为“待授权”。
3. 管理员在“用户”页面将其角色改为 `reader`、`writer` 或 `admin`。同事可在等待页面点击“检查授权状态”。需要撤销权限时将角色改回“待授权”；停用账户则会阻止其继续登录。

`none` 角色会明确拒绝仓库读取、写入、管理和智能分析，即使旧的默认读取规则或仓库授权记录原本允许访问。管理员 subject 白名单或明确配置的 OIDC 角色映射可以在首次登记时授予更高角色。后续登录只更新身份资料，不会覆盖管理员在 Gateway 分配的角色；已有绑定账户的角色和状态保持不变。

`reader`、`writer`、`admin` 都是 **全局角色**，授予后会作用于所有仓库。按仓库细分授权属于另一项设计。

## Keycloak

创建 OIDC client，启用 Standard Flow 并注册准确 callback URL。浏览器 ID token 的 `aud` 必须包含 client ID；API Bearer 可使用独立 API audience。

Realm role、client role、顶层 `roles` 和 `groups` 可映射为 Gateway role：

```dotenv
GATEWAY_OIDC_READER_ROLES=artifact-reader
GATEWAY_OIDC_WRITER_ROLES=artifact-writer
GATEWAY_OIDC_ADMIN_ROLES=artifact-admin
```

取匹配到的最高角色。

## GitLab

注册带准确 callback URL 的 OAuth/OIDC application，使用 GitLab issuer 和 application ID。需要角色映射时，把相关 group claim 加入 ID token，并用相同 reader/writer/admin 变量映射其准确值。

## 运维检查

`gateway preflight` 报告 `oidc_enabled`、`oidc_browser_login_enabled` 和设置加密 key 状态。Console Authentication 可测试保存的 discovery。当前节点保存后立即生效，其他 API 节点在有界 settings cache 窗口后看到新版本，无需重启 Gateway 或重建 Console。

本地 Kubernetes 的真实 Keycloak callback 验收见 [Keycloak Kubernetes 验收](oidc-keycloak-k8s.zh-CN.md)。
