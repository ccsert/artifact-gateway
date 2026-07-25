---
title: 收敛 OCI、Maven、Conan 缓存与维护生命周期
label: wayfinder:task
state: closed
assignee: codex
depends_on:
  - tickets/06-raw-protocol-hosted-package-slice.md
---

## Question

如何让 OCI、Maven、Conan 的协议缓存和 native Hosted 生命周期各自归属协议模块，
同时保持 app 的组合根、管理 API、OpenAPI、schema、认证授权与协议响应兼容？

## Acceptance criteria

- 协议缓存的索引、对象校验、配额、GC、Proxy 校验与跨实例锁不再由 `internal/app` 实现。
- 原生 OCI/Maven 的对象回收和 Maven 保留策略不再由 `internal/app` 实现。
- app 仅保留稳定的组合面、必要的兼容别名和跨协议管理 API 编排。
- Conan、OCI、Maven 的原有单元、集成和真实客户端 E2E 继续通过；发布清单包含全部协议门禁。

## Result

- OCI、Maven、Conan cache 分别迁入 `internal/protocol/oci`、`maven`、`conan`；Conan
  保留对象 digest 校验、配额、表示隔离、GC、请求锁和带续租的 publication lock。
- `NativeMaintenance` 与 Maven `NativeRetention` 迁入相应协议模块；`cmd/gateway`
  仍通过 app compatibility alias 装配，`CacheMaintenance` 则保留为管理 API 的跨协议
  状态和 collection 编排。
- `docs/release-readiness.md` 现将 `make conan-e2e` 作为明确的可执行发布门禁；README
  明确 Conan 是受管授权目标而非 native 存储，并给出控制台验证入口和各格式 Proxy 配置边界。

## Verification

- `go test ./...`
- `make lint`
- `make integration-test`
- `make native-oci-e2e`
- `make native-maven-e2e`
- `make conan-e2e`
- `make openapi-check`
- `make console-typecheck`
- `make console-build`
- `make console-e2e`
- `git diff --check`
