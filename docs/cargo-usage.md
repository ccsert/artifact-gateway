# Cargo registries

[简体中文](cargo-usage.zh-CN.md) | [Protocol compatibility](protocol-compatibility.md)

Create a Cargo Hosted, Proxy, or Group repository in the Console or management API. The registry root is `https://gateway.example/cargo/<name>/`; Cargo requires the `sparse+` prefix and the trailing slash in its index setting. Replace the example host and repository name with your deployment values.

## Hosted or mixed Group as an alternate registry

Put this in the project's `.cargo/config.toml` (or `$CARGO_HOME/config.toml`):

```toml
[registries.gateway]
index = "sparse+https://gateway.example/cargo/company/"
credential-provider = "cargo:token"
```

For a private registry or Hosted publication, obtain a Gateway credential with the appropriate repository grant. Inject `CARGO_REGISTRIES_GATEWAY_TOKEN` from a CI secret store or interactive credential provider. Its value is `Bearer <gateway-token>`; do not commit it to `.cargo/config.toml` or a lockfile. Anonymous reads work only when both global and repository or Group policy allow them. Group members also need compatible read grants.

```sh
cargo publish --registry gateway                 # Hosted only
cargo search <crate> --registry gateway
cargo add <crate> --registry gateway
cargo install <crate> --registry gateway
cargo yank <crate>@<version> --registry gateway  # Hosted only
cargo yank --undo <crate>@<version> --registry gateway
```

A mixed private Group has its own registry identity. Keep `registry = "gateway"` on dependencies that come from it. Do not use a mixed Group as a crates.io source replacement. Gateway repository grants govern publishing; `cargo owner` is not implemented. Yank changes future version selection but preserves previously resolved crate downloads. Management tombstones are a separate operation.

## Dedicated crates.io Proxy as source replacement

Create a Cargo Proxy with upstream `https://index.crates.io` and allowed hosts `index.crates.io, static.crates.io`. The allowlist must include any additional HTTPS hosts named by the upstream `config.json` download URL. Then configure a dedicated, crates.io-equivalent Proxy:

```toml
[source.crates-io]
replace-with = "gateway-crates-io"

[source.gateway-crates-io]
registry = "sparse+https://gateway.example/cargo/crates-io-proxy/"
```

Run `cargo build` or `cargo install` normally. The Proxy validates the upstream checksum before making `.crate` bytes visible and can serve cached versions when upstream is offline. An uncached version still needs upstream access. Proxy and Group reject publish, yank, and unyank.

The verified client baseline is Cargo 1.96.0. Run `make cargo-contract`, `make integration-test`, `make backup-restore-readiness`, and `make cargo-upgrade-readiness` for the repository's client and persistent recovery gates. These source and test claims do not assert that an existing deployment has been upgraded.
