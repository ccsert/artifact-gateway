# 参与 Artifact Gateway 开发

[English](CONTRIBUTING.md)

Artifact Gateway 当前仍由核心团队开发。本指南记录仓库内采用的工程流程，使项目在
准备扩大贡献范围期间，所有变更都保持可评审、可复现。

## 开发前置条件

- 支持 Compose 的 Docker
- 修改部署资源时需要 `kubectl`、`jq` 和本地 Kubernetes 集群
- `go.mod` 声明版本的 Go
- 修改分阶段 Cargo 协议基础时需要 Rust/Cargo 1.96.0
- Node.js 24 与 npm
- GNU Make
- OpenSSL

从 checkout 启动完整本地栈：

```sh
make dev-bootstrap
make dev
```

`make dev-bootstrap` 会在需要时创建私有 `.env`，只生成本地 Gateway、PostgreSQL 和
RustFS 所需凭据；已有真实值会被保留，因此可以安全重复执行。完整流程见
[快速入门](docs/getting-started.zh-CN.md)。

`make dev-status` 检查 Console、API 代理与 Gateway 健康状态；`make dev-down` 只停止
当前 checkout 管理的 Console；`make down` 停止 Compose 栈但不会删除 PostgreSQL 和
RustFS 数据卷。

## 变更流程

1. 每项变更只聚焦一个行为或工程问题。
2. 在受影响的公开 seam 上增加或更新测试。
3. 先修改 OpenAPI 源，再生成客户端或服务端接口。
4. Schema 变更必须新增迁移，禁止修改已经应用的迁移。
5. 用户可见变化记录在 `CHANGELOG.md` 的 `Unreleased` 下。
6. 开发时执行最小相关检查，提交前再执行下方必需检查。

禁止手工修改生成文件。可编辑 OpenAPI 根文件包括面向原生协议及 Console 的
`api/openapi/native-hosted.yaml`，以及面向 Go 管理边界的
`api/openapi/management-runtime.yaml`。共享管理路径和 schema 必须在两个根文件中
保持一致。`make openapi-check` 会重新生成并校验两个 JSON bundle、Console 客户端
与 Go 管理契约。

运行契约检查前安装固定版本生成器：

```sh
npm --prefix tools/openapi ci --ignore-scripts --no-audit --no-fund
npm --prefix console ci --ignore-scripts --no-audit --no-fund
```

`make dev` 正在运行 Vite 时不要重新安装 Console 依赖。先用 `make dev-down` 停止受管
Console，安装依赖后再执行 `make dev`；在运行中的 Vite 进程下替换 `node_modules` 会使
优化后的依赖 URL 失效。

可选的 Ant Design CLI lint/usage/doctor 辅助命令及其
`fast-glob` → `micromatch` → `braces` 开发依赖链已移除。
GHSA-vfj7-8cjw-p6xm 不再有审计例外；重新引入该 advisory 会使门禁失败。
使用 `make console-check` 执行 ESLint 和格式检查，`make console-typecheck` 检查类型，
`make console-test` 执行全量覆盖率门禁。API 生成仍使用上述固定 OpenAPI 工具；
主题通过 Console 主题定义与 Ant Design `ConfigProvider` 实现，样式通过
`make console-build` 使用 Vite 与 Tailwind 构建。这些流程均无需 Ant Design CLI。
其他审计决策保留在 `scripts/npm-audit-policy.mjs`；新 advisory、无效报告、注册表故障
和例外到期均使依赖审计失败。

## 从 Issue 到合并的追溯

每个用户可见变更或缺陷都用 GitHub Issue 记录现象、目标行为、明确范围、验收条件和
依赖关系。每个 Issue 应能独立验证和合并；涉及不同协议或缓存语义时，拆成互相链接的
后续 Issue。

分支可命名为 `codex/issue-24-group-resolution`。PR 正文用 `Closes #24` 关联 Issue，
描述最终行为，并按 PR head SHA 记录检查命令和结果。必需检查在该 SHA 上通过后再合并。
在 Issue 中留下 PR、合并 SHA 和剩余限制；确认没有 worktree 使用后，删除已合并的
功能分支。不能仅因分支年代久远就删除尚未合并的工作。

CI、隔离协议 fixture、本地运行时检查和发布验收属于不同证据。关闭实现 Issue 不代表
版本可以发布；升级、恢复和部署验收应明确跟踪。

## Issue 分类

每个 Issue 挂一个类型标签和一个主领域标签；涉及具体格式时再挂对应格式标签。标签词汇
本身就是分类：按 `label:"area: cli"` 过滤即得到该工作流的全部单。

| 轴 | 标签 | 含义 |
| --- | --- | --- |
| 类型 | `type: feature`、`type: bug`、`type: design`、`type: docs`、`type: chore` | `feature` 新增或扩展用户可见能力；`bug` 行为与预期或契约不符；`design` 产出是决策（落在 ADR 或文档）而非代码；`docs` 纯文档变更；`chore` 覆盖 CI、依赖、重构与工具链，无行为变化。 |
| 领域 | `area: console`、`area: api`、`area: cli`、`area: auth`、`area: audit`、`area: platform`、`area: ops` | `console` 控制台界面；`api` 管理契约、OpenAPI 源与生成客户端；`cli` agctl；`auth` 身份、授权、账号与服务端凭据；`audit` 审计、保留与取证；`platform` 服务端数据面——制品与仓库、生命周期与保留、晋升复制、容量、缓存、隔离、扫描；`ops` 部署、运行、诊断、日志、备份与升级。 |
| 格式 | `format: maven`、`format: oci`、`format: npm`、`format: pypi`、`format: go`、`format: apt`、`format: conan`、`format: cargo`、`format: nuget`、`format: raw` | 与领域正交，用于涉及某协议行为的单。 |

标题以领域开头：技术面用 `Console:`、`API:`、`CLI:`，其余用中文领域词（`授权:`、
`审计:`、`平台:`、`运维:`），后接结论或要求的结果，而不是动作。跨领域的工程性工作
（流程、工具链）可以省略领域词与领域标签。

每个 Issue 写清背景、范围、验收标准和明确的"不在范围"；系列需求保留一个总览 Issue，
正文用任务清单列出子单。每个可独立合并的变更对应一个 Issue，PR 用 `Closes #N` 关联。
`.github/ISSUE_TEMPLATE/` 下的表单以各类型字段表达这些要素并自动挂类型标签，提交时补
适用的领域与格式标签。缺陷的预期与契约依据定义验收，影响面说明范围与排除项；设计的
问题与约束说明范围与排除项，决策及其记录的结论定义验收。功能表单直接使用这些要素名。

## 必需检查

推送前先在本地跑完矩阵，这样一次推送只花一次 CI，而不是每轮迭代都花一次：

```sh
make ci-local                        # 快速集，数分钟
make ci-local-full                   # PR 上的全部 job，含原生 E2E 套件
./scripts/ci-local.sh --job console  # 只跑某一个 CI job
./scripts/ci-local.sh --list         # 查看每种选择包含什么
```

`scripts/ci-local.sh` 调用的是工作流本身调用的同一批目标与脚本，而不是另写一套实现；输出按 CI job 分组，并打印每项检查的耗时。默认遇错即停，加 `--keep-going` 可一次收齐所有失败。`mainline-assets` 与 `mainline-images` 仅在推送到 main 时运行，会标注为不在范围内。CI 仍是最终权威。

后端变更：

```sh
go test ./path/to/changed/package
make lint
make vet
make coverage
```

Console 变更：

```sh
npm --prefix console ci
make console-check
make console-test
make console-build
```

`make coverage` 同时执行仓库级 Go 基线与安全、生命周期、复制、扫描等稳定包的更严格
门槛。`make console-test` 统计全部手写 Console TypeScript/TSX，仅排除生成客户端和测试
基础设施。覆盖率门槛用于防回退：应通过有意义的公开 seam 测试逐步提高，不能为了通过
检查而降低。

契约、持久化或协议变更还需要运行对应检查：

```sh
make openapi-check
make integration-test
make native-oci-e2e
make native-raw-e2e
make native-maven-e2e
make conan-e2e
make cargo-contract
```

Kubernetes manifest、Console 容器路由或本地部署工具发生变化时，先执行离线渲染门禁：

```sh
make kubernetes-local-check
make console-docker-build
```

Docker Desktop Kubernetes 可用时，再执行 `make kubernetes-local-up`、
`make kubernetes-local-status` 和 `make kubernetes-local-verify`。验证流程会发布唯一 Raw
对象、重启 Gateway，并从 PostgreSQL 仓库记录与对象字节两个方向验证持久化。只有明确
要删除 namespace 与本地 PVC 数据时，才执行 `make kubernetes-local-down`。

共享后端行为发生变化时，提交前运行 `make test`。修改 Markdown 入口、能力声明或本地
资产时运行 `make docs-check`；该门禁会检查双语入口互链和本地文档链接。完整必需矩阵以
CI workflow 为最终事实来源。

### 契约 lane 与性能证据

`contract-and-test` 保持稳定的 required 汇总门禁。`contract-core` 与十条原生
lane 独立启动；原生 matrix 使用 `fail-fast: false`，最多同时调度三个 job。
主线制品仍等待汇总、quality 和 Console。汇总拒绝失败、跳过、取消、缺失、
过期或损坏的结果。同一 run/head 的定向重跑可复用较早 attempt 的成功工件，
但保留其原 attempt；较新的失败不能回退为较早的成功。

可编辑的目标和超时清单为 `scripts/ci-contract-lanes.json`：

| lane | 既有目标 | 目标时间上限 |
| --- | --- | --- |
| migration-clients | `nexus-migration-client-e2e` | 10 分钟 |
| inventory | `nexus-inventory-scan-e2e` | 10 分钟 |
| nuget | `native-nuget-e2e` | 15 分钟 |
| oci-migration | `nexus-oci-migration-e2e` | 10 分钟 |
| maven-oci-auth | `private-upstream-auth-e2e` | 10 分钟 |
| six-format-auth | `private-upstream-formats-e2e` | 15 分钟 |
| oci-discovery | `native-oci-discovery-e2e` | 15 分钟 |
| npm-migration | `nexus-npm-migration-e2e` | 15 分钟 |
| helm | `native-helm-e2e` | 15 分钟 |
| rubygems | `native-rubygems-e2e` | 15 分钟 |

每条原生 lane 先执行限时五分钟的 `go mod download`，单独保存准备日志，防止首次下载诊断混入原脚本严格解析的 JSONL 测试流。两组冷缓存性能对照均从空的私有 Go 缓存开始，并将该准备耗时计入关键路径；构建与协议客户端缓存保留原有行为。

核心保留其它既有目标，包括发布备份 15 分钟、升级 10 分钟的检查。
`./scripts/ci-local.sh --lane inventory` 单独执行一条 lane；`--job contract`
执行核心、全部十条 lane，以及原来额外的本地 CLI 栈检查。本地 OpenAPI
新鲜度通过再生成和文件摘要比较验证，允许审查未提交的生成改动。编排控制测试：
`python3 -B -m unittest discover -s scripts/tests -p test_ci_contract.py`。

每条 lane 即使在环境准备时失败，也上传 `contract-<lane>-<attempt>`。
只有 bootstrap 的工件代表失败，不能冒充测试完成。证据保留 head/tree、业务
文件摘要、原始命令和超时、case 事件、producer 原始文件、计时及清理结果。
客户端沿用已有自有 fixture 目录；包装层删除自己创建的临时目录，不删除其它
任务资源。专用 GitHub runner 另记录容器、卷和网络的执行前后差异；本地保留
fixture 自有清理报告，不把其它并行开发者资源当作自己所有。
`contract-aggregate-<attempt>` 另保存包含准备阶段的完整 GitHub job 日志。
只有该采集器增加只读 `actions: read`，发布写权限保持原样。

完整日志采集继续使用 120 秒预算。GitHub 可能先发布 runner 的完整日志，
稍后才更新 job 外层状态。仅当当前 attempt 的外层仍为 `in_progress` 且尚无
结论时，contract 采集器可组合调度器确认成功的依赖结果、精确的 head/run/job
身份、成功的 runner 收尾步骤和与之匹配的完整原始日志，形成独立完成证据。
该例外还要求已核查的 Runner 2.338.0 最终日志对象：HTTPS 200 `BlockBlob`，
提供方声明的长度、内容摘要和发布时间均匹配，且原始输出覆盖每个未跳过的
机器步骤。其它版本、可追加对象或完整性证据缺失时，仍须等待原始终态。
日志清单记录该依据，并保留原始 job 状态和时间戳。
结果缺失或矛盾、日志不完整、身份变化仍使汇总失败。
继承旧 attempt 的 job 仍须有原始 completed/success 结果；性能对照继续
要求真实终态和时间戳。

仅专用 GitHub core lane 在执行原目标前创建两个原本不存在的集成 Go 缓存卷；
已有缓存会被拒绝使用。它记录随机 owner、run、attempt 标签和创建凭据，finally
重新核对凭据后仅删除自己创建的两个卷；create 应答丢失或清理错误仍使 lane
失败。本机运行不认领、不删除这些缓存。未知新增卷（含匿名卷）继续报错。
每个 core step 保存前后资源清单，以及用于归属的卷标识、标签/选项和容器标识、
标签/挂载白名单字段；诊断工件不包含容器环境变量、命令或参数。

`contract-benchmark.yml` 进入默认分支后，执行
`gh workflow run contract-benchmark.yml --ref main`。它在同一提交调用两组相同
的六条 lane：串行 matrix 限一槽，并行 matrix 最多三槽；两组均使用全新私有
Go 缓存和相同固定客户端，各自经过严格汇总。
`benchmarkOrder` 将此对照固定为原有六条原生 lane；新增 OCI 发现、npm 迁移、Helm 与 RubyGems lane 仍在
日常 CI 与本地契约中强制执行，但不计入这项六 lane 性能声明。最终报告读取已结束的 job 元数据，
记录 checkout/setup、执行、工件上传、清理、汇总、排队及实际获分配并发数。
业务文件摘要、缓存条件、runner 架构或 CPU/内存等级不一致时拒绝对照。
所有 lane 与汇总工件必须来自当前 run attempt；定向重跑保留的早先成功任务
不能证明性能，因为等待重跑的间隔不属于执行时间。须重跑全部任务采集新的
可比样本。普通正确性汇总仍可保留原始来源信息并复用较早的成功工件。
实际至少两个并发 runner，且六 lane 关键路径缩短至少 25%，才构成成功的性能
证据。样本不可比或只有一槽时性能仍未证明；功能正确不等于已经加速。旧完整
contract 与新汇总的 wall time 另行报告，并注明各自真实缓存条件。该性能
工作流不发布发行资产或镜像。

## 文档约定

每份会进入文档站的 Markdown 都必须有英文规范路由和简体中文 companion。英文使用不带
语言后缀的 `.md`，中文使用 `.zh-CN.md`。两页必须互相链接，并分别链接对应语言的文档索引。

每个文档对都要加入 `docs/site-map.json` 的六个稳定分区之一。该文件是未来接入
Docusaurus、VitePress 或 MkDocs 的框架中立导航契约；不要再用框架专属 frontmatter
维护第二份导航事实。

双语正文必须保持行为等价。配置键、路由、状态码、命令、兼容限制、预览状态和安全边界
不能只存在于一种语言。只写语言跳转或简短占位说明不算翻译正文。

新增、重命名或移动页面后运行 `make docs-check`。它会验证本地链接、语言文件命名、双向
语言链接、本地化标题和完整导航覆盖。详见[文档站接入指南](docs/documentation-site-guide.zh-CN.md)。

## 测试要求

- 通过 HTTP、存储、协议或导出包接口测试外部可观察行为。
- 一个测试优先聚焦一个行为，并使用明确的字面期望值。
- 稳定公开 seam 可用时，修复缺陷前先补回归测试。
- 不要为了提高覆盖率而让测试依赖私有 helper。
- 不得把覆盖率收集范围缩小到少量高覆盖文件；只能排除生成代码、fixture 或测试基础设施。
- PostgreSQL 事务、迁移、锁和对象存储行为不能由内存实现证明，必须使用集成测试。

## 数据库迁移

迁移只能追加，并在 Gateway 节点启动前应用。数据库会同时记录迁移文件名与 SHA-256
校验和。使用以下命令验证迁移：

```sh
make integration-test
```

集成测试会对全新 PostgreSQL 应用所有迁移，并验证第二次执行不会产生变化。

集成测试生命周期命令显式使用 `artifact-gateway-integration` Compose 项目和
`/dev/null` 环境文件，忽略工作区 `.env` 及外部 `COMPOSE_PROJECT_NAME`，避免测试清理
选中正常开发栈。使用 `make integration-down` 清理该测试项目。

## 提交消息

使用祈使语态 Conventional Commit 标题：

```text
feat(search): add checksum lookup
fix(console): preserve selected artifact version
test(storage): cover expired worker leases
docs: describe clustered runtime roles
```

选择最窄且有意义的 scope。契约或源变更产生的生成文件应放在同一个提交中。

## 贡献许可

向 Artifact Gateway 提交贡献，即表示你同意该贡献按照项目的
[MIT License](LICENSE) 授权，除非另有明确说明。
