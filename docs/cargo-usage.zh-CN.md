# Cargo 仓库使用

[English](cargo-usage.md) | [协议兼容性](protocol-compatibility.zh-CN.md)

在 Console 或管理 API 中创建 Cargo Hosted、Proxy 或 Group 仓库。仓库根路径为 `https://gateway.example/cargo/<name>/`；Cargo 的索引配置必须带 `sparse+` 前缀和末尾斜杠。请将示例域名及仓库名替换成实际值。

## Hosted 或混合 Group：独立 Registry

在项目 `.cargo/config.toml`（或 `$CARGO_HOME/config.toml`）中配置：

```toml
[registries.gateway]
index = "sparse+https://gateway.example/cargo/company/"
credential-provider = "cargo:token"
```

私有仓库读取或 Hosted 发布需要具备相应仓库 Grant 的 Gateway 凭据。通过 CI 密钥管理或交互式 credential provider 注入 `CARGO_REGISTRIES_GATEWAY_TOKEN`，其值为 `Bearer <gateway-token>`；不要把凭据写入配置文件或锁文件。匿名读取必须同时满足全局与仓库或 Group 策略；Group 成员也需要相应的读取授权。

```sh
cargo publish --registry gateway                 # 仅 Hosted
cargo search <crate> --registry gateway
cargo add <crate> --registry gateway
cargo install <crate> --registry gateway
cargo yank <crate>@<version> --registry gateway  # 仅 Hosted
cargo yank --undo <crate>@<version> --registry gateway
```

私有与公共成员混合的 Group 使用独立 Registry 身份，其依赖应保留 `registry = "gateway"`。不要把这类 Group 当作 crates.io 源替换。发布授权以 Gateway 仓库 Grant 为准，暂不支持 `cargo owner`。Yank 影响新版本选择，但已解析版本的归档仍可下载；管理墓碑是独立操作。

## 专用 crates.io Proxy：源替换

Cargo Proxy 上游设为 `https://index.crates.io`，允许主机设为 `index.crates.io, static.crates.io`。如果上游 `config.json` 指向其他 HTTPS 下载主机，也要将其加入允许列表。仅对与 crates.io 等价的专用 Proxy 使用以下配置：

```toml
[source.crates-io]
replace-with = "gateway-crates-io"

[source.gateway-crates-io]
registry = "sparse+https://gateway.example/cargo/crates-io-proxy/"
```

之后正常运行 `cargo build` 或 `cargo install`。Proxy 在归档可见前验证上游校验和；上游离线时能服务已缓存版本，未缓存版本仍需上游可用。Proxy 和 Group 拒绝 publish、yank 与 unyank。

已验证的客户端基线为 Cargo 1.96.0。使用 `make cargo-contract`、`make integration-test`、`make backup-restore-readiness` 和 `make cargo-upgrade-readiness` 检查客户端与持久恢复门禁。源码和测试结论不代表现有部署已经升级。
