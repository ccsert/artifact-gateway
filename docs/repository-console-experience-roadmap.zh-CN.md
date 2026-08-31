# Repository Console 体验路线图

[English](repository-console-experience-roadmap.md) | [文档索引](README.zh-CN.md)

本文定义 Repository、Hosted、Proxy、Group 在 Console 中的一致体验。目标不是复制 Nexus，而是把 Artifact Gateway 已有的协议、身份、缓存、策略和生命周期语义变成操作员能理解和验证的界面。

## 目标

- 从列表进入精确 Repository、Artifact、Version 与不可变 digest 深链接。
- 明确 Hosted/Proxy/Group 的所有权和读写能力，避免把 cache 或 Group 误当 byte owner。
- 让 browse/search、容量、cache、匿名访问、授权、发布与操作状态来自真实 API，不伪造数据。
- 桌面优先但窄屏无横向溢出，支持中文/英文、明暗主题、键盘和错误恢复。

## 共享信息架构

Repository Detail 使用 Overview、Artifacts、Scanning、Security、Access Grants、Retention、Tombstones、Jobs 等格式感知 Tab。只在 capability profile 声明真实可执行操作时显示或启用控制项。

列表和全局搜索跳转应携带稳定 Repository ID、格式、类型、规范 coordinate 与 digest；页面刷新可重建状态。Group 页面强调有序成员与 owner，Proxy 页面强调 upstream、egress、cache 和健康，Hosted 页面强调 publication 与 lifecycle。

## UI 中必须展示的概念

- Repository 类型、格式、状态、匿名策略和 effective access。
- Artifact Identity：协议所有的 coordinate + digest，不从文件名猜测。
- Asset：路径、size、checksum、media type 与复制命令。
- Proxy cache disposition、source member、TTL/negative state、quota/capacity。
- Lifecycle state、Tombstone 恢复窗口、Job 状态与错误。
- Scanner evidence、Quarantine 和独立 read-enforcement policy。

## Repository 目录树

Console 应补充目录树交互，但不能假装所有格式都存在物理文件夹。目录树是只读、格式感知的投影：Maven 可展示 `groupId -> artifactId -> version -> asset`，Raw 可展示路径分段；其他格式必须由专用 adapter 定义，不能由浏览器猜路径。

建议合同如下：

- `GET /api/v2/repositories/{repositoryId}/browse` 按可选的 opaque `parent` 返回直属子节点，并使用有界 `pageSize` 与 opaque `pageToken`。
- 节点显式区分 `directory`、`namespace`、`component`、`version`、`asset`；可操作叶子携带现有详情流程需要的规范 coordinate/path 与不可变证据。
- 返回子节点前先执行授权与匿名策略，各格式 adapter 负责生命周期可见性；Quarantine 读阻断仍在原生协议边界执行。未来 Group 结果还需携带来源成员和优先级；Proxy 只暴露自身已知缓存范围。
- 节点 ID 与 cursor 不由浏览器重建，并按适用范围签名绑定 Repository、format、parent、principal、过期时间和稳定排序位置。
- 合成目录不自动拥有删除语义。未来若支持子树清理，必须由服务端解析不可变身份、先展示有界 dry-run，再通过可审计异步 Job 执行。

首批已交付 Maven、Raw Hosted adapter 与 Proxy 本地缓存边界，同时验证语义层级和路径层级。现有列表/搜索继续保留，用于跨 Repository 发现与无障碍访问；Group provenance 和其他格式 adapter 仍是后续工作。

## Maven Proxy 浏览体验

Maven 应按 groupId/artifactId/version/build/asset 分层显示，而不是平铺对象键。SNAPSHOT 显示 timestamp/build number，并把 POM、主 JAR、classifier 与 Gateway 派生 checksum 归为同一 publication。

Proxy 结果必须区分已缓存可离线解析与仅上游 metadata；只有拥有本地已验证字节的结果才能进入 scan/distribution identity picker。Group 结果显示 owner，但不得泄露用户无权读取的成员。

## Proxy 分页 API

浏览采用服务端稳定排序、bounded page size 和 opaque cursor，不由 Console 载入全部结果再过滤。Filter、Repository、format、coordinate scope 必须进入 cursor 签名，跨查询复用返回 `invalid_page_token`。

初次 loading、empty、error 与 stale refresh 必须互斥。刷新失败保留旧数据并在其上显示错误；深链接超出第一页时服务端直接解析目标，不顺序翻页。

## Proxy 容量与存储

显示逻辑 Artifact 数、物理 byte、quota、可回收估计和最新 collection。Deduplicated object 只计一次物理容量；Repository charge 必须说明共享对象语义。趋势来自服务端或明确标识的本地样本，不能把即时快照伪装为历史。

## Proxy 运维

Settings 提供 upstream endpoint、allowlist、egress mode/protocol/host/port/noProxy 和脱敏 credential 状态，使用 `If-Match`。Connection Test 只返回 reachability、auth result 和 latency，不显示 secret 或错误 body。

Operations 显示 circuit、cache hit/miss/negative、最近失败和 collection；mutation 仅管理员可用，执行后有 Audit。清理不得跨 Repository，也不能绕过 grace/reference recheck。

## 匿名访问

### 使用场景

公开 release 下载、开源依赖和只读目录可启用匿名；发布、删除、晋级、cache mutation 与管理永不匿名。

### 策略模型

全局 gate、Group opt-in、最终 Repository/member opt-in 必须同时满足。全局开启不自动公开现有目标；关闭立即阻止全部匿名读取。

### Console 管理

管理员页面分开显示三个 gate 和潜在公开目标数量，变更前解释 blast radius。Repository 页面展示 local opt-in 与实际 effective public state；Group 显示每成员结果，不把部分可公开描述为整个 Group 无条件公开。

### API 与 UI 行为

使用版本化 policy 与 `If-Match`；公开 `/browse` 只显示 effective public target，提供搜索、format filter、source-type 说明和只读提示。未登录用户看不到 publish、Grant、Admin action。

### 安全默认

默认 deny。事件响应先关闭全局 gate，再检查 Audit 中 `actor=anonymous` 和 reason，不需要逐 Repository 紧急修改。

## Hosted / Proxy / Group 一致性

三个类型共享页面骨架、Search、Artifact table、Empty/Error/Loading 和 copy command，但操作不同：Hosted 拥有 publish/lifecycle；Proxy 拥有 upstream/cache；Group 只读并拥有 member ordering，不拥有 byte。

同一概念使用一致名称和 icon。禁止把 Proxy cache hit 写成 Hosted publication，或把 Group 的成员 Artifact 写成 Group-owned object。Action 根据 capability profile 和 authorization 双重限制。

## 实施清单

### 阶段 1：修复浏览可用性

- 确保 Repository/Group/Artifact 深链接、返回路径和刷新恢复。
- 修复长 coordinate、表格 overflow、empty/error/loading 和移动 Drawer。
- 增加格式专用 copy/install/pull 命令与明确 read-only 提示。

### 阶段 2：后端浏览与分页

- 服务端 cursor、filter、sorting 和权限过滤。
- 跨 Repository 搜索与精确 digest/coordinate lookup。
- Group owner 与 Proxy cache state 使用同一解析来源。
- 新增直属子节点 browse contract，使用 opaque node ID/cursor。
- 已实现 Maven、Raw adapter 和 Proxy 本地缓存边界；Group provenance 待实现。
- Console 增加 lazy、键盘可访问的目录树，同时保留列表/搜索。

### 阶段 3：容量与存储

- Repository/format capacity、quota、dedup 与 collection 状态。
- 有证据的数据可视化；无时序 API 时明确 local sample 边界。

### 阶段 4：Proxy 运维

- Upstream/allowlist/egress hot edit、连接测试、健康与 circuit。
- Cache collection、失败解释、审计与有界指标。

### 阶段 5：匿名访问

- 全局和 local gate、public count、effective state、公开 catalog。
- GET/HEAD allow 与写操作 deny 的真实协议和浏览器 E2E。

### 阶段 6：文档与产品文案

- 统一 Repository/Hosted/Proxy/Group/Artifact/Asset 领域术语。
- 双语命令、风险说明、恢复步骤和协议限制。
- Roadmap/preview 明确标识，不把未公开格式写成已交付。

### 阶段 7：审计运维

- 服务端 filter、时间范围、cursor、CSV 和 Repository deep link。
- 后续可增加 saved query、Webhook/job 全局反馈和 support bundle。

## 完成定义

- 所有显示数据来自真实 API，capability 与操作一致。
- Desktop/mobile、light/dark、中英文、keyboard、reduced-motion 在变更范围内验证。
- 深链接刷新稳定，超长坐标不破坏布局，错误可恢复。
- 匿名、授权、cache、lifecycle 和安全状态有文字、icon 与原因，不只靠颜色。
- OpenAPI client、component test、browser E2E 和相关协议 fixture 同步通过。
