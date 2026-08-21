# Cargo 仓库研究与建议路线图

[English](cargo-repository-research.md) | [文档索引](README.zh-CN.md)

状态：研究建议，不是已实现协议或已接纳格式。C0 字节基础已实现严格有界 publish framing、完整 `.crate` 校验、规范 manifest 身份、sparse index 路径/行转换，以及官方 `cargo package`/`cargo publish` 契约测试。

C0 退出前仍需持久碰撞预留和 Memory/PostgreSQL 身份一致性；目前不公开 Cargo 路由、格式、OpenAPI 或 Console 选项。运行 `make cargo-contract` 需要固定 Rust/Cargo 1.96.0；缺少 Cargo 时门禁失败。

## 决策

Cargo 值得在 APT H3 后成为下一个新生态，优先于 NuGet；APT 生产签名、恢复和生命周期仍是当前完成工作。这是工程优先级建议，不代表已证实客户需求。

理由：Cargo 有原生 publish、download、search、yank 和两种文档化 index 协议，不需自创 companion；一个版本拥有一个不可变 `.crate` 与一条 index row，SHA-256 由服务端计算；Gitea 证明官方 client 可 publish/add/install/yank/search；不可变归档与 Gateway digest、扫描、隔离、晋级、复制和 RustFS 模型匹配。

真正成本不在上传，而是让 sparse index、Proxy source identity 和 Group ownership 对每个 crate/version 一致。在[格式扩展门禁](format-extension-guide.zh-CN.md)通过前，Cargo 必须保持不可发现。

## 规范协议基线

Cargo Registry 包含 index，并可通过根 `config.json` 声明 Web API。以 `sparse+` 开头的是 sparse HTTP registry，否则远程 index 使用 Git。配置包含 download `dl`、可选 `api` 和可选 `auth-required`。

Index 对每个小写 crate path 保存每版本一行 JSON。每行包含下载 `.crate` 的准确 SHA-256 `cksum`。同 crate 的版本忽略 SemVer build metadata 后只能出现一次；插入后除 `yanked` 外不可变。

首版 Web API 包含：`PUT /api/v1/crates/new` 发布，`DELETE .../{crate}/{version}/yank`，`PUT .../unyank`，以及 `GET /api/v1/crates?q=...` 搜索。

Publish body 是 little-endian metadata JSON 长度、JSON、crate 长度和 `.crate` 字节组成的 frame；registry 自算 checksum。Cargo 允许 publish 成功后短暂等待 index，但 Gateway 选择更强且兼容的规则：download 与 index row 原子可见后才返回成功。

文档对 private search 的认证表述存在边界差异，因此必须用官方 client 验收，而不能从单一文档假设。

## 建议的 Gateway 接口

每个 Repository/Group 使用一个带尾斜杠的规范 sparse root：

```toml
[registries.gateway]
index = "sparse+https://gateway.example/cargo/<repository>/"

[registry]
default = "gateway"
```

CI 应使用 Cargo credential provider 注入 Token。Cargo 会原样发送 Authorization，因此值为 `Bearer <gateway-token>`；可使用 `CARGO_REGISTRIES_GATEWAY_TOKEN`。

所有 Repository 类型提供相同读取形状：生成 `config.json`；读取 crate index；按 owner 下载 `.crate`；搜索。只有 Hosted 支持 publish/yank/unyank，Proxy 与 Group 明确拒绝。

Gateway `config.json` 的 `dl` 指向本地 `/api/v1/crates`，`api` 指向无尾斜杠的 Repository root；私有 registry 设置 `auth-required: true`。未认证 config 请求可先返回 401，再由 Cargo 带 credential 重试。首版不实现 `cargo owner`，授权以 Repository Grant 为准。

### Hosted

规范身份为 Repository、碰撞安全 crate-name key、忽略 build metadata 的 SemVer 唯一键，以及计算出的 `.crate` digest。保留展示名称/版本，但拒绝大小写和 `-`/`_` 混淆碰撞。

发布步骤：严格限制 frame metadata、压缩/解压大小、文件数、路径和时间；流式写 digest-addressed staged RustFS 并计算 SHA-256；有界检查归档并把规范 `Cargo.toml` 与 publish metadata 对照；准确转换 renamed dependency、version req、feature 和 checksum ownership；在一个事务中预留配额、提交引用、插入 index、审计并原子公开 index/download。

Cargo 不发送 idempotency key。可按 actor、Repository、规范坐标和 body digest 识别精确重试：相同 digest 返回 committed，既有版本不同 digest 返回不可变冲突。

Yank 不是删除，只切换 index `yanked`，阻止新解析但保留 lockfile 下载；unyank 只反转该标记。Tombstone 属于独立生命周期。

### Proxy

缓存上游 config、每 crate index 与 `.crate`。只有下载字节与 index row SHA-256 一致后才能发布缓存；保留 ETag/Last-Modified 与 304，负缓存区分文档化的 404/410/451。

Gateway 重写客户端 config 中的 `dl/api/auth`，绝不改 crate version、checksum 或显式 dependency registry。上游 credential 仅服务端持有，不进入响应、审计或 key。

crates.io source replacement 要求完全等价，应只指向专用、保 checksum 的 Proxy。混合私有 Group 是 alternate registry，不能作为 crates.io replacement。

### 有序 Group

Group 是合成只读 registry，生成自己的 config，合并成员 index。Ownership 由规范 crate name 与版本唯一键确定；只有不可变 index 数据和 checksum 完全相同时可去重，冲突必须拒绝 member 变更或新发布，不能因顺序变化选择不同字节。

首次暴露持久化 owner、index-row digest 和 `.crate` digest 的不可变 claim。重排或新增成员不得改变；删除/tombstone owner 必须显式迁移或墓碑，不能静默改指向。

Index、download、search、quarantine 和 failure 必须使用同一 owner function。Checksum mismatch、拒绝或隔离不能 fallback。Group 拒绝 publish/yank/unyank，mutation 直接发往 Hosted member。

## Sparse 与 Git Index

首版只提供 sparse，并把 sparse URL 作为唯一规范来源。Sparse 只取相关 HTTP metadata，可利用 HTTP/2、ETag/Last-Modified；Git index 会引入 smart HTTP、repo 维护、ref update、compaction/repair 和第二个一致性边界。

Cargo 会把 registry URL 写入 lockfile，同时提供 sparse/Git 会形成不同 source identity。只有测得支持基线无法用 sparse 的兼容需求后才重新评估 Git，并作为独立契约而非隐藏 alias。

## 生命周期与治理映射

- 身份：crate name/version + `.crate` SHA-256，唯一性忽略 build metadata。
- 扫描：按不可变坐标/digest 扫描 committed `.crate`；publisher metadata 与 scanner evidence 独立。
- 隔离：不改变 lifecycle；开启读取强制后从 sparse/search 隐藏并拒绝下载，Group 不 fallback。
- 保留：yank 不等于删除。Hosted 默认不自动移除已发布版本；任何破坏性保留必须显式、可预览、墓碑化、可恢复且强警告。
- 晋级：以 `.crate` 与精确 index metadata 为原子单元；同 digest 幂等，不同 digest 冲突。
- 复制：按 digest copy/verify，最后发布 index；byte 与 metadata 分开 checkpoint，绝不先暴露 index。
- 授权与错误：Cargo mutation 使用 `{"errors":[{"detail":"..."}]}`，管理 API 继续 problem+json；Grant 为权威。

## 建议交付路线图

### C0：冻结契约与字节 parser

Parser 与官方 client 契约已完成；剩余持久 identity reservation，用于在 Memory/PostgreSQL 证明大小写与 `-`/`_` 碰撞。

冻结 sparse route、身份、碰撞、frame limit、error envelope、private auth；覆盖异常 frame、gzip/tar expansion、重复/穿越路径、metadata mismatch、renamed dependency、feature、SemVer build metadata、checksum 和当前 index 字段。

退出：parser/property 与持久一致性通过，异常字节不创建 object intent 或可见 index。

### C1：Hosted sparse registry

实现原子 publish、config/index、download、search、yank/unyank、私有/公开读、容量、配额、审计、browse 和深链接，并支持对象上传与 metadata publication 间失败的精确恢复。

退出：官方 Rust 镜像完成 publish/add/install/search/yank/undo；staged 不可见，既有 lockfile 仍下载 yanked version。

### C2：保 checksum Proxy

实现 sparse upstream、checksum gate、条件/负缓存、redirect/egress、认证上游和脱敏诊断。退出：在线与上游离线 build/install 成功，损坏字节不可见，真实 crates.io replacement 无 source drift。

### C3：不可变有序 Group

通过一个持久 claim function 合并 index/search/download，只去重完全一致内容，对冲突、重排、删除、墓碑、隔离、授权和失败都保持 owner。退出：混合 Hosted+Proxy 作为 alternate registry 工作，碰撞显式，重启/重排不改变 digest，并证明不能当 crates.io replacement。

### C4：生命周期、情报与分发一致性

增加 Tombstone、restore、安全保留、回收、扫描、隔离读取、晋级、复制、Webhook、指标、备份恢复和升级证据。全部 Memory/PostgreSQL/RustFS、Worker 与官方 Cargo 门禁通过后，才可公开 Hosted/Proxy/Group。

## 验收矩阵

Hosted 需原子 publish、不可变下载、search、yank、认证、生命周期和分发；Proxy 需 checksum gate、离线 replay、源替换；Group 需持久 owner 与 collision。所有类型都需 PostgreSQL/RustFS 恢复、升级及官方 Cargo client 证据，单元或合成 HTTP 测试不能替代。
