# Go 模块路径与源码仓库一致

[English](0008-go-module-path.md) · [文档索引](../README.zh-CN.md)

状态：已接受

## 背景

`go install <module>/cmd/agctl@<tag>` 是 agctl 的三条安装通道之一（#128）。直到
`v0.9.0`，`go.mod` 声明的是 `github.com/artifact-gateway/artifact-gateway`，而源码
位于 `github.com/ccsert/artifact-gateway`。声明路径下没有仓库，Go 工具链返回
`repository not found`；改用仓库路径请求则报 module path 不匹配（#347）。

## 备选方案

1. **把模块路径迁到仓库路径。** 将 `go.mod` 与全部 import 改为
   `github.com/ccsert/artifact-gateway`。工具链可直接从 GitHub 与公共模块代理解析，
   不需要额外托管。
2. **提供 vanity 路径。** 保留 `github.com/artifact-gateway/artifact-gateway` 并使其
   可解析。`github.com/...` 路径无法自带 `go-import` 元数据，因此需要该 GitHub
   组织与仓库真实存在，项目目前并不持有。
3. **放弃该通道。** 不改路径，并在文档中把 `go install` 标为不支持。

## 决策

采用方案 1。模块路径为 `github.com/ccsert/artifact-gateway`；
`scripts/release-version-check.sh` 保证文档中的安装命令与 `go.mod` 声明的路径一致，
并在可得知仓库时与该仓库一致。

## 影响

- 从首个包含本变更的 tag 起，`go install github.com/ccsert/artifact-gateway/cmd/agctl@<tag>`
  可用。更早的 tag（包括 `v0.9.0`）仍声明旧路径，无法这样安装。
- module 模式安装以 tag 作为版本。Go 工具链不为 `pkg@version` 构建记录 VCS 信息，
  因此 `agctl version` 的 revision 显示为 `unknown`；Release 附件与镜像会注入两者。
- 引用旧路径的外部代码须改用新路径。旧路径从未可解析，因此不存在依赖它的已发布消费者。
- 若仓库将来迁入组织，按同样的机械替换再次迁移路径，并以新 ADR 取代本 ADR。
