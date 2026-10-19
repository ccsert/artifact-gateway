# PyPI 文件撤回

[English](pypi-yank.md) · [协议矩阵](protocol-compatibility.zh-CN.md)

撤回（yank）是 [PyPA 文件撤回规范](https://packaging.python.org/en/latest/specifications/file-yanking/)
定义的文件选择提示，不删除或改写 wheel/sdist。范围依赖通常跳过已撤回文件；精确的
`==` 或 `===` 锁定仍可下载，pip 会提示原因。隔离和墓碑各自执行独立治理策略。

## 管理 Hosted 版本

在 Console 打开 Hosted PyPI 项目并选择版本，文件表会显示每个文件的状态和原因。
持有 `repositories:write` 的身份可以打开“管理撤回”，选择当前版本全部文件或一个
精确文件，再撤回或恢复。仓库授权允许时无需平台管理员角色。Proxy/Group 页面只读。

生成的管理契约提供：

```text
GET   /api/v2/repositories/{repositoryId}/pypi/projects/{project}/versions/{version}/yank
PATCH /api/v2/repositories/{repositoryId}/pypi/projects/{project}/versions/{version}/yank
```

GET 返回 `repositoryId`、规范化 `project`、`version`、不透明 `revision`，以及所有可见
文件的 `filename`、`yanked`、`reason`、`digest`、`size`。强 `ETag` 是带引号的 revision。
修订覆盖完整文件集合：并发上传、恢复或撤回状态变化都会使旧选择失效。读取沿用仓库
读授权和显式匿名读取策略；PATCH 必须使用已认证的写入身份。

提交一个当前 `If-Match` 和 JSON 请求：

```json
{"filename":"widget-1.0-py3-none-any.whl","yanked":true,"reason":"ABI 回归"}
```

`yanked` 必填。省略 `filename` 会原子更新该版本当前全部可见文件。以后新上传的文件
不会自动继承撤回；须刷新并在新操作中明确包含。原因可省略或为空，须为有效 UTF-8，
至多 1024 字节且不含控制字符。恢复（`yanked:false`）会清除原因。未知字段、null、
无效或过大的输入返回 `400`。

旧修订、弱标签、通配符或多值 validator 返回 `412`，不修改状态。刷新后重新确认文件
集合再提交。重复当前状态成功且不推进修订。变更与 `pypi.yank` / `pypi.unyank` 成功
审计在同一事务提交，审计写入失败会回滚状态。证据包含文件或版本范围、变更前后修订、
原因和变更文件数。档案大小与 SHA-256 保持不变。公开能力目录只为 Hosted PyPI 声明
`yank`；能力声明不会绕过授权，也不会隐式补齐缺失的 store port。

## 原生读取、Proxy 与生命周期

HTML Simple 为已撤回链接输出 `data-yanked="原因"`，无原因时输出空属性。JSON Simple
输出 `yanked:false`、无原因时的 `true` 或原因字符串；上游 JSON 的空字符串也表示已
撤回。原因仅按文本转义，不当作 HTML 渲染。

Proxy 重验保留撤回属性和已验证档案缓存。无效或相互冲突的上游元数据不会静默恢复
缓存文件，已有 stale 路径会显示警告。Group 保留首选来源的完整文件元数据及字节，
低优先级副本不能洗掉其撤回声明。客户端凭据不向上游转发；认证上游索引仍未支持。

墓碑/恢复保留撤回属性。晋升和复制在发布新的目标文件时复制源当前声明；重复请求若
遇到已发布且字节相同的目标，保留目标自身治理状态，不覆盖其撤回元数据。备份恢复
同时保留 PostgreSQL 字段与已验证不可变对象。`000152_pypi_yank.sql` 只向前追加；旧
schema 的备份须完成正常向前迁移后才能提供服务。

## 验证与边界

`make native-pypi-e2e` 在私有虚拟环境固定 pip **26.2**、twine **6.2.0**。Hosted、
Proxy、Group 使用新的下载目录、无客户端缓存，验证 wheel/sdist 范围跳过、精确锁定
原因警告、混合文件状态、恢复/上游重验和停源读取。每份档案完整比较字节、size 与
SHA-256。

`scripts/pypi-yank-storage-e2e.sh` 使用动态端口创建并清理自有 Compose 工程，覆盖
PostgreSQL 跨 Gateway CAS、事务内审计失败、持久授权、RustFS 重启、晋升复制与墓碑
恢复。真实 `pg_dump` 恢复到空数据库，已验证字节通过新 S3 bucket 回读。这是隔离验收，
不是生产恢复或部署。不新增 Warehouse JSON API、漏洞数据库或私有上游认证。命令映射
中的 agctl yank 命令仍明确标为计划。
