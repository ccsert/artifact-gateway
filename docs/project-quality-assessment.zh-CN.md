# 项目质量评估

[English](project-quality-assessment.md) · [文档总索引](README.zh-CN.md)

评估快照：2026-08-21。本评估描述项目在公开前准备阶段的工程质量，不代表生产认证或
发布批准。

## 综合判断

Artifact Gateway 已经具备扎实的协议与持久化基础、明确契约、广泛的自动化门禁，以及
远超一般早期项目的生命周期语义。当前主要风险已经不是“基础制品库功能缺失”，而是
功能继续增长时，少数大型应用与 Console 模块会显著提高安全变更成本。

| 维度 | 判断 | 证据与解释 |
| --- | --- | --- |
| 架构 | 强 | PostgreSQL 与对象存储职责明确；运行角色和持久化任务语义已有文档。 |
| 协议正确性 | 强 | 能力限制显式记录，对外声明的格式有原生客户端测试。 |
| 验证体系 | 强，但覆盖率仍有提升空间 | CI、契约、集成、E2E、恢复和准备门禁齐全；部分全局覆盖率下限仍较保守。 |
| 可维护性 | 需要集中优化 | 若干路由、模型和原生协议文件体量过大，评审成本和变更耦合正在上升。 |
| 文档 | 核心入口双语，深层覆盖不均 | README、快速入门、整体架构、参与开发、协议兼容性、恢复和索引已有中文入口；多数深层契约与研究资料仍只有英文。 |
| 可运维性 | 准备基线良好 | 健康检查、指标、诊断、恢复、Kubernetes 和分布式角色文档已具备；生产证据仍在准备。 |
| 性能 | 本地基线可复现 | 已测量二进制/镜像体积、静默内存、经鉴权的 PostgreSQL/RustFS 读取和一组 64 MiB 工程负载；受控类生产负载和 soak 尚未完成。 |
| 公开项目准备 | 明确延后 | 许可证、正式分发、公开安全报告渠道和公开支持承诺不在当前范围。 |

## 当前高质量部分

- 架构没有隐藏存储边界：PostgreSQL 负责元数据与协调，经过校验的不可变字节进入
  S3 兼容对象存储。
- 后台任务使用持久化租约、fencing、幂等和轮询恢复，不把通知当成事实来源。
- 协议声明受显式兼容矩阵和真实客户端门禁约束。
- OpenAPI 源、Console 生成客户端和 Go 生成契约由同一个漂移检查约束。
- 迁移、备份恢复、就绪、依赖和覆盖率检查都随仓库版本管理。
- 隔离 Docker 的[性能基线](performance-baseline.zh-CN.md)量化了交付物体积、静默内存、
  并发吞吐和观测峰值，并明确避免把笔记本结果写成生产 SLA。
- APT Hosted、Cargo、NuGet 等预览或研究工作没有混入正式能力声明。

## 已启动的可维护性优化

质量优化已经开始，但远未完成：

- `console/src/lib/publicBrowseModel.ts` 已经承接 Maven/Conan 纯分组与深链状态，并通过
  该模块接口测试。
- `console/src/components/PublicBrowsePrimitives.tsx` 已经承接版本选择、元数据和使用片段
  等共享展示原语。
- 本轮将 OCI Index/Manifest/Config 递归读取和 Tag 分页提炼到
  `console/src/lib/publicOci.ts`。它只暴露小型读取接口，可接入生产或测试 HTTP adapter；
  聚焦测试覆盖递归、摘要保留、体积汇总、可选 Config 失败和错误传播。
- 各协议的仓库使用说明已经移入纯函数边界
  `console/src/lib/publicRepositoryUsage.ts`；八种格式均可脱离浏览器状态和页面组件测试。
- 两次提取在不改变路由或可见行为的前提下，将 `PublicBrowse.tsx` 从 2,705 行降到
  2,534 行。它仍是热点，下一步 seam 是 Maven/Conan 展示区块和其余格式投影。

`internal/repository/model.go` 以及大型 Maven/npm/OCI 应用模块还没有获得同等规模的
实际拆分，仍属于计划工作，不能写成已经完成的质量提升。

本轮 Raw 字节路径评审已经产生了可验证的后端改进：

- Proxy miss 现在用固定 128 KiB 缓冲写入临时文件并计算哈希；缓存发布与读取使用
  `PutVerifiedReader`、`Stat`、`Open` 和 `OpenRange`，不再使用制品等大的 `[]byte`。
- 正向 HEAD miss 仍是上游 HEAD，不再下载或缓存上游 body。
- 匿名成员过滤不再别名修改 MemoryStore slice；request lock 按 member 及时释放，并在
  慢上游任务期间续租。
- Raw 与 OCI 的 resumable PATCH 持久化不可变 offset chunk，不再为每个新块下载并重写
  旧字节；完成时只做一次有序摘要校验，并兼容旧的累计对象会话。完成、取消或过期的
  Raw Session 会保留 PostgreSQL 轨迹，并由 durable reclaim 删除残留 prefix 与 chunk。
- 每个 Gateway 默认最多允许 4 个 Raw Proxy 临时落盘任务并发执行，该正整数可配置。
  admission 在访问上游前生效；饱和时返回带 `Retry-After: 1` 的 `503`，并递增
  `artifact_gateway_raw_spool_rejections_total`。
- 受控 HTTPS Docker 负载让 8 个并发客户端读取同一个 64 MiB 冷路径，只产生一次上游
  body GET；停止上游后，又完成了逐字节一致的缓存回放。
- signer/scanner 启动失败会记录真实根因；生产代码中的 `%v` 错误包装已替换为保留
  error chain 的 `%w`。

常量堆 Proxy 路径有意把 heap 压力换成临时磁盘压力。进程内 admission 已经限制同时
落盘数量，但部署仍需独立临时卷、空闲空间监控，以及硬性的 ephemeral-storage
request/limit。chunk 完成目前是 O(n)；后续可在不改变 HTTP chunk 契约的前提下，在
对象存储端增加 S3 multipart compose，以去掉完整上传的完成阶段 spool。

## 主要质量缺口

### 1. 大型模块提高变更耦合

当前热点包括：

| 文件 | 约计行数 | 建议拆分边界 |
| --- | ---: | --- |
| `console/src/pages/PublicBrowse.tsx` | 2,534 | 路由外壳、查询状态、格式投影和展示区块 |
| `internal/repository/model.go` | 1,328 | 仓库、身份、生命周期、情报和运维记录 |
| `internal/app/native_maven.go` | 1,173 | HTTP 适配与 Maven 发布应用服务 |
| `internal/app/native_npm.go` | 1,160 | Registry HTTP 结构与 Hosted/Proxy/Group 编排 |
| `internal/app/native_oci.go` | 1,033 | Distribution 路由与上传/Manifest 应用服务 |

文件大并不等于必然存在缺陷，但会降低独立评审、窄测试和后续职责归属的效率。重构应
保持公开行为，每次只移动一个明确边界；一次性重写整个包会制造更大风险。

### 2. 覆盖率下限用于防回退，不代表完成度

仓库级 Go 覆盖率门槛为 40%；稳定包有更高专项门槛，但 authorization 起始门槛为
38%。Console 对全部手写代码执行 40% 行/语句、53% 函数、65% 分支门槛，并为重点
模块设置更高阈值。这些门槛能防止回退，但随着大型模块拆分和公共边界测试成本下降，
应该持续提高。

### 3. 深层文档尚未全部双语

项目已经具备中英文对等的 README、文档索引、快速入门、整体架构、参与开发、协议兼容
性和恢复入口。中文索引会明确标记剩余“仅英文”内容，不再暗示已经存在中文正文。逐字
翻译所有 ADR 与研究记录会带来很高同步成本；下一批应优先身份与运维旅程，再考虑实现
研究材料。

### 4. 准备证据范围大于默认测试目标

`make test` 保护共享本地行为，而完整协议 E2E、集成、浏览器、性能、轮换、升级和恢复
门禁属于独立目标。后续准备清单必须指向精确 CI 证据，不能把单个绿色命令当成完整发布
证明。当前性能报告是可复现的 arm64 本地基线；成为发布阈值前，仍需受控
Linux/amd64 Runner、资源上限、TLS/Ingress、读写混合和持续 soak。

## 建议优化顺序

1. **保持入门路径可执行。** 把 `make dev-bootstrap`、双语快速入门和文档链接当成受测接口。
2. **继续在不重做交互的前提下拆分 `PublicBrowse.tsx`。** 纯浏览模型、共享原语、OCI
   读取模块和仓库使用说明生成器已经形成聚焦 seam；下一步拆 Maven/Conan 展示区块，
   并保留搜索、筛选、深链和响应式状态的浏览器回归。
3. **按领域拆分仓库记录。** 先机械移动类型，不同时修改持久化行为，并保持公开仓库接口稳定。
4. **加深原生协议模块。** 按格式逐一分离 HTTP 解析/响应塑形与 Hosted/Proxy/Group 应用服务。
5. **加固大对象字节面。** 增加部署级临时卷和 ephemeral-storage 限额，并在该限额下
   验证大量不同对象并发 miss 的 admission 行为；再评估隐藏在 object-store port 后、
   且不要求客户端固定 chunk 大小的 multipart compose。
6. **每次提取同步提升专项覆盖。** 先补有意义的边界测试再提高阈值；不得为通过 CI 下调门槛。
7. **审慎升级性能证据。** 在受控 Linux/amd64 Runner 上复跑，再加入硬资源限制、混合
   流量与 soak 阈值，不能把单台笔记本快照转化为通用结论。
8. **维护准备矩阵。** 正式分发工作启动后，记录精确提交、干净工作区、不可变镜像、CI、
   集成、恢复、目标环境和具名批准证据。

## 持续优化规则

- 优化内部结构前，优先保持协议兼容性和不可变制品身份。
- 采用 API 与持久化契约不变的小步提取。
- PostgreSQL 保持为唯一控制面协调服务；没有测量证据和架构决策，不新增中间件依赖。
- 轻量级定位中不得淡化独立的 S3 兼容字节面要求。
- 重构涉及运维可见边界时，同时增加聚焦测试和文档更新。
- 始终区分本地验证、CI 候选和正式发布证据。
