# Proxy Repository 出站代理设计

[English](proxy-egress-design.md) | [文档索引](README.zh-CN.md)

状态：已于 2026-08-06 实现。迁移 `000070_egress_proxy.sql`、`internal/egress` Transport Factory、含 `:test` 的 V2 管理 API 和 Console 配置表单均已落地。原方案的 `egress_mode` 审计字段延期，当前用 `upstream_error` 表示失败。

## 问题

此前只有 Raw Proxy 显式处理出站：使用 `http.ProxyFromEnvironment`，未命中环境代理时把 dial 固定到通过私网检查的 DNS 结果。OCI、Maven、Conan 使用默认 transport，导致：

- 代理策略是进程级而非每 Repository，无法让 Maven Central 走企业代理而内部 OCI 直连。
- 标准库不支持 `ALL_PROXY` 中的 SOCKS5。
- SSRF 加固不对称，只有 Raw 执行私网地址检查与 DNS pinning。
- 操作员看不到 Repository 的有效出站路径，健康与熔断也无法区分代理故障和上游故障。

## 目标

- 通过 V2 API 和 Console 管理每个 Proxy Repository 的出站配置。
- 支持 `direct`、`environment`、`custom`；custom 支持 HTTP CONNECT 和带远程 DNS 的 SOCKS5，可选用户名/密码。
- 所有格式共用一个 Transport Factory。
- 上游 allowlist 始终作用于目标主机；代理地址除明确测试覆盖外不得解析到私网。
- 凭证不得进入 API 响应、审计、metric label 或日志。

## 非目标

不支持客户端逐请求选择代理、PAC 文件、Hosted 本地读取代理，也不在本文处理复制目标代理。

## 数据模型

Repository 和 legacy Group Proxy member 可带：

```json
{
  "egressProxy": {
    "mode": "custom",
    "protocol": "socks5",
    "host": "proxy.corp.example",
    "port": 1080,
    "username": "gateway",
    "password": "AES-GCM ciphertext, base64",
    "remoteDns": true,
    "noProxy": ["*.internal.example", "10.0.0.0/8"]
  }
}
```

- `mode` 为 `direct|environment|custom`，默认 environment 以保持兼容。
- custom 的 `protocol` 为 `http|socks5`。
- API 通过 TLS 接受明文密码，服务端用 `GATEWAY_EGRESS_PROXY_KEY` 的 32 字节 key 进行 AES-256-GCM 加密，只持久化 ciphertext；响应仅有 `credentialsConfigured`。
- `remoteDns` 默认 false：本地解析上游并执行私网检查，再把 IP 交给 SOCKS5；true 使用 socks5h 由代理解析。HTTP CONNECT 始终发送 hostname。
- `noProxy` 针对上游 host，而非代理 host。

迁移为 Repository 增加 JSONB `egress_proxy`；`NULL` 等同 environment，更新使用现有 `version` 乐观并发。

## Transport Factory

`internal/egress` 以配置包装 clone 后的 `http.Transport`：

- `direct`：`Proxy=nil`，保留上游 DNS 私网检查和 pinned dial。
- `environment`：`ProxyFromEnvironment`，清除本地 dial hook，把 DNS 交给代理。
- custom HTTP：使用 `ProxyURL` 和可选 Basic `Proxy-Authorization`，CONNECT 目标仍须通过上游 allowlist。
- custom SOCKS5：使用 `x/net/proxy.SOCKS5`；按 `remoteDns` 决定传 IP 还是 hostname。
- `noProxy` 匹配时按上游主机绕过 custom 代理。

代理地址在保存和 dial 时都验证；必须可解析、端口合法，非测试模式下不得是 private/loopback。

## API 与契约

- OpenAPI `EgressProxy` 可用于 Proxy Repository 的 create/patch，Hosted 拒绝该字段。
- 响应从不返回 password；新 password 覆盖旧值，`clearCredentials: true` 显式清除。
- 管理员 `POST /api/v2/repositories/{name}/egress-proxy:test` 进行轻量连接测试，返回 reachability、认证结果和延迟，且不持久化。
- 生成物由 `make openapi-bundle`、`make openapi-generate-admin` 和 `make openapi-check` 维护。

## 格式集成

Raw 用共享 Factory 替代专用逻辑并保持 pinning/TLS override 拒绝；OCI 的 `UpstreamClient.Fetch`、Maven 的元数据与负缓存请求、Conan 的 Group member 请求均使用解析后的 Repository 配置。

## Console

Proxy Settings 提供模式、协议、host/port、可选认证、noProxy 编辑和“测试连接”；Overview 显示有效出站模式，健康卡区分代理故障。Audit/Operations 将 `proxy_error` 与现有上游错误一致呈现。

## 安全与审计

Password 只以 AES-256-GCM ciphertext 存储，构造 transport 时延迟解密；数据 key 来自环境，未来可用前向迁移轮换。Metric 只标 mode/protocol，不标 host；测试接口仅管理员可用且限流。

## 推进顺序

1. 迁移、模型和校验；
2. 共享 Factory，先迁 Raw，再接 OCI/Maven/Conan；
3. OpenAPI、生成客户端和 Console 连接测试；
4. 审计、指标及 README/兼容文档。
