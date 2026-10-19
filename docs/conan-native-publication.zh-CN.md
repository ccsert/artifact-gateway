# Conan 2 原生发布

[English](conan-native-publication.md) · [协议兼容性](protocol-compatibility.zh-CN.md)

唯一规范远程入口为 `/conan/<repository>`，Conan 自行附加 `/v1/ping` 或 `/v2/...`。
Hosted、Proxy、Group 使用同一布局；`/conan/v2/<repository>` 没有别名，直接返回 404。
Hosted 接收上传，Proxy/Group 禁写。已有管理 Session、修订浏览、生命周期和审计 API 继续可用；
管理与原生发布均要求完整且校验通过的修订。

Conan 仓库及 Group 保留名称 `v2`，避免旧布局重新成为可达入口。

```sh
conan remote add releases https://gateway.example.com/conan/releases
conan remote login releases ci -p "$SERVICE_ACCOUNT_CREDENTIAL"
conan create . --user=team --channel=stable -nr
conan upload 'widget/1.0@team/stable#*:*#*' -r releases --confirm
conan list 'widget/1.0@team/stable#*:*#*' -r releases
conan download widget/1.0@team/stable -r releases
conan install --requires=widget/1.0@team/stable -r releases --build=never
```

名称和版本应替换为真实 Recipe。`agctl repo connect` 与 Console 用法片段给出相同入口，
agctl 不重复实现 Conan 上传协议。使用 Group 时，发布到其 Hosted 成员，从 Group 规范入口安装。

## 认证与权限

服务端配置独立 `GATEWAY_CONAN_TOKEN_SECRET`，至少 32 字节，可用 `openssl rand -hex 32`
单独生成；不得与管理员或 Resolver 登录凭据相同。密钥保持私有，多副本使用同一值。
留空不影响其他格式，但官方认证握手明确返回 `501/not_supported`；短密钥或复用登录秘密会
在配置加载时拒绝。连接示例需要服务端先完成此配置，publish 能力声明不提供缺失的认证材料。

Conan Basic 登录接受本地机器账户凭据或配置的 Resolver 凭据，换取绑定精确仓库/Group ID
的五分钟令牌。令牌只含凭据摘要，不含原文；逐请求回查源凭据撤销与机器账户状态。
源凭据和签名密钥轮换都会使旧令牌失效，令牌不能跨远程仓库使用。交互用户密码、OIDC
浏览器会话、独立 API Key 不支持此交换。客户端令牌、上游凭据与签名材料分开，不能转发
上游。显式错误认证不能回退匿名访问。

读取需要仓库 read，PUT 需要精确 Recipe/Package 资源 write；发现、会话状态与结果回读
也需要 read。握手成功不授予制品访问权限。

## 可见边界、重试与生命周期

每个原生文件 PUT 要求 `X-Checksum-Sha1`，服务端有界暂存并验证长度、SHA-1 和独立存储 SHA-256。
以发布者、reference、RREV、Package ID、PREV 打开或恢复一小时持久 Recipe/Package Session。
每次尝试使用新不可变对象 key，在既有 Session/object intent 表中写入带版本指针。
未完整提交的修订不会出现在协议索引中。

官方客户端最后上传 `conanmanifest.txt`。服务端先回读所有暂存资产，逐字节核 size/SHA-256，
再流式校验完整展开树的每项 MD5、RREV/PREV 和由 `conaninfo.txt` 派生的 Package ID。
最终在同一元数据事务写不可变修订、会话 committed 和以真实提交调用者为 Actor 的 publication audit；Session 版本阻止
验证期间的并发指针变化。同内容重放只保留一个可见身份；已有身份不同字节返回 409。
缺失或错误资产、manifest 或身份不一致返回 422，权限拒绝不会公开修订。

管理发布保留原有 HTTP 请求和响应形状：
`POST /api/v2/repositories/{id}/conan-publish-sessions` 声明完整 Recipe/Package
文件集合及精确 SHA-256、大小；
`PUT /api/v2/conan-publish-sessions/{id}/objects/{name}` 验证声明并以新不可变 key 暂存；
`POST /api/v2/conan-publish-sessions/{id}:commit` 校验完整暂存快照后才切换可见。
两个入口共用 manifest、资产、身份校验及版本 fence 保护的 revision/session/audit 原子提交。
审计 Actor 是真实认证的 commit 调用者；获准调用者与会话创建者不同时也不冒用创建者身份。
成功创建会话或上传对象不公开部分修订。旧式非 hash 身份、缺 manifest 或不完整资产声明不再接受。

上传中断后，用相同缓存、身份、目标及凭据重跑原 `conan upload`。未过期会话可跨 Gateway、
PostgreSQL 和 S3 服务重启继续；过期会话被 fencing，重试获得全新暂存 key。失败 commit
保留 open 会话且修订不可见；配额 507 可在容量恢复后重试。已授权调用者写 Group/Proxy
返回 405；错误上传路径或文件返回 400；不支持的握手方法返回 404。

沿用容量计费、异步发布扫描和 Recipe revision 隔离闭包。Recipe 隔离期间也拒绝追加 Package，
解除后可合法重试。Retention 只评估可见修订；未过期 open 会话对象不回收，遗弃 intent
经过 collector 宽限期后可回收，已提交可见引用保留物理字节。没有新增存储后端或另一套
发布系统；既有完整修订晋升、复制、墓碑/恢复与回收契约继续生效。

成功 PUT 返回 `X-Artifact-Gateway-Publish-Session` 和 `X-Artifact-Gateway-Publish-State`
（open/committed）；管理 Session GET 可查同一会话，但管理对象 PUT/commit 不得修改原生会话或绕过
manifest 校验。Console 制品视图只显示 committed 修订；展开 Conan `publish` 审计记录可看到
持久发布会话 UUID 和本地化的“已提交”状态，并结合既有坐标、机器 Actor 关联。
摘要等其他 Evidence 仍可通过审计 API 获取；缺失或格式错误的发布证据显示为不可用。
本单不声明新增 Console 上传表单。

## 支持范围与验证

固定官方客户端 **Conan 2.21.0**，支持其默认 hash revision 流程：小写 32 位十六进制
RREV/PREV、40 位 Package ID；Recipe 为 `conanfile.py` 或 `conan_export.tgz`，可选
`conan_sources.tgz`；Package 要求 `conaninfo.txt`、`conan_package.tgz`；均要求 manifest。
单次上传和合计展开树（包含 tar framing）上限 1 GiB，manifest/conaninfo 上限 1 MiB，
manifest 与每个归档最多 4096 条目。只校验而不解压执行；不安全链接、穿越、重复、
截断 gzip/tar、取消或不支持的条目类型均失败关闭。不支持 Conan 1、SCM revision、可选
metadata 上传、其他压缩格式和任意远程索引聚合。

`make conan-e2e` 创建私有固定客户端、唯一 PostgreSQL/RustFS Compose 工程与每轮动态端口。
真实 create/upload/list/download/install 覆盖 Memory 与 PostgreSQL/S3；主动切断上传，
恢复前重启两个持久服务，读取前再次重启；从生产者本地缓存冻结 RREV/Package ID/PREV，并在进入 Gateway 前捕获精确七个上传输入；
以这些独立输入核 HTTP/S3 完整字节及元数据 size/SHA-256/身份，再核 publication audit
和扫描调度，最后从 Group 做空缓存 target-only 安装。
脚本要求指定 fixture 实际 pass，skip 不能变绿；完成清理进程、凭据、容器、卷和网络。
不访问生产或外部 Nexus。

单元/存储契约覆盖认证、配额、不可变冲突、并发发布、快照 fencing、过期与 open 会话 GC
保护；已有管理发布/读取与生命周期用例继续回归。公开 HTTP 与 Memory/PostgreSQL 合同也覆盖
普通管理会话和原生会话并发：即使全部声明字节及 SHA-256 匹配，也不能以错误 manifest 发布、
公开元数据或替换暂存字节；两个入口都核对真实审计 Actor 与会话快照。
本机证据只对应所测源码树，CI、合并与
发布 SHA 分开验收。kkRepo 固定 `2d0ce09290dbd9a2f47a5d4df7a373337a503c36` 的
ConanService、ConanLeaseManager、manifest policy 与黑盒测试仅作为源码/夹具参考，
不声称已实际运行 kkRepo。
