# Keycloak Kubernetes 验收

[English](oidc-keycloak-k8s.md) · [文档索引](README.zh-CN.md)

`scripts/oidc-keycloak-k8s-e2e.sh` 会在当前 Kubernetes Context 中启动隔离的真实 Keycloak
fixture，同时部署临时 PostgreSQL、RustFS 和 Gateway，执行全部迁移，将 Gateway 与
Keycloak 转发到本机，启动 Console，并运行浏览器 SSO 测试。

```sh
./scripts/oidc-keycloak-k8s-e2e.sh
```

该测试覆盖完整浏览器流程：Discovery、带 PKCE 的 Authorization Code、`state` 与
`nonce` 校验、Code Exchange、ID Token 签名与角色映射校验、HttpOnly Gateway Session
Cookie 签发以及退出失效。浏览器登录前，脚本还会把 fixture 从环境变量引导迁移到
版本化数据库配置 API，并通过运行时配置验证 Provider Discovery。

fixture 使用非生产 Keycloak Realm 和固定测试账号，测试结束后自动删除。排查失败部署时
设置 `OIDC_TEST_KEEP_NAMESPACE=1`。

脚本要求 Kubernetes 与 Docker 共享本地镜像存储
（Docker Desktop 满足），并需要空闲端口 `18080`、`8081` 与 `4173`。可以分别通过
`OIDC_TEST_GATEWAY_PORT`、`OIDC_TEST_KEYCLOAK_PORT` 和 `OIDC_TEST_CONSOLE_PORT` 覆盖。

脚本会同步更新测试 Keycloak Issuer、浏览器断言、Sidecar Listener 和 Kubernetes Service
Target Port。

生产 Issuer 和 JWKS Endpoint 必须使用 HTTPS。HTTP 只允许 `localhost`、`127.0.0.1`
和 `::1` 回环主机，仅用于本地 fixture，不允许远程不安全 Provider。
