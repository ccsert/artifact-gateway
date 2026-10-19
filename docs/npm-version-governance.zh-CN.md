# npm 版本治理

[English](npm-version-governance.md) · [文档索引](README.zh-CN.md)

npm Hosted 支持 npm 11.6.2 的原生弃用提示与撤包协议。这些操作只改变 Registry
元数据，不修改已发布的 Tarball、Manifest、摘要、大小、发布者或发布时间。Proxy 与
Group 保持只读；不增加 Nexus 端点或另一套发布数据库。

## 客户端操作与权限

为 npm 配置路径限定的 Registry 和凭据，不配置回退 Registry：

```sh
npm deprecate 'widget@1.0.0' 'Use 1.1.0'
npm deprecate '@scope/widget@>=1.0.0 <2.0.0' 'Use 2.0.0'
npm deprecate 'widget@1.0.0' ''
npm unpublish 'widget@1.0.0' --force
npm unpublish '@scope/widget' --force
```

弃用提示需要仓库 `write`；撤包与管理恢复需要仓库 `admin`。写权限包含读权限，不包含
删除权限。原生包变更检查包名资源前缀；恢复沿用已有仓库管理边界。待授权、必须改密、
未授权和已撤权身份在变更前被拒绝。匿名读取仍受全局和仓库策略约束，匿名写入被拒绝。

精确版本和 SemVer 范围由 npm 自身选择，预发布版本遵循该客户端的规则。服务端将完整
文档与已提交快照对照，不另外解释范围。提示是纯文本，最多 4096 个 UTF-8 字节；空
字符串表示清除提示。每份元数据文档、每次整包撤包最多 1000 个版本；任一无效或未知
目标使整个事务失败。

Console 向读者展示所选版本的提示，Hosted 写入者可编辑或清除提示，管理员可撤下该
版本。变更失败保留包详情和草稿。成功状态码却缺少完整结果时显示结果未知，要求先回读
当前状态再决定是否重交。Proxy、Group 展示提示但不显示变更操作。

## 修订号、并发与审计

`GET /npm/<repository>/<package>` 返回 Hosted 包的 `_rev`。元数据与版本来自同一
数据库快照。发布、标签、提示、墓碑和恢复使用相同的包级协调，实际状态变化时推进修订号。

| 原生请求 | 效果 |
| --- | --- |
| 无附件的 `PUT /npm/<repository>/<package>` | 设置或清除提示，其余可见版本字段和标签不变 |
| `PUT /npm/<repository>/<package>/-rev/<revision>` | 为移除的版本写墓碑，并原子更新保留标签 |
| `DELETE /npm/<repository>/<package>/-rev/<revision>` | 为所有剩余版本写墓碑，并移除标签 |
| `DELETE /npm/<repository>/<package>/-/<tarball>/-rev/<revision>` | 确认 npm 对已有墓碑身份的清理请求，保留字节 |

路径与正文修订号必须相同。过期请求返回 `409`，不会覆盖并发发布、标签移动、删除、
恢复或提示变更。响应丢失后的旧请求重放同样返回 `409`，应回读 Packument 判断首次请求
是否提交。使用当前修订号的无变化提示更新会记审计，但不推进修订号。npm 自身将撤包
收到的 Registry `404` 视为已经不存在；授权失败仍为 `401`/`403`。

成功的 `npm.deprecate`、`npm.unpublish`、`artifact.restore` 审计与元数据一起提交，
保留实际操作者、版本/摘要范围、标准请求原因和修订证据。审计写入失败会回滚变更。
撤包后的确认记为 `npm.unpublish.cleanup`，不是物理删除事件。认证、权限拒绝记录
授权来源与原因，不记录 Token。

## 可见性、恢复与回收

Packument、精确版本、Tarball、标签和搜索均隐藏墓碑。高优先级 Hosted 的墓碑始终
拥有该版本身份，即使最后一个可见版本已经消失，低优先级 Hosted 或 Proxy 成员也不能
经 Group 复活它。恢复同一身份后屏障解除。隔离决策继续绑定原有不可变身份。

通过 Console 的仓库墓碑任务，或携带 `{"coordinate":"widget@1.0.0"}` 调用
`POST /api/v2/repositories/<id>/restore` 恢复。恢复核对保留对象大小和摘要，取得对象
租约，再次检查回收栅栏后才恢复可见。原提示和不可变身份保持不变。`latest` 缺失时指向
恢复版本，已移除的自定义标签不会自动重建；需要其他指向时显式使用 `npm dist-tag`。

撤包属于逻辑删除。既有保留策略和至少 24 小时的回收宽限期继续生效，可见共享引用阻止
字节回收。Collector 在对象租约内先持久写入 `collecting` 栅栏，再删除 S3 字节。
从该边界起恢复明确失败，包括字节删除成功、最终元数据更新失败的场景。重试生命周期
任务可完成回收，不会将缺字节版本恢复为可见。回收后的版本身份仍被保留，不能重新发布。

## 持久化与验证

向前迁移 `000149_native_npm_metadata_governance.sql` 添加包修订号、可变提示字段及
回收栅栏；从既有 Manifest 回填提示，不改写发布身份和 Tarball。纯元数据变更不上传
字节、不新增字节配额占用；恢复沿用配额和租约准入。本功能不增加秘密、上游凭据转发
或签名私钥。

Gateway 装配通过独立窄接口显式注入原生元数据变更和管理恢复，Collector 使用自己的
回收栅栏接口。新操作不扩张已冻结的 GatewayStore 聚合；旧恢复签名直接退役，不保留别名。

- Memory/PostgreSQL 共用契约覆盖原子选中、无变化/过期重放、不可变身份、墓碑/标签
  一致性、恢复和共享引用；两个 PostgreSQL 实例验证并发 CAS 与审计失败回滚。
- 升级测试应用 149 之前的全部 schema，写入旧发布，追加 149 并重建 Store，核对提示
  以及未改变的 Manifest、时间、摘要和对象身份。
- `make native-npm-e2e` 校验官方 npm 11.6.2 压缩包的固定 SHA-512。
  PostgreSQL/RustFS 门禁验证 scope/非 scope 的精确/范围提示、空缓存安装警告与清除、
  写入者拒绝、单版本/整包撤包、Group 权威、新实例及管理恢复，并在治理前后核对四份
  完整 Tarball 的大小与 SHA-256。
- HTTP、生命周期及 PostgreSQL/S3 测试覆盖缺字节、取消、过期响应、撤权与部分回收失败
  恢复；Console 覆盖草稿保留、未知结果、提示转义、原生写入，以及明暗主题桌面/窄屏
  几何；完整 Playwright 是 Console 门禁。

对照参考为 kkRepo 固定
[`2d0ce092` 的 npm Hosted service](https://github.com/klboke/kkRepo/blob/2d0ce09290dbd9a2f47a5d4df7a373337a503c36/server/src/main/java/com/github/klboke/kkrepo/server/npm/NpmHostedService.java)。
借鉴其修订号元数据/删除路径的协议覆盖，保留 Gateway 自身的墓碑、不可变身份、配额和
租约契约。这些测试属于隔离本机/CI 证据，不代表生产切流，也不复制 npmjs.com 的账户
和时间窗策略。
