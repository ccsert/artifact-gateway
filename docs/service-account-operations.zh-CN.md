# Service Account 运维

[English](service-account-operations.md) | [文档索引](README.zh-CN.md)

Service Account 是 CI 和外部应用的稳定非人类身份。Repository Grant 绑定 `service-account:<id>`，一个或多个短期 credential 认证为同一 principal，因此轮换凭证无需修改 Grant。

Jenkins、GitLab CI、发布自动化、扫描器等需要跨轮换保持可审计身份的应用应使用 Service Account。需要全局角色的有界管理客户端才使用独立 API Key；无人值守自动化不得创建人类 User。

## 安全模型

- Service Account 没有全局角色，只能访问明确授予其 principal 的 Repository。
- 凭证明文只返回一次，Gateway 只存 verifier。
- 凭证最长 365 天，可单独撤销。
- 禁用账号会在下一请求拒绝其全部直接凭证与短期 OCI principal token。
- 撤销旧凭证不影响重叠的新凭证；用旧凭证换取的 OCI token 最多再存活五分钟。
- 管理审计记录账户/凭证生命周期，但不包含明文 Token。

凭证必须保存在 CI Secret Store。禁止写入 Jenkinsfile、提交到 Git 的 npm/Maven 配置、镜像、shell trace 或发布记录。

## 创建与授权

在 **Management → Service Accounts** 创建账号并签发首个凭证，立即复制 Token，之后无法取回。

打开目标 Repository 的 **Access Grants**，选择 Service Account，并授予最小 scope 与 resource prefix。存储的 principal 形如：

```text
service-account:2f8b5e9d-7f52-4e54-956a-c82860f3ae67
```

启用流水线前可用 Console Access Control evaluator 验证同一 principal。

## 客户端配置

凭证支持 Bearer 和原生客户端 HTTP Basic。客户端需要用户名时使用 `jenkins` 等非空说明标签；授权始终基于稳定 Service Account principal，而非该标签。

Maven `settings.xml`：

```xml
<server>
  <id>artifact-gateway</id>
  <username>jenkins</username>
  <password>${env.ARTIFACT_GATEWAY_TOKEN}</password>
</server>
```

npm 在运行时注入：

```ini
registry=https://gateway.example.com/npm/npm-group/
//gateway.example.com/npm/npm-group/:_authToken=${ARTIFACT_GATEWAY_TOKEN}
always-auth=true
```

OCI 使用标准 Registry token 流：

```sh
printf '%s' "$ARTIFACT_GATEWAY_TOKEN" |
  docker login gateway.example.com --username jenkins --password-stdin
```

PyPI 可使用任意非空 username 和该 credential 作为 password；优先生成临时配置，任务结束后删除。

## 零停机轮换

1. 旧凭证有效时签发新凭证。
2. 将新 Token 写入 CI Secret Store，不修改 Grant。
3. 使用新凭证执行读取，并在授权时执行发布。
4. 撤销旧凭证。
5. 确认旧值返回 `401`、新值正常，审计 actor 仍为 `service-account:<id>`。
6. 从 CI Secret Store 移除旧值。

每个发布候选运行 `make service-account-rotation-e2e`。隔离 Gateway/PostgreSQL/RustFS 门禁覆盖凭证重叠、稳定 Grant、Raw Basic 发布/读取、单独撤销、账号禁用和脱敏审计。

## 事件响应

- 仅单个 secret 疑似泄漏时撤销该 credential。
- 应用身份不再可信时禁用整个 Service Account，以一次阻止所有凭证并保留审计和 Grant。
- 检查 `service_account.credential.create`、`.revoke`、`service_account.update` 审计。
- 只有签发新凭证并从下游 Secret Store 清除全部受影响值后才重新启用。
