# 制品格式扩展指南

[English](format-extension-guide.md) | [文档索引](README.zh-CN.md)

Artifact Gateway 只接纳完整的仓库能力，不把新增格式等同于增加枚举值或 Console 选项。服务端目录 `internal/repository/format_profiles.go` 是准入入口，`GET /api/v2/formats` 是由其生成的管理投影。

## 准入门禁

只有当同一变更集为声明的每项能力提供明确实现计划时，才能添加格式档案：

1. 定义规范化 package、version、asset 和不可变摘要身份。
2. 添加只向前的 PostgreSQL 迁移，并保证 Memory/PostgreSQL Store 一致。
3. 使用协议兼容客户端实现原生 Hosted 发布与读取，包括完整性验证和原子可见性。没有发布协议的生态可遵循 [ADR 0003](adr/0003-protocol-only-formats.zh-CN.md)，仅声明 Proxy 与 Group。
4. 实现格式专用缓存键、负缓存、上游保护和有序成员行为的 Proxy 缓存及 Group 解析。
5. 增加管理浏览/搜索投影和稳定的 artifact/version 深链接；全局搜索结果必须可直接定位。
6. 在声明相应操作前完成逻辑删除、墓碑、恢复、保留、延迟回收、晋级和断点复制。协议只读格式必须省略不支持的生命周期操作，不能暴露占位接口。
7. 接入 Repository Grant、匿名读取门禁、审计字段、有界指标、Worker 格式过滤、容量、配额、备份和恢复。
8. 扩展 OpenAPI、生成的 Go/TypeScript 客户端、Console 选择器、协议兼容文档、单元/集成测试及黑盒客户端夹具。

上述任一要求仍只是占位 handler 时，不得把格式加入 `Format`、`FormatProfile` 或 Console。开发分支可以分阶段实现，但正式接纳的档案必须描述同一 revision 中可执行的行为。

## 能力规则

- `RepositoryTypes` 决定可创建的仓库类型。
- `GroupSupported` 决定是否允许 Group；它不代表可混用任意 Repository。成员必须处于 active 且格式一致。
- `AnonymousRead` 表示协议能够遵循全局及 Repository/Group 匿名策略，不代表默认开启匿名访问。
- `PublicationScanning` 表示 Hosted 发布能向自动扫描和有界补扫暴露规范不可变资产；是否实际扫描仍由扫描器配置和 Repository 策略决定。
- `HostedOperations`、`ProxyOperations` 必须与可执行的管理和协议行为一致。`GET /repositories/{id}/capabilities` 由同一档案派生，不得维护第二份列表。

档案 helper 返回防御性副本。调用方应使用 `SupportedFormatProfiles`、`SupportedFormats`、`FormatProfileFor` 和能力谓词，不应另建格式清单。

## 必需验证

新增格式至少运行：

```sh
make openapi-check
go test ./...
go vet ./...
make console-typecheck
make console-check
make console-test
make console-build
```

还必须增加协议原生端到端夹具，以及 PostgreSQL/S3 持久集成覆盖：发布、解析、删除、恢复、保留、回收、晋级、复制和升级/回滚。
