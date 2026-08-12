# Nexus 能力审查（2026-08）

> 历史时点审查：本文保留 2026-08 审查当时的结论，不再作为当前能力事实来源。
> 已完成项和剩余差距以 `docs/nexus-gap-analysis.md` 为准。

本次审查覆盖后端协议与管理 API、生命周期/存储/授权，以及 Ant Design 6 Console 的主要用户流程。结论是：Artifact Gateway 的 Hosted 生命周期、Proxy 缓存治理、匿名读取、审计保留、晋级/复制和四种协议的基础兼容性已经形成可用 V1；要达到或超过 Nexus 的企业级体验，剩余工作主要集中在“运营规模”和“安全治理”，而不是再堆叠单个格式页面。

## 已确认的优势

- OCI、Maven、Conan、Raw 都有 Hosted、Proxy 或 Group 的明确边界。
- 发布、删除、恢复、保留、回收、晋级和复制具有幂等、审计或校验约束；复制支持 checkpoint 和 SHA-256 校验。
- 匿名读取策略、仓库级 grants、本地用户、API key 角色和 OIDC 验证已经存在，且读写/管理路径分开。
- Console 已使用 Ant Design 6，提供全局搜索、公开浏览、容量趋势、缓存运维、访问控制、审计导出和登录入口。

## 仍需补齐的能力

### P0：企业安全与身份

- OIDC 目前仍是单 issuer/audience 验证，缺少 IdP 角色映射、logout/back-channel、会话列表和登录审计。
- 本地用户/API key 仍是粗粒度 reader/writer/admin；缺少仓库路径 content selector、角色模板、权限解释器和过期/最后使用时间。
- 需要安全基线：密码重置/轮换策略、API key expiry、JWKS/issuer 配置页面、CSRF/SSRF/上传大小与解压限制的可观测告警。

### P1：运营规模

- 全局搜索目前由 Console 并发调用每个仓库的搜索接口；大仓库、多租户和匿名公开目录应由后端索引统一分页、排序和权限过滤。
- 任务中心目前能汇总已有生命周期任务，但还没有通用 scheduler、重试/暂停/取消操作、队列深度和 worker 健康指标。
- Proxy 还缺少可配置缓存 TTL、负缓存策略、路由规则和凭据轮换；Blob store 只有单一 S3/RustFS 后端，没有 compaction/容量趋势的服务端时序。
- 备份/恢复已有 rehearsal，但缺少 Console 里的备份策略、最近一次成功备份、恢复演练结果和下载 support bundle。

### P2：制品供应链与体验

- 制品详情还应统一展示 checksum、签名、SBOM、provenance、许可证和漏洞扫描结果，并提供下载/复制依赖坐标/OCI pull 命令。
- OCI、Conan、Raw 的发布入口还不如 Maven 完整；建议抽象共享的分片上传/校验/提交组件。
- Webhook 首切片已覆盖隔离/解除隔离事件；仍缺少更广事件类型、邮件通知、
  stage/release 工作流、收藏/标签/下载热度和保存搜索。
- 审计现在支持服务端仓库、分组、结果、格式、操作和主体筛选；下一步应支持时间范围、分页 token、保存查询和服务端 CSV 导出。

## 本次已落地

1. 修复 Console lockfile，使 GitHub Actions 的 `npm ci` 可复现，并显式锁定生成客户端的 Prettier 步骤，避免 OpenAPI 生成漂移。
2. 将审计结果、格式、操作、主体筛选下沉到 OpenAPI、Memory/Postgres 存储和管理 API；Console 不再只在最近 100 条记录上做客户端过滤。
3. 新增 Console 全局“任务中心”，汇总跨仓库生命周期任务和审计保留任务，展示失败原因并支持状态/类型/仓库筛选。
4. 将保留策略从 Maven 扩展到 OCI、Conan 和 Raw：分别按制品坐标、镜像名称、Conan reference 和文件路径执行格式感知的候选计算，并支持签名游标 dry-run、`If-Match` 并发保护和统一 worker 执行。
5. Raw 删除改为可恢复墓碑；Proxy/Group 明确禁止配置 Hosted 保留策略；Console 使用 Ant Design 6 按仓库格式呈现适用规则，避免向用户暴露无效配置。

## 生命周期后续记录

1. 优先补齐任务重试、指数退避、最终失败状态，以及服务重启后对卡在 `running` 状态任务的恢复。
2. 在任务中心增加立即执行、取消、重试、进度和失败原因，并记录任务级审计事件。
3. 处理策略版本变化时已排队任务的兼容行为，避免旧任务永久失败或无意义重试。
4. 明确 Conan recipe revision 恢复时是否级联恢复由保留策略删除的 package revisions，并以集成测试固化语义。
5. 为 retention dry-run 增加候选原因统计、摘要和导出，便于管理员在真正执行前核对影响范围。

## 建议下一阶段

1. 先做 API key expiry/last-used、OIDC role mapping 和权限解释接口，这三项直接降低生产运维风险。
2. 再做后端统一搜索索引与真正的游标分页，替换 Console 的逐仓库 fan-out。
3. 最后扩展已交付的 Webhook 事件目录，并接入邮件、SBOM/扫描结果和更广任务类型，
   形成 Nexus Firewall/IQ/Task Scheduler 对应能力。

以上缺口按风险和投入排序；不建议为了“看起来像 Nexus”引入与现有生命周期模型冲突的硬删除或无审计的后台操作。
