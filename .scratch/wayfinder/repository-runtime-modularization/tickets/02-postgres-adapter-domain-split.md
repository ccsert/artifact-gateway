---
title: 按领域拆分 PostgresStore adapter implementation
label: wayfinder:task
state: closed
assignee: codex
depends_on:
  - tickets/01-repository-package-core-split.md
---

## Question

`internal/repository/postgres.go` 如何在保持单一 `PostgresStore` adapter 和数据库语义不变的前提下，
按 Hosted/grants、OCI、Raw、Maven、Groups/Audit 等职责拆分 implementation 文件？

## Acceptance criteria

- 不改变 migration、SQL 语义、事务边界、错误映射或 `Store` interface。
- 拆分后每个文件围绕一个领域职责组织，私有 helper 留在最靠近调用者的位置。
- 通过 repository 单元测试、Postgres 相关集成测试，以及至少受影响协议 E2E。

## Resolution

- `internal/repository/postgres.go` 现在只保留 `PostgresStore`、constructor、`Close` 和
  shared `isUnique` helper；仍是同一个 exported adapter。
- SQL implementation 按领域拆分到 `postgres_hosted.go`（Hosted repositories/groups、
  grants、retention）、`postgres_oci.go`、`postgres_raw.go`、`postgres_maven.go`、
  `postgres_group.go`（legacy Raw/OCI/Maven/Conan groups）和 `postgres_audit.go`。
- 没有新增公开 interface，没有修改 migration、SQL 文本、事务边界、错误映射、
  `Store` 方法集合、OpenAPI、路由或协议 handler。
- 已验证：`gofmt`、`go test -count=1 ./internal/repository ./internal/app ./contracts`、
  `make lint`、`make test`、`make integration-test`、`make native-raw-e2e`、
  `make native-oci-e2e`、`make native-maven-e2e`、`make conan-e2e`、`git diff --check`。
