# Repository Grant 运行时授权计划

[English](repository-grant-authorization-plan.md) | [文档索引](README.zh-CN.md)

## 目的

Repository Grant 原本是版本化持久管理数据，本文将其提升为 Hosted Repository 的授权来源，不改变认证机制或协议错误契约。迁移增量进行；只有操作员显式管理 Grant 后，该 Repository 才离开旧静态策略。

## 授权模型

认证建立 `Principal`，授权为该 principal 评估一个 Repository 操作，并返回 allow/deny、稳定 source/reason 供审计与指标使用。Evaluator 不写 HTTP 响应也不解析协议凭证。

输入包括 principal 与 administrator 标记、目标 Repository ID/name/format、`read|write|intelligence|admin` 操作，以及 Grant set 和 legacy reader/writer pattern。

管理员始终允许，保留 `GATEWAY_ADMIN_TOKEN` 和 OIDC admin 的 bootstrap/recovery 路径。任何主体都走同一条固定顺序：`none` 账号状态先被拒绝，管理员身份放行，覆盖该操作的全局角色（`RoleAllows(principal.Role, operation)`）放行，之后才查询该主体的 Repository Grant，最后由旧静态策略兜底。因此只有管理员等级能越过 Grant：此前覆盖全部 Repository 的全局 `reader`/`writer` 已被移除，而 `member` 本身不附带任何仓库能力，普通账号的可达范围完全由按仓库 Grant 决定：

- `repositories:admin` 包含 write、read、intelligence；
- `repositories:write` 包含 read；
- `repositories:read` 只允许读取；
- `repositories:intelligence` 只允许写签名、SBOM、provenance、license、vulnerability 等元数据，不隐含读取、发布、删除或管理。

Service Account 没有全局角色，只通过显式 Grant 访问，凭证轮换不改变稳定 `service-account:<id>`。独立 API Key 保留自己的全局角色，不能与 Service Account credential 混同。Grant 是精确 principal 匹配且不跨 Repository。Grant 还可携带可选 resource prefix（`migrations/000048_repository_grant_resource_prefixes.sql`），把授权限定在 identity 以该前缀开头的 resource 上；匹配由 `grantMatchesResource` 实现，空前缀匹配该 Repository 的所有 resource。

Authorization Role 与 Authorization Template 是基于 Grant 的可复用管理对象。Role（`migrations/000091_authorization_roles.sql`）是命名的 `repositories:*` scope 集合：在 Grant 编辑器中选择一个 Role 会把它的 scope 复制成显式快照，之后编辑 Role 不会静默改变已持久化的判定。Template（`migrations/000083_authorization_templates.sql`）是可复用的 Grant 集合：把它应用到某个 Repository 会用模板规则替换该 Repository 的 Grant set，替换前按目标 Repository 格式校验规则，并推进标记“已管理”的存储版本。Role 与 Template 的管理仅限管理员。

未管理时保留旧协议静态行为与已有 wildcard 语义；缺少 reader map 时会拒绝未匹配的调用方，除非用 `GATEWAY_LEGACY_READ_DEFAULT=allow` 恢复 0.4 之前的姿态。默认 Grant set 版本为 1；任何成功 `ReplaceRepositoryGrants`（包括空数组）把版本提升到 1 以上，作为“已管理”标记。显式空集会拒绝所有走到 Grant 判定步骤的主体，撤销那些只能靠 Grant 获得权限的非管理员。

## 操作映射

| 操作 | Scope | 主要路由 |
| --- | --- | --- |
| Read | `repositories:read` | Maven download、OCI fetch、Raw GET/HEAD、Conan read-through |
| Write | `repositories:write` | Maven publication、OCI upload/manifest/delete、Raw PUT/DELETE |
| Intelligence | `repositories:intelligence` | 对可见 Artifact 写安全情报，不获得发布/删除/管理能力 |
| Admin | `repositories:admin` | 替换 Grant 和后续 Repository 级管理变更 |

V2 将全局发现和已知资源操作分离。有 read Grant 的 principal 可读取已知 Repository detail、retention、artifact 和 publish session；write 可执行 Repository 内 mutation；admin 管 Grant。

`GET /api/v2/repositories` 现在按权限过滤而非仅限管理员：管理员可见全部 Repository，其他已认证主体只能看到其全局角色或 Grant 允许读取的 Repository，待审批（`none`）账号被拒绝。过滤在服务端按调用方自身的有效读权限执行，列表不会包含调用方无权读取的 Repository。Audit list、Repository/Group lifecycle 及其余全局管理发现路由仍只允许管理员。

`GET /api/v2/repositories/{id}` 对不存在的 Repository 仍返回 `404`、对存在但不可读的返回 `403`，因此已知标识符的调用方仍可确认其存在。使该响应与列表保持一致由 effective-access 相关工作跟进，本计划暂不声称已实现。

## Group 与 Proxy

只有 member 持久化 `repositoryId` 且指向 active、格式匹配 Repository 时，才能评估该成员 Grant；运行时不得从名称、Group、路径或 endpoint 推断绑定。

Conan 已显式绑定。OCI、Maven、Raw legacy member 尚未持久绑定时继续使用旧静态策略，这是兼容行为，不是已绑定 member 的授权 fallback。

候选算法：匿名且 Group/member 未开启时在缓存前排除；已认证、已绑定且 Grant 允许时才访问该候选缓存/源；拒绝或 lookup 失败时记录有界判定并跳过；未绑定 legacy member 沿用旧策略。

若请求只因已绑定成员均被拒绝而耗尽，应返回格式现有 access-denied，而非 `404`。后续获准成员仍可成功，但不能获取、缓存或在响应中命名被拒绝成员。正负缓存只有在其来源成员通过同一授权检查后才可用；拒绝永不缓存。

OCI 终态为 Registry `403 DENIED`，Maven/Raw/Conan 保留现有 `403`。Conan 的 Repository 可作为 read-through 授权目标；未绑定 member 不从名称或 endpoint 推断。

## 协议契约

Maven/Raw 保留现有 Basic challenge/status，OCI 保留 Registry Bearer challenge，管理路由保留 `application/problem+json` 和未认证 `access_denied`。授权 source/reason 在 Maven、OCI、Raw、Conan（legacy 与 native）以及管理路由上写入审计；npm、PyPI、Go、APT 的拒绝只计入有界计数器、不带判定字段，这部分拆为独立工作。二者都不会通过 not-found 或 principal-specific 错误泄露。

管理员 `GET /api/v2/audits` 可看到可选 `authorizationSource/Reason`；只有进入 Repository 授权判定时出现。客户端应接受未来有界值并把缺失解释为“未发生 Repository 授权判定”。V1 Audit 响应不变。

## V1 兼容面

`/api/v1/**` 处理器仍是平台管理员操作，**不参与平台/仓库两档拆分**。它们早于按仓库权限模型，且 V1 Group 的成员可能完全没有仓库绑定，因此没有任何信息能把它归到某个管理员所持有的仓库上。两档模型落在 v2 面：hosted 仓库、hosted 群组，以及它们之上的管理视图。需要按仓库管理的 V1 客户端应迁移到 `/api/v2`。

## 判定取值

每个仓库授权判定都会写明是**哪一级**作出的决定，因此 effective-access 解释面板与审计记录说明的是"谁决定的"，而不只是"允许还是拒绝"。以下是当前的**有界取值**；消费方必须容忍后续新增的有界值，而字段缺失表示该请求从未进入仓库授权判定。

`authorizationSource`：

| 取值 | 由谁产生 |
| --- | --- |
| `administrator` | 平台管理员身份：`admin` 等级、管理员 subject 白名单，或静态管理员 token |
| `role` | 全局账号等级，具体哪一级由 reason 指明 |
| `repository_grants` | 按仓库授权集合——已标记托管的，或显式命名该主体的 |
| `legacy_static` | `GATEWAY_REPOSITORY_READERS` / `GATEWAY_REPOSITORY_WRITERS` 中配置的读写模式 |
| `legacy_protocol` | 原生协议兜底：放行任何已认证主体，适用于 npm、PyPI、OCI、Raw、APT |

`authorizationReason`：

| 取值 | 含义 |
| --- | --- |
| `administrator` | 由管理员身份判定 |
| `role_admin`、`role_member` | 由所指明的全局等级判定。只有 `role_admin` 会放行，其余等级继续落到授权集合 |
| `scope_granted` | 有授权同时匹配主体、操作与资源 |
| `scope_not_granted` | 授权集合生效但未匹配 |
| `grant_lookup_failed` | 授权集合读取失败，按拒绝处理 |
| `read_pattern_granted`、`write_pattern_granted` | 命中 legacy 静态模式 |
| `authenticated` | 原生协议兜底放行了已认证主体 |
| `repository_anonymous_read_enabled`、`repository_anonymous_read_disabled`、`global_anonymous_access_disabled`、`repository_not_active` | effective-access 答复补充的匿名读解释；这些不会进入拒绝计数器 |

拒绝计数器**有意收得更窄**：它只统计授权判定阶段，因为 label 必须收敛到运维可告警的小集合。快照、隔离区与密码相关判定使用各自的 source，它们不是仓库授权判定，不在此列。

## Console 能力推导

会话载荷维持现有形状：`/auth/session` 与 `/api/v2/identity` 返回 `{actor, kind,
role?, administrator, oidc?}`，**有意不携带能力集**。两档权限在 Console 内、由会话已有的数据推导，且只在一个模块里推导（`console/src/lib/authorization.ts`），导航项、路由守卫与仓库页面都只读它：

- `platformCapabilities(identity)`：把 `administrator` 变成 `platformAdmin`，把 `role === "none"` 变成 `pending`，两者再合成 `browseRepositories`。
- `repositoryPermissions(access)`：把单个仓库的 effective-access 答复变成 `read`、`write`、`administer`、`intelligence`。

仓库档不能放在会话里：它是**按仓库**回答的，而 effective-access 答复本身就是服务端实际执行的那份权威答案。会话上的能力集会是同一个按仓库判定的第二份副本，且在还没有任何仓库被指明时就已签发，两份可能互相矛盾。平台管理员无论该仓库的答复如何都保留全部仓库界面，因此 Console 自身的管理不依赖该答复。

## 指标

`artifact_gateway_repository_authorization_denials_total` 只计已管理 Grant 的拒绝，label 限制为 format、固定 source `repository_grants` 和 reason `scope_not_granted|grant_lookup_failed`。

严禁 actor、Repository、member、path、coordinate、request/trace ID、endpoint、upstream host 作为 label。旧静态/未认证拒绝保留原指标，不混入该计数。

## 推进与回滚

先实现 hierarchy、legacy fallback、空集和 metadata 单测，再按 Maven、OCI、Raw、Conan 逐协议接入并加 allow/deny E2E；补齐 Memory/PostgreSQL、一致替换、审计和有界指标；最后评审 scoped management。

回滚只需停用运行时 evaluator，协议恢复静态策略而不重写 Grant。重新启用会立即应用持久 Grant，首版禁止授权缓存。
