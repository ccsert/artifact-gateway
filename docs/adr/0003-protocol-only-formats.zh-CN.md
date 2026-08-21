# 可以接纳仅协议格式，但不能虚构发布 API

[English](0003-protocol-only-formats.md) · [文档索引](../README.zh-CN.md)

状态：已接受

Artifact Gateway 通常只在 Hosted、Proxy、Group 与制品生命周期行为都可以执行后接纳
一种格式。但有些生态只标准化分发，并没有仓库发布协议。Go Modules 是第一个此类
场景：`GOPROXY` 定义不可变读取，而模块作者通过版本控制系统发布，不会上传到模块代理。

满足以下全部条件时，这类生态可以只声明 Proxy 与 Group：

1. 原生客户端协议没有标准发布操作。
2. 能力档案省略 Hosted，以及所有无法执行的生命周期操作。
3. Proxy 缓存、完整性校验、授权、匿名访问、审计、搜索、容量核算、恢复、Console
   工作流和真实客户端 fixture 必须一起交付。
4. 未来新增 Hosted 流程时必须另行接受契约；产品专属上传 API 不得冒充生态标准。

这个例外用于保持能力发现真实可信，不会降低已声明仓库类型的质量要求。

Go 在 `0004-go-hosted-publication.md` 的 Gateway 专属 Hosted 发布契约被接受并实现前，
遵循此例外。该通用例外仍可用于其他只读生态；Go Hosted 必须继续把自己的 `PUT`
描述为 Gateway 扩展，而不是 Go 官方操作。
