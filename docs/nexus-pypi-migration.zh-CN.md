# Nexus PyPI Hosted 迁移

[English](nexus-pypi-migration.md) · [迁移批次](nexus-migration-batches.zh-CN.md)

Issue #461 将 PyPI Hosted 字节导入接入持久 Nexus 批次 worker。目标必须是已存在、
处于活动状态的 Gateway PyPI Hosted 仓库。批次显式映射源 Hosted 仓库；发现和计划
不会自动创建仓库，也不会复制源权限。

## 支持的源形状

读取器使用受限、带鉴权、只读的 Nexus Components REST 分页及其 Assets 记录，
并读取同仓库的 HTML Simple 项目页。Components 提供项目和版本身份，文件名及归档
核心元数据必须与之相符。项目使用 Python 规范化名称；首批接受规范发行版本、wheel
及 `.tar.gz` 源码发行文件，其他格式、歧义身份及不支持的归档形状明确失败。

项目 Simple 页必须与 Components 提供完全一致的文件集合，每个链接必须携带
SHA-256 fragment。链接必须解析至配置的 HTTPS 源和仓库内，禁止凭据、查询参数和
歧义编码路径；重复文件名、源路径及 HTML 属性均被拒绝。逐文件保留
`Requires-Python`、yank 标记及原因；Python 版本约束必须与归档核心元数据一致。
HTML 被解析成排序后的私有快照，排版变化不作为身份变化；该快照不会成为原生制品
或公开批次资产。

单文件压缩大小上限为 64 MiB，解压总量为 256 MiB，单项目 Simple/快照和单元数据
成员各为 2 MiB，每个项目最多 1,000 个文件，每份归档最多 10,000 个成员。现有
64 MiB 清单元数据预算计入各版本条目保存的项目快照副本。解析不解压到文件系统、
不执行代码；不安全路径、链接、重复成员、多份元数据、截断 ZIP/gzip 和身份不符
都会失败。

[PyPA Simple API](https://packaging.python.org/en/latest/specifications/simple-repository-api/)
定义文件选择元数据。[Sonatype PyPI 文档](https://help.sonatype.com/en/pypi-repositories.html)
将 JSON Simple 支持起点列为 Nexus 3.93，因此本切片使用 HTML 覆盖更早的源版本。
[PyPA wheel 规范](https://packaging.python.org/en/latest/specifications/binary-distribution-format/)
定义归档内部包身份。

## 持久发布与当前权限

每个 `project@version` 是一个发布单元。计划阶段读取并计算所有文件摘要，验证归档
身份，固定完整文件集合和选择快照；执行及恢复时重读实际字节和选择事实。发布前
完整回读目标对象并计算摘要，文件大小、ETag 或元数据行相等不能替代字节证明。

PyPI 只接受可持久定位的管理员：当前活动、角色为 admin 且不需强制改密的 `user:`，
或当前未撤销、未过期且仍有 admin 角色的独立 `api-key:`。静态 bootstrap 身份、
服务账户和未知身份会在创建 PyPI 批次前被拒绝；可先用 bootstrap 身份创建受平台
管理的管理员 API Key。发布事务锁定并重验当前用户或密钥行，不从旧批次恢复权限。
源凭据仅用于只读请求，复验时收到拒绝就停止导入。

窄接口 `PyPIImportStore` 使原生发布与批次 lease、源连接版本校验处于同一个实际
数据库事务中。一个版本的文件引用、发布审计及 committed 条目进度一起提交。
容量检查、不可变同名身份冲突、墓碑、隔离、审计写入、陈旧 worker 或数据库事务
失联任一失败，都回滚所有新增引用。已有目标 yank 和选择决策不被覆盖；相同内容
重试保留一份发布审计，提交回执丢失也保留已提交进度供恢复。

容量继续使用现有显式、绑定目标与存储池的操作员证据，本切片没有将其变成自动
存储测量。不支持或发生变化的源观察结果保留为失败，不转成成功跳过。Nexus REST
不提供事务性仓库快照，worker 通过受限重复读取检测已观察到的漂移。

## 可复现验收

运行 `make nexus-pypi-migration-e2e` 或对应脚本。它使用合成 Nexus HTTPS 响应和
自有 PostgreSQL/RustFS Compose 项目，保留 JSONL 结果及清理回执，强制八项命名
验收全部执行且无跳过。若本机 Docker 默认地址池耗尽，可给自有网络设置
`PYPI_MIGRATION_SUBNET`，应先确认不与现有网络重叠。脚本仅清理本项目及临时
客户端环境。

脚本从 `files.pythonhosted.org` 下载固定 pip 26.2 wheel，按官方 PyPI 发布元数据
固定的 SHA-256 和大小校验后，无索引安装该本地文件。客户端在源服务关闭后，以空缓存和仅目标索引配置选择可用 wheel，解析
合成依赖并核对两份原始摘要，再仅用本地下载文件完成安装和 import。较新的 yanked
版本及 Python 版本要求不满足的发行版不会被选中。源码发行文件的元数据及完整
`.tar.gz` 下载单独验证，不把它表述为源码构建验收。

范围排除 Proxy/Group 资产导入、`.zip`/`.tgz`、源码构建执行、源用户与凭据搬迁及
Console 新流程。PyPI 已有正式格式准入和父迁移路线图，与本切片的字节导入证据
分别记录。
