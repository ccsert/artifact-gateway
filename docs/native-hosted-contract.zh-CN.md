# ADR：Native Hosted 领域与管理 API 契约

[English](native-hosted-contract.md) | [文档索引](README.zh-CN.md)

状态：已接受的架构契约，最初用于 Native Hosted Platform V3 规划。当前运行时已超出本文记录的格式清单。

本文用于元数据权威、对象生命周期、幂等和事务规则；当前格式与端点以[协议兼容性基线](protocol-compatibility.zh-CN.md)为准。清单冲突时，以可执行测试和兼容基线为准。

## 范围与术语

**Repository** 是恰好一个格式的持久策略、授权和保留 namespace。**Group** 是同格式 Repository 的有序只读视图，不拥有字节。**Member** 是 Group 与 Repository 的成员关系，position 唯一。**Artifact coordinate** 是格式定义的不可变身份，如 Raw path、OCI `repository@sha256:<digest>` 或 Maven GAVCE。

Hosted 写契约按格式区分。Raw 使用标准 `PUT /raw/{repository}/{path}`，对象引用提交后可见。OCI 直接使用 Registry V2 upload/blob/manifest/tag。Maven 默认成功 PUT 直接发布；严格模式下服务端创建 staging session，并由 `POST .../coordinates/{coordinate}:commit` 显式切换可见性。Conan 使用当前兼容基线定义的管理发布契约。

Maven session 在 committed/aborted/expired 后不可复用。`Idempotency-Key` 对认证 actor 和目标保持 24 小时；同 key 不同 payload 返回 `409 idempotency_conflict`。

Group 成员只由 Group 管理。`/groups/{groupId}/members` 替换必须提供完整有序列表与 `If-Match`；成员必须存在、格式一致、不重复且 position 从 0 连续。成员边与 Group version 在一个 PostgreSQL 事务更新，Repository 不提供 member write endpoint。

以下细则主要覆盖原始 Raw、OCI、Maven；Conan、npm、PyPI、Go、APT 由当前兼容基线及测试拥有。Group/Proxy 服从当前 overlay，不服从过期的 V2 清单陈述。

## 元数据、对象生命周期与事务

PostgreSQL 是 Repository、Group、member、Grant、Retention Policy、Maven session、coordinate、object reference 和 audit 的权威。S3 兼容存储只保存不可变字节，key 由服务端按 digest 派生，绝不接受客户端 object key。

PostgreSQL advisory lock 和事务 idempotency record 提供短期协调；连接丢失会释放 lock，受影响并发操作必须失败关闭，不得改变持久事实。

PostgreSQL 无法加入 S3 事务，所以对象上传先于 metadata promotion。Raw/OCI 先记录格式 object intent，再上传 digest object、验证 size/digest，最后写可见 path/blob/manifest/tag。Maven strict commit 锁 session 与 coordinate，在一事务插入 reference、切换可见并标记 committed。

Reader 只能经 visible coordinate/path 和 committed reference 读取。未提交对象不可达；已提交 coordinate 不指向未验证对象。不可变 coordinate 覆盖返回 `409 coordinate_exists`；OCI tag 可原子移动到新不可变 manifest。删除写 Tombstone 并移出解析，Retention 只评估 visible coordinate，不扫描裸 S3 列表。

Hosted Repository 默认 Retention Policy 为 version 1、`keepDays=30`、`minimumVersions=1`。PUT 同时要求 representation version 与 `If-Match`，成功递增，过期返回 `412`。配置不直接删除内容；scheduler 为支持格式创建持久任务。

版本分组按格式进行：Maven GA、OCI image name、Conan reference、npm package、PyPI normalized project、Go canonical module；Raw 无版本组，按 path last update。RE2 selection/protection 匹配逻辑 cleanup unit。任务写可恢复墓碑，字节由格式 collector 延迟处理。

Collector 在至少 24 小时宽限期后运行，持有对象 lock 并在 S3 delete 前重查可见引用。Go 先写 `collecting` fence，确保 S3 已删但 metadata 未终结时 restore 也失败关闭；共享可见引用保留物理字节，但过期 Repository 引用停止计费。失败可重试，无 API 暴露 object key。

Go 晋级/复制以完整 `.info`/`.mod`/`.zip` 为发布单元。管理身份使用 `module@version`+ZIP digest，但准入和 Worker 最终发布检查三个 digest。晋级复用已验证对象；复制对每表示持久 checkpoint 到目标专用 key，最后一事务公开。Proxy 不可作为目标。

## 授权、错误、分页与兼容

管理端点要求 Bearer。全局 Repository list/create 和 Hosted Group lifecycle 只允许管理员。对已知 Repository：read 可读 detail/retention/session/artifact；write 可 disable/发布/删除；admin 可改 Grant/Retention；独立 intelligence 只可写可见 Artifact 的安全情报。

Scope 继承为 admin→write→read，admin 还包含 intelligence；独立 intelligence 不授予读取、发布、删除或管理。写 intelligence 前必须验证 coordinate/digest 当前可见，拒绝 orphan identity。

协议认证映射到同一 principal 与策略。Raw/Maven 支持 resolver Basic 或 Gateway Bearer，OCI 使用 Registry token exchange。匿名只在 Group 与最终 Repository 均允许时放行 GET/HEAD，否则返回协议 challenge。生成客户端不能从管理 API 的 Bearer 默认推断协议路由安全声明。

管理失败统一 `application/problem+json`：`code` 稳定可编程，`message` 安全可读，`requestId` 关联审计。写入拒绝未知字段，响应可增加字段；V2 内不改变既有字段语义/类型，不兼容变化进入 V3。

Collection 使用不透明 URL-safe `pageToken` 和 `pageSize`（默认 50、最大 200），按不可变 ID 排序。Token 保存位置而非数据库 offset，15 分钟过期或跨 endpoint 使用返回 `400 invalid_page_token`。

## Native 格式与协议契约

协议路由独立于管理 API。Raw/OCI 只读 committed reference；Maven 默认直接发布验证后的 PUT，strict 模式下 commit 前不可读。不存在、staged、expired、aborted、deleted 均返回 `404`。

**Raw：** PUT canonical 多段 path，GET/HEAD 在 reference commit 后读取；拒绝空、directory、dot/dot-dot 和编码绕过。读取支持 HEAD、Digest、ETag 和单 range，经对象存储流式接口。未认证返回 `401` challenge。

Raw/OCI resumable PATCH 将每段保存为 offset-addressed 不可变 chunk，不重写历史。完成时验证连续、只流式组装一次、验证 digest、提交可见并清理 chunk。升级期间允许旧 cumulative prefix 完成；持久 reclaim 清理完成、取消、过期 session 的剩余 chunk，保留 PostgreSQL 记录。

**OCI：** manifest 可按 digest/tag GET，返回 `Docker-Content-Digest`；blob 按 digest 读取。写入支持 upload session、PATCH resume、mount、manifest PUT、tag move、cancel 和 media type negotiation。Tag list 有界字典序分页，认证使用 Bearer challenge。`_catalog` 和 referrer 属当前兼容目标；不支持直接 blob 删除。

**Maven：** GET 读取 canonical multi-segment asset path；snapshot/repository metadata 由可见坐标生成，不接受客户端 mutable metadata 为权威。

### Maven 发布策略

默认 `mavenStrictPublication=false`：每个验证后的 primary PUT 直接发布，兼容未修改的 `mvn deploy`/Gradle `publish`。服务端派生 asset、digest、size、coordinate，先记录 intent 再上传，并在 PostgreSQL 事务发布；成功 `201` 后立即可读。

选择 `mavenStrictPublication=true` 时，成功 PUT 追加到 publisher 在该 Repository+GAV 的唯一 open session，直到 CI wrapper/插件调用 `coordinate:commit` 才可见。默认直接仓库调用 commit 返回 `409 publication_commit_disabled`。

Maven/Gradle 没有跨 POM、JAR、classifier、checksum、metadata 的标准完成信号；metadata、sidecar、最后请求或 quiet period 都可能缺失、重排或重试。直接模式以 partial publication 风险换取零迁移成本；strict 模式以专用集成换取坐标级原子可见。两者都不承诺跨多个 GAV 的 Reactor 原子事务。

客户端 checksum 只作 assertion，由 Gateway 验证或丢弃并从主对象生成。客户端 metadata 为兼容 no-op；可读 metadata 从可见坐标生成。

Strict commit caller 必须是 session publisher 或窄授权 release principal。Expected-name 只断言完整性，不能提供 digest、size、metadata、key 或可见性。事务锁 session/coordinate，验证 POM 与资产、派生 checksum/metadata、拒绝冲突并一次公开。

Strict commit 对 publisher/repository/coordinate/key 保持 24 小时幂等；相同重试返回 committed，不同 expected set 返回冲突。缺 POM、未验证对象、异常 POM、配额或坐标冲突都保持不可见；过期 session 返回 `409 session_closed`。

Release coordinate 不允许不同字节。Snapshot 创建不可变 timestamped build 并推进生成 metadata。发布后回滚使用墓碑或新晋级，不原地修改或立即删 S3。详见 [Maven Hosted 发布](maven-hosted-publication.zh-CN.md)。

PostgreSQL 拥有 session、inventory、intent、reference、idempotency 和 commit lock；S3 只存 digest byte。失败 S3 不可 commit，失败 metadata promotion 留下不可达可重试 staged byte。Collector 只 claim 24 小时以上无引用 intent，使用 `FOR UPDATE SKIP LOCKED` 并重查引用。

### CCS-44 顺序

1. PUT adapter 从 canonical path 派生 coordinate，复用 publisher open session，追加 verified intent；commit 路由优先于 catch-all。
2. 统一 `CommitMavenCoordinate` 模块拥有 session lock、POM 解析、expected 校验、幂等、冲突、metadata/checksum 与事务。
3. Maven extension/Gradle plugin 与 opt-in flag 同步交付；严格模式启用前先配置集成，GET 保持标准兼容。
4. 黑盒覆盖 POM/JAR/sidecar 部分失败、重试、并发 commit、session expiry/restart、S3 成功/PostgreSQL 失败，以及 24 小时 collector。

非目标：猜测未修改客户端完成事件、让客户端 metadata 成为权威、跨 coordinate 事务或 write path runtime fallback。

## Native Hosted 完成边界

Native handler 只用 PostgreSQL metadata 与对象存储字节，不调用外部 Hosted adapter。Legacy Group 只用于外部 Proxy 读取，不能成为 native fallback。Repository 删除是逻辑 metadata 删除，回收保持可追踪直到确认无 committed reference。

## 可执行契约

`api/openapi/native-hosted.yaml` 是可编辑源，`native-hosted-v1.json` 是生成 bundle。`go test ./contracts` 验证两种形式、引用、Group 成员和 Raw/OCI/Maven 生命周期夹具。`make openapi-check` 重建 bundle/客户端并运行门禁，CI 在全量测试前执行。
