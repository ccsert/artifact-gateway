# Go Hosted 使用单一规范模块 ZIP 发布

[English](0004-go-hosted-publication.md) · [文档索引](../README.zh-CN.md)

状态：已接受

官方 `GOPROXY` 协议定义不可变读取，但没有上传操作。因此 Artifact Gateway 将所有读取
保留在标准 `/go/<repository>/<escaped-module>/@v/...` 布局下，并为 Hosted 仓库定义
一个明确的 Gateway 发布扩展：

```text
PUT /go/<repository>/<escaped-module>/@v/<escaped-version>.zip
```

请求体是规范 Go 模块 ZIP。Gateway 使用 `golang.org/x/mod` 校验模块路径、语义版本、
归档布局、大小限制和顶层 `go.mod`。请求中的模块与版本必须同时匹配 ZIP 根目录和
`module` 指令。Gateway 从该文件派生 `.mod`，并用首次发布时间生成 `.info`；客户端
不会上传这两个派生值。

发布会锁定 `module@version` 坐标，保存经过验证且内容寻址的 `.info`、`.mod` 与 `.zip`
对象，并在一个 PostgreSQL 事务中同时公开三种表示。首次发布返回 `201`；重放相同 ZIP
幂等返回 `200`，且不会改变首次发布时间。使用不同 ZIP 或 `go.mod` 字节复用坐标返回
`409`。失败或冲突请求不会暴露部分版本。

写入任何此前不存在的对象前，Gateway 会持久化内部回收意图。回收 Worker 在同一对象
锁上串行化，保留已提交发布引用的对象，并在数据库或对象存储失败后重试删除未引用
对象。这是发布事务边界的崩溃恢复，不是面向用户的删除、恢复和回收生命周期能力。

此扩展需要经过认证的仓库写权限，仅用于 Go Hosted。Go Proxy 保持只读和可信穿透缓存；
Group 以 Hosted 优先的冲突策略组合 Hosted 与 Proxy 成员，并继续提供标准 `GOPROXY`
读取面。

该决定满足 `0003-protocol-only-formats.md` 的独立契约要求，并不把 `PUT` 声称为 Go
生态标准。Go Hosted 能力档案现在独立声明并门禁 `read`、`publish`、`browse`、`delete`、
`restore`、`retain`、`reclaim`、`promote` 与 `replicate`。

晋升和复制保留完整
`.info`/`.mod`/`.zip` 快照，并且只能发布到 Hosted 目标。隔离读取强制执行、认证上游
Proxy 凭据和校验和数据库镜像仍是独立能力，在可执行前不得对外声明。
