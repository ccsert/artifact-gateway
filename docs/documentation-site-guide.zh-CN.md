# 文档站接入指南

[English](documentation-site-guide.md) | [文档索引](README.zh-CN.md)

Artifact Gateway 保持 Markdown 框架中立，便于后续接入 Docusaurus、VitePress、MkDocs
或其他静态文档系统，而不重写文档所有权与语言规则。

## 信息架构

`docs/site-map.json` 是导航契约，定义六个有序分区、本地化分区/页面标题、稳定页面 ID，
以及每份文档的英文和简体中文路径。

六个分区依次为：快速开始、架构与设计、协议与仓库格式、运维与安全、质量性能与发布、
研究路线图与参考。项目 README 继续作为项目首页；两个 `docs/README` 是文档首页，并保持
与 site map 相同的分区顺序。

## Locale 与路由约定

- 英文是默认 locale，使用不带后缀的 `.md`。
- 简体中文使用匹配的 `.zh-CN.md`。
- 根级文档同样遵循该规则，例如 `SECURITY.md` 与 `SECURITY.zh-CN.md`。
- 每对文档互相链接，并链接对应语言的文档索引。
- 检查器拒绝 `.en.md`，避免两套英文路由约定。

站点可以把英文挂载在 `/`、中文挂载在 `/zh-CN/`，但源路径和稳定 ID 不应改变。
重命名公开页面的 redirect/alias 应进入站点配置，不应创建第二种源命名方案。

## 内容所有权

双语正文要求行为等价，不要求逐句直译。两种语言都必须保留命令、配置键、API 路由、
状态码、兼容限制、安全边界、证据范围，以及能力是已发布、预览、规划还是历史材料。

不能发布只跳转到另一语言的占位页。一种语言的行为说明变化时，必须在同一变更中更新 companion。

## 生成器 Adapter

未来 Adapter 应读取 `docs/site-map.json` 生成 Sidebar 或 Navigation。它可以在构建目录
生成 frontmatter，但不得重写源 Markdown，也不得提交第二份手工导航树。

资源保存在 `docs/assets/`。仓库中的相对链接保持有效，Adapter 只能在生成输出中重写。
搜索索引应保存稳定 page ID、locale、section ID、title 与最终 route，语言切换不能依赖
比较翻译标题。路由缺失、重复、未成对或缺本地化标题时，构建应失败。

## 本地验证

```sh
make docs-check
```

门禁检查工作区中实际存在的 tracked/untracked Markdown，验证本地链接、语言命名、双向
语言链接、实质中文正文、本地化标题、唯一 ID/路径和完整导航覆盖。

删除或重命名页面时，在同一变更中更新两种语言、所有入站链接和 site map。历史发布记录
保存在 `docs/release-records/`，ADR 保存在 `docs/adr/`。

## 首次文档站验收

- 两种语言首页都按 site map 顺序渲染六个分区。
- 切换语言时保持同一稳定 page ID。
- 根级文档、ADR、发布记录、Mermaid 和图片均能解析。
- 代码块、表格、标题和 anchor 正确渲染。
- 搜索返回当前 locale 的结果并记录来源语言。
- 已对曾经分享的文档 URL 配置 redirect。
- CI 同时运行 `make docs-check` 和站点自身 link/build 门禁。
