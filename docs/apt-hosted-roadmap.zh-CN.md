# APT Hosted 路线图

[English](apt-hosted-roadmap.md) | [文档索引](README.zh-CN.md)

## 状态与优先级

APT Hosted 是 Kubernetes 本地部署基线后的格式扩展重点。APT Proxy 与有序 Group 读取已支持；本文描述由 Artifact Gateway 持有的可信发布。在各里程碑通过验收前，不改变当前公开能力。

APT 不能通过通用上传接口接纳。Debian 客户端通过 `Release`/`InRelease` 中的校验和解析 `Packages`，且当前 APT 默认拒绝无签名仓库，因此 Gateway 必须先实现原子签名快照模型。

规范参考为 [Debian Repository Format](https://wiki.debian.org/DebianRepository/Format) 与 [`apt-secure(8)`](https://manpages.debian.org/unstable/apt/apt-secure.8.en.html)。

## APT-H1：发布契约与领域模型

状态：已完成。已实现流式 `.deb` 解析、预留配额的幂等 session、Repository 写权限管理 API、生成客户端、按内容寻址 staged revision、中断恢复、引用安全的持久孤儿回收、审计、building snapshot、Memory/PostgreSQL 一致性和窄 signer 接口。

staged revision 与 building snapshot 不出现在协议读取中，H2/H3 完成前仍只公开 Proxy 能力。管理流程严格处于可见前：

1. 管理员通过 `POST /api/v2/repositories` 显式创建 `format: apt, type: hosted` 预览仓库。
2. 带 `Idempotency-Key` 调用 `/apt/publication-sessions`，为一个 suite、component 和 `.deb` 预留配额。
3. 向 session 的 `/package` 流式上传、哈希和解析包，从 Debian control 元数据派生规范身份。
4. 查询 session 持久状态；`staged` 不代表可安装，只有 H2 原子快照才能发布。

必须定义 package/version/architecture 身份、重复版本、幂等、配额、审计、中断恢复和 signer 边界。Gateway 只向窄 signer 请求签名，应用节点不持有或分发私钥。

验收门禁包括管理 provisioning、迁移、Memory/PostgreSQL 一致、流式限制、异常归档、身份向量与冻结 OpenAPI。H1 已通过，但不代表 Hosted 协议可用。

## APT-H2：原子 Hosted 仓库

状态：已完成但不公开的操作员预览。已提交包会确定性生成 `Packages`/gzip 索引、Release 校验和与 Acquire-By-Hash 对象；H1 signer 生成 `InRelease`/`Release.gpg`，PostgreSQL 以一次可审计事务切换全部签名资产可见性。

Hosted 支持 GET、HEAD、条件响应和 Range。故障注入覆盖签名失败与审计回滚；PostgreSQL/RustFS 跨实例测试覆盖发布、pool 路径不可变、对象写失败和孤儿回收。中断构建保留对象意图，不能替换旧快照。

发布 API 在 `Idempotency-Key` 下提交不可变 session 集。暂时签名或配额失败可精确恢复。loopback 参考 signer 的私钥不进入 Gateway；Debian 容器门禁执行签名 `apt-get update` 和安装，并在 signer 停止后从不可变状态重放。

客户端必须只看到同一快照的包、索引和 Release。直接与 by-hash 路径指向同一对象时容量只计算一次。参考 signer 只是 H2 验收夹具，因此能力仍显示 Proxy/Group。

## APT-H3：生产签名、轮换与运维

状态：进行中。远程 HTTPS signer 必须用 operator 挂载的纯公钥 keyring 验证，并精确匹配一至两个固定 OpenPGP fingerprint；Gateway 在可见前验证 `InRelease` 与 `Release.gpg`，支持 old/new 失败关闭的重叠窗口。

启动日志、preflight、快照响应和不可变审计暴露有效 fingerprint 证据。管理员 signing-state API 与 Console 对比运行策略和最新快照，展示 active/next/out-of-policy；有界结果与延迟指标不使用 signer、Repository 或 actor 等高基数 label。

原生 APT 门禁已证明 PostgreSQL/RustFS 备份、后续变更、恢复旧快照、证据与对象摘要一致，以及 signer 离线安装。独立 HTTPS 门禁已证明旧密钥、重叠、新密钥、拒绝与退役。

仍待完成：托管 KMS/HSM 私钥保管与恢复、专用快照保留/重建/导出、部署告警。验收要求轮换前中后 Debian 验证成功，恢复仓库重现相同签名摘要；完成这些生产保管项前不得宣称 H3 完成。

## APT-H4：生命周期、扫描与分发

- 删除或保留必须生成新签名快照；墓碑包在宽限期内可恢复，物理回收延迟且重查引用。
- 在公开手动或发布扫描前实现 `.deb` resolver 与 scanner adapter；当前参考扫描器不宣称覆盖 APT。
- 隔离读取与准入策略应用到 package 身份及签名快照，不能让被阻止包通过旧索引意外可达。
- 晋级/复制拷贝不可变包证据，再为目标重建并签名元数据；不得复制源仓库 `Release` 作为目标权威。
- 在独立 Group 快照 owner 能生成并签名一致聚合前，APT Group 只允许有序 Proxy，不原地合并独立签名上游。

验收要求保留、恢复、隔离、晋级和复制通过 PostgreSQL、Worker 重试、快照原子性与真实 APT 客户端测试。

## 交付顺序

按 H1 至 H4 推进。H1/H2 建立最低真实 Hosted 预览，H3 是任何生产声明的前提，H4 补齐现有生命周期和安全模型。H1-H3 全部通过前，格式能力 API 与 Console 仍显示 APT 为 `proxy` only。
