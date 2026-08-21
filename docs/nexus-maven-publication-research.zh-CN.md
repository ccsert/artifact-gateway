# Nexus Repository Maven Hosted 发布行为研究

[English](nexus-maven-publication-research.md) | [文档索引](README.zh-CN.md)

> 调研日期：2026-08-21
>
> 调研范围：Sonatype Nexus Repository 3 普通 Maven Hosted、Nexus Repository Pro Staging，以及标准 Maven/Gradle 发布客户端。
> 证据范围：只引用 Sonatype 官方文档、Sonatype 官方 GitHub 源码、Apache Maven 官方文档和 Gradle 官方文档。

## 结论摘要

1. **普通 Nexus Maven Hosted 不要求额外的 finalize/commit。** Sonatype 官方配置示例直接让 `mvn clean deploy` 向 `maven-releases` 或 `maven-snapshots` 发布，没有坐标级提交调用。
2. **普通 Hosted 的成功 PUT 是逐资产生效的。** Nexus Repository 3 的请求处理器在 PUT 中直接保存该路径并返回 `201 Created`；GET/HEAD 直接按同一路径查询已保存资产。因此一个成功 PUT 的 POM、JAR 或 sidecar 会立即成为该 Hosted 仓库中的可读取资产。
3. **普通 Hosted 没有坐标级原子发布。** Maven/Gradle 会分别上传 POM、主制品、classifier、checksum 和 metadata；Nexus 的开源实现也是逐请求保存资产，没有等待某个坐标的完整资产集合，也没有标准 finalize 端点。若发布在中途失败，已经成功 PUT 的资产可能已经可见。
4. **Nexus Repository Pro Staging 与普通 Hosted 是两层不同能力。** 当前 Nexus Repository 3 Staging 使用多个 Hosted 仓库、component tag 以及 move/delete REST API，把一组制品从开发/测试仓库晋升到生产仓库。它不是在普通 Maven PUT 后执行一个坐标级 commit。
5. **Nexus Repository 3 不应与 Nexus Repository 2 的经典 close/release 工作流混淆。** Nexus 2 使用“每个 build 一个动态 staging repository”的模型；Nexus 3 改为“每个 build 一个 tag，固定数量 Hosted 仓库之间移动”的模型，两者不兼容。
6. **Artifact Gateway 已据此采用“默认直接、严格可选”。** Maven Hosted 默认与普通 Nexus Hosted 一样逐个公开经过验证的成功 PUT；`mavenStrictPublication=true` 时才要求 Gateway 坐标提交。

## 1. 普通 `mvn deploy` 是否需要额外 commit

不需要。

Sonatype 的 [Maven Repositories](https://help.sonatype.com/en/maven-repositories.html) 文档要求在 POM 的 `distributionManagement` 中直接配置 Hosted 仓库 URL，并明确给出 `mvn clean deploy` 作为完整构建和上传命令。该普通 Hosted 流程没有额外的 finalize、close 或 commit 调用。

Apache Maven 的 [Maven Deploy Plugin](https://maven.apache.org/plugins/maven-deploy-plugin/) 说明 `deploy:deploy` 会发布主制品、POM 和 attached artifacts，并由插件负责更新仓库所需的 metadata 和 checksum。这里的“负责”是客户端按 Maven repository layout 发起一组独立上传，不是服务端坐标事务。

Maven Deploy Plugin 暴露的 [`deployAtEnd`](https://maven.apache.org/plugins/maven-deploy-plugin/deploy-mojo.html#deployAtEnd) 可以把多模块 reactor 的部署推迟到构建末尾：如果构建在进入部署前失败，则不发布 reactor 项目。但该参数没有定义服务端 finalize，也不能让已经发出的多个 HTTP 上传变成一个远端原子事务。

Gradle 的 [`maven-publish`](https://docs.gradle.org/current/userguide/publishing_maven.html) 官方文档也明确说明它兼容 Maven Deploy Plugin 使用的标准和协议，并列出 Sonatype Nexus 为兼容目标。普通 `publish` 任务同样没有 Nexus 专属 finalize 步骤。

因此准确结论是：

```text
mvn deploy / gradle publish
        ↓
分别上传 POM、JAR、classifier、checksum、metadata
        ↓
每个成功请求直接进入目标 Hosted 仓库
        ↓
客户端在全部上传成功后结束 publish/deploy task
```

最后一步只是客户端任务成功结束，不是 Nexus 收到一个“该坐标已经完整”的协议消息。

## 2. 每个 PUT 后资产是否立即可见

是，普通 Hosted 采用逐路径立即保存和读取的行为。

Sonatype 官方 GitHub 中 Nexus Repository 3.93.2 的 [`MavenContentHandler`](https://github.com/sonatype/nexus-public/blob/ed94b05e53eff451c4b617ef31d09e8e9f066365/public/common/components/formats/nexus-repository-maven/src/main/java/org/sonatype/nexus/content/maven/internal/recipe/MavenContentHandler.java#L57-L92) 显示：

- PUT 调用 `storage.put(mavenPath, payload)`，完成后立即返回 `201 Created`；
- GET/HEAD 调用 `storage.get(mavenPath)`，找到资产就直接返回 `200 OK`；
- 两条路径之间没有 staging status 或 published flag 判断。

对应的 [`MavenContentFacetImpl`](https://github.com/sonatype/nexus-public/blob/ed94b05e53eff451c4b617ef31d09e8e9f066365/public/common/components/formats/nexus-repository-maven/src/main/java/org/sonatype/nexus/content/maven/internal/recipe/MavenContentFacetImpl.java#L227-L269) 在 PUT 中摄取 Blob、验证 metadata，然后创建或获取 Maven component 并保存当前资产。其 [`saveAsset`](https://github.com/sonatype/nexus-public/blob/ed94b05e53eff451c4b617ef31d09e8e9f066365/public/common/components/formats/nexus-repository-maven/src/main/java/org/sonatype/nexus/content/maven/internal/recipe/MavenContentFacetImpl.java#L332-L352) 对当前路径执行 `blob(blob).save()`；GET 则直接查询同一路径资产。

由此可以得出以下受证据约束的推论：

- 如果 `demo-1.0.pom` PUT 成功，而后续 `demo-1.0.jar` 上传失败，POM 已经可以从普通 Hosted URL 读取；
- 如果 JAR 先成功、POM 后失败，JAR 也可能单独可见；
- component 数据库记录可以在第一个带坐标的资产保存时创建，不代表坐标的预期文件已经全部到齐。

“立即可见”是指 PUT 成功返回后，对相同 Hosted 仓库路径的后续 GET 可以读取；这不等同于对跨地域复制、外部代理缓存或搜索索引可见性的时延作出保证。

## 3. Nexus 普通 Hosted 是否提供坐标级原子发布

没有找到这样的协议或实现保证，开源请求路径反而明确显示它是逐资产事务。

普通 Maven Repository 协议的远端对象是一个个路径，例如：

```text
com/example/demo/1.0/demo-1.0.pom
com/example/demo/1.0/demo-1.0.jar
com/example/demo/1.0/demo-1.0-sources.jar
com/example/demo/1.0/demo-1.0.pom.sha1
com/example/demo/1.0/demo-1.0.jar.sha1
com/example/demo/maven-metadata.xml
```

Apache Maven 官方文档把这些内容描述为 deploy plugin 需要部署和更新的多个对象；Sonatype 普通 Hosted 接口则逐 PUT 保存。两边都没有定义“声明预期资产集合，再一次性发布整个 GAV”的标准握手。

Nexus 的 [Deployment Policy](https://help.sonatype.com/en/configurable-repository-fields.html#deployment-policy) 可以禁止重复部署、允许覆盖或把 Hosted 设为只读，但它控制的是是否允许写入/重写，并不提供坐标完整性事务。

所以 Nexus 普通 Hosted 的兼容性取舍是：

- 优点：原生兼容未经修改的 Maven、Gradle 和其他 Maven repository 客户端；
- 代价：网络或客户端在多个上传之间失败时，目标仓库可能短暂或持续包含不完整坐标；
- 缓解方式：客户端构建顺序、`deployAtEnd`、禁止 release redeploy、CI 检查，以及使用隔离仓库和 Staging，而不是普通 Hosted 内部的坐标 commit。

## 4. Nexus Repository Pro Staging 的实际模型

Sonatype 当前 [Staging](https://help.sonatype.com/en/staging.html) 文档把 Nexus Repository 3 Pro Staging 定义为三个 building blocks：

1. Hosted repositories：制品先上传到某个 Hosted 仓库；
2. Component tags：把一次 build 的 component/assets 标记为逻辑组；
3. REST endpoints：在 Hosted 仓库之间 move，或删除匹配的一组 component。

官方 [Staging Concepts](https://help.sonatype.com/en/staging-concepts.html) 给出的典型 Maven 流程是：

```text
maven-dev-hosted
    ↑ 普通上传，可选 build tag
    │
    ├─ CI 验证成功 ── move API ──→ maven-uat-hosted
    │                                  │
    │                                  └─ move API ──→ maven-prod-hosted
    │
    └─ CI 验证失败 ── delete API
```

消费者通常只通过与生命周期阶段对应的 Group 仓库读取。这样，上传到 `maven-dev-hosted` 的半成品即使在该仓库内逐资产可见，也不会自动出现在生产消费者使用的 `maven-prod` Group 中。

Nexus Repository 3 Pro 的 [`nxrm3-maven-plugin`](https://help.sonatype.com/en/nexus-repository-maven-plugin.html) 可以替换 Maven Deploy Plugin，在上传时为制品分配 tag，并提供：

- `nxrm3:staging-deploy`：上传并关联 tag；
- `nxrm3:staging-move`：按 tag 从 source Hosted 移动到 destination Hosted；
- `nxrm3:staging-delete`：按 tag 回滚删除。

这意味着 Pro Staging 提供的是**构建批次识别、环境隔离和晋升工作流**，并非普通 Hosted 中单个 GAV 的隐藏状态与 commit。

Sonatype 文档说明 move 可以作用于所有匹配搜索条件或 tag 的 component，但没有在公开文档中承诺“任意数量 component 的 move 是一个数据库级全有或全无事务”。因此本文不把 Staging move 表述成坐标级或 build 级强原子提交；可以确认的是，生产可见性通过不同仓库/Group 和 CI 晋升门禁来控制。

## 5. Nexus Repository 2 的 close/release 不等于 Nexus Repository 3

Sonatype 的 [Upgrading Staging](https://help.sonatype.com/en/upgrading-staging.html) 明确区分了两代模型：

| Nexus Repository 2 | Nexus Repository 3 |
|---|---|
| 每次 build 创建一个动态 staging repository | 每次 build 使用一个 component tag |
| Nexus 内部定义工作流 | Jenkins 等外部 CI 定义工作流 |
| Maven 2 专用 | Staging building blocks 支持多种格式 |
| 经典 staging suite 和 close/release | 固定 Hosted 仓库之间通过 REST move/delete |
| staging repository 被 release repository 吸收 | tag/build metadata 在晋升后继续存在 |

Sonatype 也明确说明 Nexus Repository 3 Maven Plugin 与只用于 Nexus Repository 2 的 Nexus Staging Maven Plugin 不兼容。

因此，在讨论“像 Nexus 一样做 staging”时，必须先明确指的是：

- Nexus 2/Maven Central 历史语境中的 close/release；还是
- 当前 Nexus Repository 3 Pro 的 tag + repository move。

对当前产品设计有参考价值的是后者。

## 6. 与 Artifact Gateway 当前两种发布模式的对比

| 维度 | Nexus 普通 Maven Hosted | Gateway 默认直接模式 | Gateway 严格模式 |
|---|---|---|---|
| 标准 Maven/Gradle PUT | 直接保存并可见 | 校验后直接发布 | 校验并暂存 |
| 额外 finalize | 不需要 | 不需要 | 需要 Gateway `coordinate:commit` |
| 未修改的 `mvn deploy` | 命令成功后发布完成 | 命令成功后发布完成 | PUT 成功，但没有提交不会公开 |
| 单坐标完整性 | 不保证跨资产原子性 | 不保证跨资产原子性 | commit 前不可见，commit 时校验并原子公开 |
| 中途失败风险 | 已成功资产可能保持可见 | 已成功资产可能保持可见 | 暂存资产保持不可见 |
| 协议兼容性 | 标准客户端零改造 | 标准客户端零改造 | 发布语义包含 Gateway 扩展 |

Artifact Gateway 默认模式与 Nexus 普通 Hosted 的客户端门槛一致，同时保留服务端对象校验、不可变 Release、权威 checksum/metadata 和 PostgreSQL 发布事实。严格模式不是 Nexus 普通 Hosted 的行为；它提供更强的坐标完整性，也明确引入额外集成成本。

## 7. 对 Artifact Gateway 的设计含义

以下属于基于上述一手证据的产品设计推论，不是对 Sonatype 行为的引用。

项目已经把上述证据落实为仓库级策略：`mavenStrictPublication` 默认 `false`，成功 PUT 直接可见；显式设置为 `true` 时，才使用暂存与 `coordinate:commit`。

如果目标是保留当前更强的原子坐标保证，可以考虑两种清晰模型：

1. **普通 Hosted 兼容模式 + 可选原子模式**
   - 普通 Hosted：每个 PUT 立即可见，最大化 Maven/Nexus 兼容性；
   - Atomic Hosted：保持暂存与 `coordinate:commit`，明确要求 Gateway 插件或 CI wrapper。
2. **隔离 Hosted + 晋升模式**
   - 上传行为保持 Maven 标准和逐资产可见；
   - CI 只向隔离 Hosted 发布；
   - 上传、校验全部成功后，将完整坐标或 build 晋升到生产 Hosted；
   - 消费者只读取生产 Group。这更接近 Nexus Repository 3 Pro Staging。

对外文案必须同时说明两种模式：默认模式支持现有 Nexus 流水线零改造迁移，但不承诺多文件坐标原子可见；严格模式需要 Gateway 专属提交，但能在 commit 前隐藏整个坐标。两种模式都不应被夸大为跨多个 GAV 的构建级原子事务。

## 参考资料

- Sonatype：[Maven Repositories](https://help.sonatype.com/en/maven-repositories.html)
- Sonatype：[Configurable Repository Fields - Deployment Policy](https://help.sonatype.com/en/configurable-repository-fields.html#deployment-policy)
- Sonatype：[Staging](https://help.sonatype.com/en/staging.html)
- Sonatype：[Staging Concepts](https://help.sonatype.com/en/staging-concepts.html)
- Sonatype：[Upgrading Staging](https://help.sonatype.com/en/upgrading-staging.html)
- Sonatype：[Nexus Repository Maven Plugin](https://help.sonatype.com/en/nexus-repository-maven-plugin.html)
- Sonatype GitHub：[MavenContentHandler.java, Nexus Repository 3.93.2](https://github.com/sonatype/nexus-public/blob/ed94b05e53eff451c4b617ef31d09e8e9f066365/public/common/components/formats/nexus-repository-maven/src/main/java/org/sonatype/nexus/content/maven/internal/recipe/MavenContentHandler.java)
- Sonatype GitHub：[MavenContentFacetImpl.java, Nexus Repository 3.93.2](https://github.com/sonatype/nexus-public/blob/ed94b05e53eff451c4b617ef31d09e8e9f066365/public/common/components/formats/nexus-repository-maven/src/main/java/org/sonatype/nexus/content/maven/internal/recipe/MavenContentFacetImpl.java)
- Apache Maven：[Maven Deploy Plugin](https://maven.apache.org/plugins/maven-deploy-plugin/)
- Apache Maven：[`deploy:deploy` parameters](https://maven.apache.org/plugins/maven-deploy-plugin/deploy-mojo.html)
- Gradle：[The Maven Publish Plugin](https://docs.gradle.org/current/userguide/publishing_maven.html)
