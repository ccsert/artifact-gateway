# APT Hosted 签名与 H3 轮换预览

[English](apt-hosted-signing.md) | [文档索引](README.zh-CN.md)

APT Hosted H2 是验证完整签名发布路径的操作员预览，可被 Debian 客户端安装，但不是生产签名服务。对外公开前，H3 必须具备托管密钥、轮换、撤销、恢复演练、指标和告警。外部 HTTPS 与客户端轮换演练已可执行；KMS/HSM 和密钥恢复仍未完成。

## 本地部署

生成 32 至 256 字节随机 Bearer Token，在本地 `.env` 中配置且不要提交：

```text
COMPOSE_PROFILES=apt-signer
GATEWAY_APT_SIGNER_ENDPOINT=http://127.0.0.1:18083/v1/sign-release
GATEWAY_APT_SIGNER_TOKEN=<random-token>
GATEWAY_APT_SIGNER_TIMEOUT=15s
```

启动正常本地栈。Signer 与 Gateway 共用网络命名空间，只监听 loopback，RSA 私钥仅存放在 signer 容器挂载的 `gateway-apt-signer` volume。Gateway 只接收签名字节和公钥身份，永不读取私钥。

公钥可在共享网络内从 `http://127.0.0.1:18083/v1/public-key` 获取。通过健康检查二进制导出或可信运维渠道复制后再配置 APT 客户端；系统不会自动安装信任。

## 发布顺序

1. 使用 `POST /api/v2/repositories` 创建 `format: apt, type: hosted`。
2. 带 `Idempotency-Key` 创建一个不可变 package publication session。
3. 以 `Content-Type: application/vnd.debian.binary-package` 上传准确 `.deb`。
4. 使用新的稳定 `Idempotency-Key` 发布 suite snapshot：

```json
{
  "suite": "stable",
  "sequence": 1,
  "publicationSessionIds": ["<staged-session-id>"]
}
```

第 4 步原子提交 `Release`、`InRelease`、`Release.gpg`、直接/by-hash 索引及全部 `pool/` 包之前，package 不可见。精确重试返回或恢复同一快照；同 key 不同 body 返回 `idempotency_conflict`；signer 不可用返回 `signer_unavailable`；配额不足返回 `quota_exceeded`，提升配额后可用同 key 重试。

## 验证

`make native-apt-e2e` 构建真实 `.deb`，通过生成契约发布，在干净 Debian 容器导入参考公钥，执行 `apt-get update` 和安装。随后捕获 Release、签名、直接/by-hash 索引和 `.deb` 的不可变证据，备份 PostgreSQL/RustFS，发布新快照并恢复备份。

恢复后的 signing-state 和所有字节摘要必须与原快照完全相同，最后停止 signer 再次更新和安装。该门禁证明仓库元数据和对象恢复；signer 私钥 volume 不在备份中，所以生产密钥备份恢复仍是 H3 责任。

`make apt-signer-rotation-e2e` 验证外部边界：两个 signer 独立持有只读私钥 volume，Gateway 使用 signer 专用 CA 通过 TLS 连接。门禁依次验证旧密钥、old/new 重叠、切换 signer、旧客户端拒绝新快照、退役旧密钥，以及只信任新公钥的安装。Gateway 从不挂载私钥。

这是生产形态的本地轮换与客户端信任演练，不代表参考 signer 是托管 KMS/HSM。生成密钥和短期 CA 随隔离 Compose 项目删除。

## 生产边界

不得把参考 signer 的本地私钥作为生产信任根，也不得发布其 Token 或 volume。生产 H3 adapter 必须通过 HTTPS 暴露相同的 digest-bound 窄协议，并把私钥操作限制在托管 KMS、HSM 或等价的可审计 signer 中。

远程 signer 默认失败关闭。`GATEWAY_APT_SIGNER_TRUSTED_FINGERPRINTS` 必须包含其 OpenPGP fingerprint，`GATEWAY_APT_SIGNER_TRUSTED_PUBLIC_KEYS_FILE` 必须指向只含相同一至两个公钥的有界 armored keyring。

Gateway 同时验证 `InRelease` 与 `Release.gpg`，并要求已验证签名实体匹配报告的 fingerprint。Signer identity、算法和主密钥 fingerprint 从可信 OpenPGP packet 派生，不接受 HTTP 响应自报值。

解析器只接受一个公钥 armor block，拒绝私钥、额外 block 和尾随数据。远程签名要求 RSA/SHA-256，密钥 2048 至 4096 位。fingerprint 可配置一个 active 的 40/64 hex 值和一个可选 next 值；配置不会自动改变客户端信任。

私有 CA signer 使用只读 PEM 和 `GATEWAY_APT_SIGNER_TLS_CA_FILE`。Gateway 仅为 signer client 建立专用 trust pool，不修改进程全局根证书。

生产轮换顺序：先通过运维渠道分发并验证 next 公钥；挂载包含 old/new 的 keyring、配置 fingerprint 并重启；切换 signer 并确认新快照与审计显示新 fingerprint；客户端重叠窗口结束后再移除 old。

`reference-apt-signer-keyring` 可合并一至两个独立公钥并打印规范 fingerprint，不接受或输出私钥。未列出的 signer key 会在快照可见性变化前被拒绝；preflight 只暴露验证后的 key 数量。

## 操作员状态与指标

管理员可调用 `GET /api/v2/repositories/{repositoryId}/apt/signing-state` 或打开 Security Tab。响应只包含 signer mode、一至两个公开 fingerprint 和最新快照证据，不返回 endpoint、Token、路径或私钥。

- `unconfigured`：无法发布新快照。
- `fixture`：正在使用 H2 loopback 参考 signer，不能视为生产信任。
- `ready`：一个远程 fingerprint 作为 active key 固定。
- `rotation_overlap`：old/next 双 key 窗口生效。
- `policy_mismatch`：策略不完整或最新快照 key 不在当前 pins；新签名已失败关闭，但下次发布前必须处理。

`/metrics` 暴露有界进程级计数和延迟直方图：

```text
artifact_gateway_apt_signing_requests_total{outcome="success|untrusted_signer|invalid_signature|unavailable"}
artifact_gateway_apt_signing_duration_seconds_bucket
artifact_gateway_apt_signing_duration_seconds_sum
artifact_gateway_apt_signing_duration_seconds_count
```

label 不包含 Repository、actor、endpoint 或 fingerprint。部署可对 15 分钟内失败次数和 p95 超过 10 秒设置告警；发布窗口内失败应 page，延迟异常可 warning。项目尚未提供监控栈时，告警安装由部署方负责。
