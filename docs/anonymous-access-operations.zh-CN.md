# 匿名访问运维指南

[English](anonymous-access-operations.md) | [文档索引](README.zh-CN.md)

匿名访问默认拒绝，并且只适用于协议和浏览接口的 `GET`、`HEAD` 请求。发布、删除、晋级、缓存变更以及所有管理写操作始终需要身份认证。

## 启用条件

匿名读取只有在所有相关门禁均开启时才会放行：

1. 管理员通过带 `If-Match` 的 `PUT /api/v2/anonymous-access-policy` 启用全局策略。
2. 目标 Repository 或 Group 配置了 `anonymousRead: true`。
3. 若目标是 Group，最终命中的成员 Repository 也必须配置 `anonymousRead: true`。

仅开启全局策略不会自动公开已有 Repository 或 Group。关闭全局策略会立即拒绝所有匿名读取，即使目标自身的策略仍为开启状态。

已认证的操作员或具备相应范围的用户可调用 `GET /api/v2/repositories/{repositoryId}/effective-access` 查看自身的实际权限。该诊断接口本身不允许匿名访问，不支持模拟其他用户；即使 Repository 读取被拒绝，它仍会返回当前调用者的读取、写入、管理和匿名读取判定。

使用 `GET /api/v2/identity` 可确认 Gateway 当前评估的身份、凭证来源和全局角色。OIDC 诊断只暴露已配置且与验证后令牌匹配的角色映射，不会返回任意上游声明或令牌内容。

匿名请求无论成功还是拒绝，都会在仅管理员可见的审计日志中记录 `actor=anonymous` 以及授权来源和原因。

Console 将这些条件呈现为统一的公开访问边界：全局开关与 Repository、Group/成员的局部开关分开显示，并在管理员切换全局开关前展示潜在公开目标数量。此界面不会创建额外策略或绕过门禁。

未登录的 `/browse` 目录只显示实际公开的目标。搜索、格式过滤、来源类型说明和只读提示只用于发现；发布、授权与管理仍要求已认证的管理身份。

## 客户端示例

带认证的 Maven 拉取：

```sh
curl -u resolver:$GATEWAY_RESOLVER_TOKEN \
  https://gateway.example.com/maven/releases/org/example/widget/1.0/widget-1.0.jar
```

所有门禁开启后的匿名 Maven 拉取：

```sh
curl https://gateway.example.com/maven/public/org/example/widget/1.0/widget-1.0.jar
```

带认证的 Raw 读取：

```sh
curl -H "Authorization: Bearer $GATEWAY_TOKEN" \
  https://gateway.example.com/raw/releases/widgets/widget.tar.gz
```

匿名 OCI manifest 拉取：

```sh
oras manifest fetch gateway.example.com/public/widget:1.0
```

Conan 2 remote 的 URL 必须保留 Group。不要将匿名访问用于 Gateway 正常 revision 读取之外的端点。

```sh
conan remote add public https://gateway.example.com/conan/v2/public
conan install --requires=widget/1.0@team/stable
```

## 运维检查

- 确认未认证 `GET` 只对预期目标成功。
- 确认未带凭证的 `POST`、`PUT`、`DELETE` 仍被拒绝。
- 检查审计记录中的 `actor=anonymous` 和策略原因。
- 比较 Repository 的实际权限前，先用操作员凭证调用 `/api/v2/identity`，避免误判 Console 保存的凭证。
- 事件响应时优先关闭全局策略，不要先逐个修改 Repository。
