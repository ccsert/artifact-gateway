---
title: 拆分 Repository 包核心模型与 Memory adapter
label: wayfinder:task
state: closed
assignee: codex
depends_on: []
---

## Question

`internal/repository/repository.go` 如何拆分为更深的 Repository 包内部 Module，使领域模型、
`Store` interface 和 Memory adapter 的 implementation 分别拥有清晰 locality，同时保持
所有导出符号、调用方 import、测试行为和协议语义不变？

## Acceptance criteria

- `internal/repository/repository.go` 不再同时承载领域类型、`Store` interface 和完整
  `MemoryStore` implementation；拆分后的文件名按领域职责命名。
- 不新增公开 interface，不改变 `Store` 方法集合、导出类型名、导出函数名或调用方所见语义。
- Memory adapter 内部可继续分多个私有 helper，但不得引入只有一个 adapter 使用的浅 seam。
- `gofmt` 后通过 `go test -count=1 ./internal/repository`，并视编译影响运行相关 app 测试。
- 在本 ticket 的 Resolution 中记录最终文件边界和验证命令。

## Resolution

- `internal/repository/repository.go` 已移除；导出领域模型集中到 `model.go`，错误值与
  Store interfaces 集中到 `store.go`。
- `MemoryStore` 保持同一个 adapter，不新增公开 seam；核心 struct/constructor 留在
  `memory_store.go`，implementation 按职责拆入 `memory_raw.go`、`memory_oci.go`、
  `memory_maven.go`、`memory_hosted.go`、`memory_group.go`、`memory_audit.go`。
- 最大 Memory adapter 文件约 318 行；首票没有改变导出类型、导出函数、Store 方法集合、
  数据库 schema、路由、认证授权或协议响应。
- 已验证：`gofmt`、`go test -count=1 ./internal/repository ./internal/app ./contracts`。
