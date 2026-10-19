# Memory 存储作为显式契约测试 Fixture

[English](0010-memory-store-contracts.md) · [文档索引](../README.zh-CN.md)

状态：已接受；Raw 首个切片已实现（#278）

## 背景与选项

Gateway 同时维护 memory 与 PostgreSQL 存储。Memory 适合隔离协议和 HTTP fixture，
但其测试通过不能证明 SQL 事务、行锁、租约或恢复。在基线 `b30a4651` 上，既有快速
repository 测试耗时 3.342 秒。在领域拆分前一次性替换全部 fixture，会把不同重构耦合，
也更难单独评估各格式的改动。

比较两个方案：

1. 立即删除 memory，让所有协议/HTTP 测试依赖 PostgreSQL。消除双实现，但会同时要求
   纯协议测试和全部现有 fixture 命令准备、隔离数据库。
2. 保留 memory 作为显式注入的测试 fixture，要求同一公开存储契约也在 PostgreSQL 运行。
   保留快速测试并暴露语义差异；事务、锁、租约、多实例和重启仍必须有真实 PG 证据。

选择方案 2，从 Raw 开始。这是测试策略，不承诺任何兼容性；API 与存储接口仍可按
ADR 0009 破坏性调整。Memory 不是生产持久化模式；#279 的显式组合根移除隐藏兜底。

## 契约与 Raw 首个切片

`raw_store_contract_test.go` 通过 `NativeRawStore` 定义一套测试，不访问 map、执行 SQL
或为不同后端设置不同期望。普通测试用新建 MemoryStore；integration build 对真实 PG
执行同一函数，每个场景使用独立仓库和不可变身份。`compose.integration.yml` 已在 CI
执行带 tag 的 repository 测试。

首批覆盖有界排序分页、对象元数据不可变、条件写并发、上传完成与条件检查原子性、
取消/过期、墓碑/恢复/回收，以及上传和对象锁争用时取消。它尚未覆盖每个存储操作，
也不声称 memory 能模拟全部 SQL 失败模式。默认读取/回收上限为 100，字节回收后恢复
返回 `ErrNotFound`；条件完成遭拒绝时，上传保持 open，原路径仍可见。

同套测试先复现了四类 memory 偏差：默认资产分页无界、默认回收批次为空、回收后恢复
错误不同、锁等待忽略取消；修复后在 memory 与真实 PG 上通过。内存锁沿请求 context
等待，不模拟 PG session、事务或连接池语义。

运行：

```sh
go test ./internal/repository -run TestRawMemoryStoreContract -count=1
go test -tags=integration ./internal/repository -run 'TestRaw(Memory|Postgres)StoreContract' -count=1
make integration-test
```

第二条要求 `TEST_DATABASE_URL` 指向已迁移的隔离测试库，不得使用生产库。在隔离
PostgreSQL 16 fixture 上，连续三轮带 race 的共享测试 memory 耗时 0.05 秒，PG 为 0.44–0.46 秒（构建缓存
已热，不含建库/迁移）。这是本地观察，不保证运行延迟或 CI 耗时。上面的 3.342 秒是
既有全包测试基线，与该切片耗时不是同规模对比。另用同一主机、热构建缓存，在基线精确归档和修改后
工作区分别执行 `go test -count=1 -json ./internal/repository`（Go 1.26.9 / darwin / arm64），
正常套件从 3.342 秒变为 3.382 秒，增加 0.040 秒（1.2%）。这组单次测试耗时用于衡量新增
fixture 成本，不代表统计显著的性能变化。

## 后续清单

修改已覆盖存储时必须保留同套公开契约。某能力无法诚实模拟时，收窄 fixture 或使用
PG 专属测试，不能以 memory 通过作为权威。随 #280–#282 逐领域推进：

- [x] Raw 首批发布/上传/生命周期/锁契约。
- [ ] Raw 剩余操作和持久化/约束失败场景。
- [ ] Maven 发布、历史身份、SNAPSHOT 接管。
- [ ] OCI upload/blob/manifest/index 生命周期及并发锁。
- [ ] npm/PyPI 发布、可变元数据及生命周期。
- [ ] Go/Cargo 身份预留、发布及生命周期。
- [ ] Conan recipe/package 闭包与发布。
- [ ] APT 发布、快照及签名引用。
- [ ] 共享身份、授权、治理、任务及运维存储。

每个迁移过的契约都必须对 memory 与 PG 执行相同期望。某领域的维护成本超过 fixture
收益时，可删除该领域 memory，并在同一切片记录决策、调整调用方。不能仅为绕开数据库
测试而给 memory 增加生产专属行为。schema 只追加、字节不可变、租约/fencing 规则继续有效。
