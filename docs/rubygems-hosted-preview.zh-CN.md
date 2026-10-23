# RubyGems Hosted 预览

[English](rubygems-hosted-preview.md) · [文档索引](README.zh-CN.md)

RubyGems Hosted 是默认关闭的预览，管理入口为
`/api/v2/previews/rubygems`，发布原始 `.gem` 并提供 RubyGems/Bundler 原生
compact 索引、Marshal 4.8 legacy 索引和 zlib gemspec。它尚未加入正式格式
档案、正式仓库目录、Console 创建选择器或安装预置，没有预览 `agctl` 命令。
Hosted 切片由 [#459](https://github.com/ccsert/artifact-gateway/issues/459) 跟踪；
原生 yank 与受控恢复由 [#462](https://github.com/ccsert/artifact-gateway/issues/462) 跟踪。
Proxy/Group、隔离、硬删除、Console、扫描、分发、备份准入和
Nexus 迁移仍由 [#293](https://github.com/ccsert/artifact-gateway/issues/293) 承接。

## 启用与创建

通过正常追加式迁移流程应用
`000185_native_rubygems_hosted_preview.sql` 和
`000187_native_rubygems_yank_governance.sql`，配置既有 PostgreSQL 和受校验的
S3/RustFS 运行时端口，并设置 `GATEWAY_RUBYGEMS_PREVIEW_ENABLED=true`。
默认值为 false，非法布尔值阻止启动。发布持久化使用显式注入的领域存储，
PostgreSQL 适配器及 advisory-lock 连接池独立组合。

仅 API 进程提供预览请求，不启动清理 worker。要收集过期上传，需在
worker/standalone 进程启用相同预览，并允许 `rubygems` 格式和 `retention`
工作类型；任一过滤器排除对应项都会排除该收集器。诊断通过
`previewWorkerFormats` 报告 `rubygems`，正式 `workerFormats` 枚举保持原有准入范围。

管理员使用以下请求创建仓库：

```text
POST /api/v2/previews/rubygems/repositories
Authorization: Bearer <本地管理员凭据>
Idempotency-Key: <稳定请求标识>
Content-Type: application/json

{"name":"ruby-internal","anonymousRead":false}
```

回执包含 `id`、`preview:true`、`sourceUrl`、`pushHost` 和 `publicationUrl`。
地址相对于 Gateway，需要补上 Gateway origin。正式列表不展示该预览，
请保存回执，通过 `GET /api/v2/previews/rubygems/repositories/{id}` 读回。
相同主体、幂等键和请求体返回同一仓库；改变请求体会冲突。

消费配置使用以 `/` 结尾的 `sourceUrl`。`pushHost` 特意不带尾斜杠，因为
官方 `gem push` 会自行追加 `/api/v1/gems`。`publicationUrl` 是直接 HTTP
上传的完整入口，不能作为 `gem push --host` 参数。

## 原生认证与客户端

原生 `Authorization` 支持本地 API key、Bearer 凭据，或以凭据为密码的
HTTP Basic。Basic 用户名不会切换主体。仓库、名称前缀授权和当前账户状态
使用既有 Gateway authorizer。集合索引逐包检查名称，前缀授权只能发现对应项。
Push 对解析后的真实名称检查授权，并在提交前重新检查认证、主体及写权限。
显式无效、过期或已撤销的凭据不会回退匿名访问；匿名读取需要全局策略和
仓库同时允许。

对于已具备私有凭据、消费入口为
`https://gateway.example.com/rubygems/ruby-internal/` 的仓库：

```sh
export GEM_HOST_API_KEY='<本地发布凭据>'
gem push owned_top-1.2.0.gem \
  --host https://gateway.example.com/rubygems/ruby-internal

gem install owned_top --version 1.2.0 --remote --clear-sources \
  --source 'https://gateway:<本地读取凭据>@gateway.example.com/rubygems/ruby-internal/' \
  --no-document
```

无人值守客户端使用自有凭据文件或配置，避免把含凭据的 source URL 写入共享
日志。Bundler Gemfile 只配置该仓库的 `sourceUrl`，可用标准 Bundler 凭据配置
提供 Basic 认证。已发布到该仓库的包及传递依赖不需要公共回退源。

## 发布、完整性与容量

不可变身份是保留大小写的 gem 名称、RubyGems 规范化版本身份和 platform。
原始版本、platform 字符串继续用于原生索引。解析器把 tar、gzip、YAML
视为有界数据，不执行 Ruby、不实例化 YAML 对象，也不读取上传者提供的
Marshal 对象。metadata、data、SHA-256/SHA-512 校验清单必须完整一致，
声明的文件必须与安全的普通文件 payload 路径精确对应。重复归档/YAML 项、
alias、不安全路径、不支持的 Ruby tag 和不一致的依赖约束会明确拒绝；
普通有界的尾随 YAML 字段不会擦掉已识别的 runtime/development 依赖及约束。

上限为原始 gem 64 MiB、解压 metadata 1 MiB、解压 payload 256 MiB、
10,000 个普通 payload 文件、256 个依赖、每组 32 个版本约束，以及每仓库
4,096 个有效预留或已提交身份。外层和 payload tar 必须是 USTAR，支持其合法 prefix 长路径；PAX/GNU 扩展、
签名归档及符号链接会被拒绝。platform 必须使用 RubyGems 规范字符串，缺失、
null 或空值按固定官方解析器映射为 `ruby`；序列/对象编码、非规范别名和依赖
宿主的 `current` 不支持。依赖类型必须是原始普通 `:runtime`/`:development`
symbol，只有同名拼写的字符串会被拒绝，以保持原 gem 语义。平台包可索引、下载；服务端执行扩展及验证所有目标操作系统不在
本切片范围。

date 支持缺失/null、合法 ISO 日历日期，以及固定官方 YAML 解析器理解的时间戳。
带引号的日期字符串遵循官方 `date=` 字符串语法，其中带引号的 ISO `T` 时间戳
会被拒绝；其他日期类型强转不在预览支持范围。`require_paths` 支持缺失/null、
字符串数组及官方旧版嵌套字符串数组，标量及非字符串项会被拒绝。已知 Ruby
对象 tag 会在整个 metadata 树检查，包括未使用的尾字段，因为官方 YAML
loader 同样会初始化这些节点。普通有界尾字段继续受支持，不能隐藏畸形的
日期或版本初始化数据。

原生 push 先预留容量与唯一对象键，暂存完整原字节，再重新打开并计算全对象
摘要、验证身份和 metadata、重新检查发布权限，最后在一个事务中提交索引
可见性和审计。待完成、失败上传不会进入任何索引。相同可见身份和原字节幂等
返回，不新增成功发布审计；不同字节或歧义原生文件名返回 409。每次原包读取
均先重新核验完整大小和 SHA-256。

管理员使用 GET/PUT
`/api/v2/previews/rubygems/repositories/{id}/capacity` 查看容量、设置配额。
`usedBytes` 和 `objectCount` 对应所有保留的已提交原包（包括已撤回的包），`pendingBytes` 预留尚未过期的
上传，`quotaBytes:0` 表示未配置字节配额。配额不得降到已提交和预留用量之下，
配额更新与审计保留事务边界。这套独立预览容量不代表格式已具备通用保留或
备份准入。

预留在一小时后过期。响应丢失或进程重启后可重试同一原包 push。清理只删除
过期、从未可见的上传对象，并与发布共用身份锁；新会话使用不同对象键。
它不会删除已发布的 gem，删除或收集标记失败会保留重试资格。

## 原生撤回与受控恢复

固定官方 RubyGems 客户端可撤回一个已发布的版本及平台变体：

```sh
export GEM_HOST_API_KEY='<当前仓库写入凭据>'
gem yank owned_top --version 1.2.0 \
  --host https://gateway.example.com/rubygems/ruby-internal
# 指定平台变体：
gem yank owned_variant --version 1.0.0 --platform java \
  --host https://gateway.example.com/rubygems/ruby-internal
```

客户端向该 host 下的 `DELETE /api/v1/gems/yank` 发送
`application/x-www-form-urlencoded` 请求体。总量不超过 4 KiB，只接受单值
`gem_name`、`version` 和可选 `platform`。省略或空平台精确表示 `ruby`，
不会选中其它平台。版本查找沿用发布时的规范化身份。未知或重复字段、
未支持的 `ruby_abi`、错误编码、非规范平台均明确拒绝。请求必须具备该
精确包名的当前写权限；等待共享身份锁后重新认证并核验授权。

撤回成功返回 200，并原子地将目标身份从 compact/legacy 索引、quick
gemspec、原包 GET/HEAD 中隐藏，携带旧 ETag 的请求也不能读取。空缓存
客户端即使持有依赖锁文件，也无法安装被撤回的包；已下载的客户端副本
仍保留在客户端。Gateway 保留原始字节、不可变身份与容量占用，过期上传
收集器不会删除这些 committed 对象。同字节或不同字节的重新 push 都不能
绕过撤回。重复原生 yank 成功，但不重复改变状态或记录成功审计。

当前仓库管理员可读取保留的治理状态：

```text
GET /api/v2/previews/rubygems/repositories/{repositoryId}/packages/{gemName}/{gemVersion}/{platform}/governance
Authorization: Bearer <仓库管理员凭据>
```

响应包含 `repositoryId`、`name`、`version`、`platform`、`digest`、
`governanceVersion` 和 `yanked`；发生过变更时另含 `updatedAt`、`updatedBy`
和 `reason`。治理版本初始为 `1`。根据读回结果向相同路径 POST：

```json
{
  "expectedDigest": "sha256:<原始 SHA-256>",
  "expectedGovernanceVersion": "2",
  "action": "restore",
  "reason": "已核实保留包并批准恢复"
}
```

POST 支持 `yank` 和 `restore`，要求精确包名的当前仓库管理员权限、匹配的
摘要与治理版本，以及不超过 2,048 个 Unicode 字符的非空白理由。过期决策
返回 409。恢复会重新读取并验证原始完整大小/SHA-256、归档、解析身份及
元数据，再次核验当前凭据与管理员授权；缺失或损坏的内容保持隐藏。状态与
审计同事务提交，治理版本单调递增。携带当前版本的同状态请求不产生新状态
或重复审计；管理响应丢失后，先重新 GET 再决定是否重试。

此切片提供保留字节的 Hosted 治理。隔离、硬删除、物理回收、Proxy/Group
传播与通用生命周期准入仍由父路线图承接。原生协议依据固定
[yank 客户端](https://github.com/rubygems/rubygems/blob/v3.6.7/lib/rubygems/commands/yank_command.rb)
和 [RubyGems 移除语义](https://guides.rubygems.org/removing-a-published-gem/)。

## 原生契约与验证

`/rubygems/{repository}/` 下提供 `names`、`versions`、`info/{name}`、
`specs.4.8.gz`、`latest_specs.4.8.gz`、`prerelease_specs.4.8.gz`、
`quick/Marshal.4.8/{fullName}.gemspec.rz`、`gems/{fullName}.gem`。
GET/HEAD 使用相同授权，支持 ETag 条件读取；Range 请求获得当前完整的 200
表示。compact MD5 是协议缓存校验，不可变包身份和内容完整性使用 SHA-256。

运行 `scripts/native-rubygems-e2e.sh` 执行独占原生门禁。门禁固定
[官方 Ruby 镜像](https://hub.docker.com/_/ruby)
`ruby:3.4.4-alpine@sha256:81096866ac15f906adc79867da3ed97a2aa271d6149363e216a174701345c53b`，
并严格核对镜像实际自带的 **Ruby 3.4.4 / RubyGems 3.6.7 / Bundler 2.6.7**。
它按记录的 SHA-256 核对官方
[gemspec 序列化](https://github.com/rubygems/rubygems/blob/v3.6.7/lib/rubygems/specification.rb)、
[push host 拼接](https://github.com/rubygems/rubygems/blob/v3.6.7/lib/rubygems/gemcutter_utilities.rb)、
[compact 索引解析](https://github.com/rubygems/rubygems/blob/v3.6.7/bundler/lib/bundler/compact_index_client/parser.rb)
源码，协议行为也依据官方 [push API](https://guides.rubygems.org/rubygems-org-api/)。

门禁构建真实 Gateway 二进制，启动自有 PostgreSQL/RustFS，以断网容器执行
`gem build`，再执行原生 push、空缓存 gem/Bundler 三层依赖安装、compact 和
full-index 两种消费、显式预发布安装与平台包 fetch；物理重启 Gateway、
PostgreSQL 和对象存储后再次冷消费。透明 HTTP 转发 observer 记录响应，
不 mock 认证、协议响应、字节或持久化。原包目录只供 build/push 挂载，每个
消费者使用独立、初始为空的工作目录、HOME 和缓存树，gem install 显式要求
远端解析。每阶段独立断言完整目标 `.gem` GET 的大小、SHA-256、解析后的身份
和依赖与原包一致，重启后的两个冷客户端也分别验证，不能借其他阶段的请求
通过断言。七个原包的大小/SHA/身份、分阶段响应证据、原始客户端
输出、JSON 测试结果、源码/工具来源和资源清理回执保存在
`output/native-rubygems-e2e/<run>/`。缺失客户端不能通过 skip 放行。
这些是 fixture 本地或 CI 验收结果，不代表生产部署，也不代表已验证所有
RubyGems 平台的扩展执行。

SIGTERM/SIGINT 取消保留非零结果，收齐自有命令进程组，并在有界清理后写回执。
离线取消回归把原字节 producer 复制到临时仓库，以 Docker 替身运行，不使用
真实 daemon，也不向原生证据目录加入故意失败的运行。

固定客户端还分别撤回 `ruby` 与 `java` 变体，并重复一次 yank 验证幂等。
对被撤回的传递依赖，空缓存 gem 与持有原依赖锁文件的 Bundler 必须在物理
重启前后都失败；管理恢复后，两种冷客户端再次获得原始摘要闭环。预期的
原生拒绝保留非零命令状态，不计作跳过测试或成功安装。
