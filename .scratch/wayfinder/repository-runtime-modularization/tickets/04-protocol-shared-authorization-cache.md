---
title: 提炼协议运行时共享授权与缓存复核 Module
label: wayfinder:task
state: closed
assignee: codex
depends_on:
  - tickets/03-repository-api-composition-root.md
---

## Question

Raw、OCI、Maven、Conan handler 中哪些候选授权、cache source revalidation 和审计逻辑已经形成
真实共享行为，可以提炼为小 Interface、深 implementation 的私有 Module，而不改变协议响应？

## Acceptance criteria

- 只抽取至少两个协议真实共享且语义一致的行为；不为了减少行数制造参数膨胀 helper。
- 保持各协议 status code、headers、challenge、cache hit/miss 和审计语义稳定。
- 通过相关 app 测试和 Raw/OCI/Maven/Conan E2E。

## Resolution

新增 `internal/app/repository_member_runtime.go` 作为私有共享 Module，集中表达 legacy
Group member 的 managed grant 判断与筛选：

- `groupMemberAccess.filterManaged` 现在服务 Maven 与 OCI 的显式绑定成员筛选，handler 仍保留各自的审计写入和协议响应。
- Conan 的 managed member 判断改为复用 `groupMemberAccess.managedDecision`。
- Maven 与 OCI 的 cache source 复核改为复用 `cacheSourcePresent` / `cacheSourceAllowed`，仍由各自 cache adapter 决定 proxy allowlist。
- Raw 保留现有 inline 路径，因为它对 unbound legacy member denial 有不同的终端 `403` 行为；强行纳入共享 helper 会扩大 helper 参数并增加行为风险。

验证：

- `go test -count=1 ./internal/app ./contracts`
- `git diff --check`
- `make lint`
- `make test`
- `make openapi-check`
- `make console-typecheck`
- `make integration-test`
- `make native-raw-e2e`
- `make native-oci-e2e`
- `make native-maven-e2e`
- `make conan-e2e`
