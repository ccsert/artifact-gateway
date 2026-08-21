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

禁止手工修改生成文件。可编辑 OpenAPI 根文件是
`api/openapi/native-hosted.yaml`；`make openapi-check` 会重新生成并校验 JSON bundle、
Console 客户端与 Go 管理契约。

运行契约检查前安装固定版本生成器：

```sh
npm --prefix tools/openapi ci --ignore-scripts --no-audit --no-fund
npm --prefix console ci --ignore-scripts --no-audit --no-fund
```

`make dev` 正在运行 Vite 时不要重新安装 Console 依赖。先用 `make dev-down` 停止受管
Console，安装依赖后再执行 `make dev`；在运行中的 Vite 进程下替换 `node_modules` 会使
优化后的依赖 URL 失效。

## 必需检查

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

## 提交消息

使用祈使语态 Conventional Commit 标题：

```text
feat(search): add checksum lookup
fix(console): preserve selected artifact version
test(storage): cover expired worker leases
docs: describe clustered runtime roles
```

选择最窄且有意义的 scope。契约或源变更产生的生成文件应放在同一个提交中。
