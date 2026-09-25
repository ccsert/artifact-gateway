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
3. 管理员在“用户”页面将其角色改为 `member`、`reader`、`writer` 或 `admin`。选择 `member` 表示批准账户但不附带任何仓库权限，之后按仓库逐项授权。同事可在等待页面点击“检查授权状态”。需要撤销权限时将角色改回“待授权”；停用账户则会阻止其继续登录。

`none` 角色会明确拒绝仓库读取、写入、管理和智能分析，即使旧的默认读取规则或仓库授权记录原本允许访问。管理员 subject 白名单或明确配置的 OIDC 角色映射可以在首次登记时授予更高角色。后续登录只更新身份资料，不会覆盖管理员在 Gateway 分配的角色；已有绑定账户的角色和状态保持不变。

`reader`、`writer`、`admin` 都是 **全局角色**，授予后会作用于所有仓库。`member` 相反：它批准账户但不附带任何仓库能力，可访问范围完全来自按仓库授权。将其余全局角色收敛为按仓库授权属于另一项设计。

授权后，`reader` 和 `writer` 可在 Console 的“仓库”页面查看其有读取权限的仓库及制品。`writer` 还可在有写入权限的托管仓库使用 Raw 文件上传、Maven 发布向导，以及 OCI、npm、PyPI 发布指引；OCI、Conan、Go、APT 等格式继续使用各自的原生协议客户端发布。当前 OCI 仓库的 Docker/Podman 发布需要管理员签发具备仓库写入授权的服务账号凭据，通过安全渠道交给发布者；浏览器 SSO 会话不能用作 Docker 凭据。`reader` 看不到上传或发布入口。创建仓库、修改配置、访问授权、用户管理等治理操作仍需 `admin`。

## Keycloak

创建 OIDC client，启用 Standard Flow 并注册准确 callback URL。浏览器 ID token 的 `aud` 必须包含 client ID；API Bearer 可使用独立 API audience。

Realm role、client role、顶层 `roles` 和 `groups` 可映射为 Gateway 账号等级：

```dotenv
GATEWAY_OIDC_MEMBER_ROLES=artifact-member
GATEWAY_OIDC_ADMIN_ROLES=artifact-admin
```

映射的作用是**审批账号**而非授予仓库能力：命中管理员映射即成为管理员，命中成员映射则通过审批但不附带任何仓库权限，仓库访问来自按仓库授权。两者同时命中时管理员优先。

## GitLab

注册带准确 callback URL 的 OAuth/OIDC application，使用 GitLab issuer 和 application ID。需要角色映射时，把相关 group claim 加入 ID token，并用相同的 member/admin 变量映射其准确值。

## 升级与回滚

保存的 OIDC 设置收敛为单份 member 角色列表：reader 列表与 writer 列表合并进来，已存储的
JIT 默认值 `reader`/`writer` 变为 `member`，`GATEWAY_OIDC_READER_ROLES` 与
`GATEWAY_OIDC_WRITER_ROLES` 由 `GATEWAY_OIDC_MEMBER_ROLES` 取代。原先列在任一旧变量里的
外部 realm 角色，必须在新的变量中重新列出才会继续匹配。

该迁移替换了 reader 与 writer 两列而非将其保留，因此"仅回滚应用"——即用旧镜像对接已迁移的
schema——无法读取已保存的设置。回滚方式与 APT lifecycle 迁移相同：恢复升级前的数据库并换回
匹配的二进制。

## 运维检查

`gateway preflight` 报告 `oidc_enabled`、`oidc_browser_login_enabled` 和设置加密 key 状态。Console Authentication 可测试保存的 discovery。当前节点保存后立即生效，其他 API 节点在有界 settings cache 窗口后看到新版本，无需重启 Gateway 或重建 Console。

本地 Kubernetes 的真实 Keycloak callback 验收见 [Keycloak Kubernetes 验收](oidc-keycloak-k8s.zh-CN.md)。
