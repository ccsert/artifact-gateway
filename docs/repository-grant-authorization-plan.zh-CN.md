# Repository Grant 运行时授权计划

[English](repository-grant-authorization-plan.md) | [文档索引](README.zh-CN.md)

## 目的

Repository Grant 原本是版本化持久管理数据，本文将其提升为 Hosted Repository 的授权来源，不改变认证机制或协议错误契约。迁移增量进行；只有操作员显式管理 Grant 后，该 Repository 才离开旧静态策略。

## 授权模型

认证建立 `Principal`，授权为该 principal 评估一个 Repository 操作，并返回 allow/deny、稳定 source/reason 供审计与指标使用。Evaluator 不写 HTTP 响应也不解析协议凭证。

输入包括 principal 与 administrator 标记、目标 Repository ID/name/format、`read|write|intelligence|admin` 操作，以及 Grant set 和 legacy reader/writer pattern。

管理员始终允许，保留 `GATEWAY_ADMIN_TOKEN` 和 OIDC admin 的 bootstrap/recovery 路径。非管理员的已管理 Grant 对目标 Repository 具有权威性：

- `repositories:admin` 包含 write、read、intelligence；
- `repositories:write` 包含 read；
- `repositories:read` 只允许读取；
- `repositories:intelligence` 只允许写签名、SBOM、provenance、license、vulnerability 等元数据，不隐含读取、发布、删除或管理。

Service Account 没有全局角色，只通过显式 Grant 访问，凭证轮换不改变稳定 `service-account:<id>`。独立 API Key 保留自己的全局角色，不能与 Service Account credential 混同。Grant 是精确 principal 匹配且不跨 Repository。

未管理时保留旧协议静态行为与已有 wildcard 语义；缺少 reader map 时保留本地开发的 unrestricted-read。默认 Grant set 版本为 1；任何成功 `ReplaceRepositoryGrants`（包括空数组）把版本提升到 1 以上，作为“已管理”标记。显式空集拒绝所有非管理员。

## 操作映射

| 操作 | Scope | 主要路由 |
| --- | --- | --- |
| Read | `repositories:read` | Maven download、OCI fetch、Raw GET/HEAD、Conan read-through |
| Write | `repositories:write` | Maven publication、OCI upload/manifest/delete、Raw PUT/DELETE |
| Intelligence | `repositories:intelligence` | 对可见 Artifact 写安全情报，不获得发布/删除/管理能力 |
| Admin | `repositories:admin` | 替换 Grant 和后续 Repository 级管理变更 |

V2 将全局发现和已知资源操作分离。有 read Grant 的 principal 可读取已知 Repository detail、retention、artifact 和 publish session；write 可执行 Repository 内 mutation；admin 管 Grant。

Repository list、Audit list、Repository/Group lifecycle 和全局管理发现仍只允许管理员。Scoped Grant 不是 discovery Grant，不能枚举 Repository、Group、Audit 或分页状态，避免空过滤列表成为存在性 oracle。

## Group 与 Proxy

只有 member 持久化 `repositoryId` 且指向 active、格式匹配 Repository 时，才能评估该成员 Grant；运行时不得从名称、Group、路径或 endpoint 推断绑定。

Conan 已显式绑定。OCI、Maven、Raw legacy member 尚未持久绑定时继续使用旧静态策略，这是兼容行为，不是已绑定 member 的授权 fallback。

候选算法：匿名且 Group/member 未开启时在缓存前排除；已认证、已绑定且 Grant 允许时才访问该候选缓存/源；拒绝或 lookup 失败时记录有界判定并跳过；未绑定 legacy member 沿用旧策略。

若请求只因已绑定成员均被拒绝而耗尽，应返回格式现有 access-denied，而非 `404`。后续获准成员仍可成功，但不能获取、缓存或在响应中命名被拒绝成员。正负缓存只有在其来源成员通过同一授权检查后才可用；拒绝永不缓存。

OCI 终态为 Registry `403 DENIED`，Maven/Raw/Conan 保留现有 `403`。Conan 的 Repository 可作为 read-through 授权目标；未绑定 member 不从名称或 endpoint 推断。

## 协议契约

Maven/Raw 保留现有 Basic challenge/status，OCI 保留 Registry Bearer challenge，管理路由保留 `application/problem+json` 和未认证 `access_denied`。授权 source/reason 只写审计，不通过 not-found 或 principal-specific 错误泄露。

管理员 `GET /api/v2/audits` 可看到可选 `authorizationSource/Reason`；只有进入 Repository 授权判定时出现。客户端应接受未来有界值并把缺失解释为“未发生 Repository 授权判定”。V1 Audit 响应不变。

## 指标

`artifact_gateway_repository_authorization_denials_total` 只计已管理 Grant 的拒绝，label 限制为 format、固定 source `repository_grants` 和 reason `scope_not_granted|grant_lookup_failed`。

严禁 actor、Repository、member、path、coordinate、request/trace ID、endpoint、upstream host 作为 label。旧静态/未认证拒绝保留原指标，不混入该计数。

## 推进与回滚

先实现 hierarchy、legacy fallback、空集和 metadata 单测，再按 Maven、OCI、Raw、Conan 逐协议接入并加 allow/deny E2E；补齐 Memory/PostgreSQL、一致替换、审计和有界指标；最后评审 scoped management。

回滚只需停用运行时 evaluator，协议恢复静态策略而不重写 Grant。重新启用会立即应用持久 Grant，首版禁止授权缓存。
