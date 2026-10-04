# 仓库逻辑配额告警

[English](repository-quota-alerts.md) | [文档索引](README.zh-CN.md)

本批实现默认关闭的有限内置评估：仓库逻辑字节持续超限 → PostgreSQL 状态与
不可变事件 → [已有 TLS 邮件通道](email-notification-delivery.zh-CN.md)。
对应 [#237](https://github.com/ccsert/artifact-gateway/issues/237)，属于
[#211](https://github.com/ccsert/artifact-gateway/issues/211)、
[#212](https://github.com/ccsert/artifact-gateway/issues/212)、
[#229](https://github.com/ccsert/artifact-gateway/issues/229) 与
[#230](https://github.com/ccsert/artifact-gateway/issues/230) 的部分交付。
它不评估本地挂载或 5xx；S3/NAS 物理池容量仍为 unknown。平台管理员可在 Console
**系统 → 配额告警**（`/system?tab=alerts`）管理规则并查看安全交付证据。

适合小规模私有化部署的推荐仍是复用必需的 PostgreSQL、Scheduler 与 Worker，
避免额外维护告警服务；代价是产品承担规则与邮件生命周期，而且 Gateway/PG 全停
时无法自发通知。需要独立停机检测时使用已有外部 availability watcher。
Prometheus/Alertmanager 可承担外部观测和路由，但需要独立安装、升级与配置，
不是这条有限仓库配额规则的前置条件。

## Console 使用流程

先通过已有部署配置和管理员 API 配置邮件通道、加密密钥与收件人目标。Console
只显示目标名称、语言、启停状态、收件人已配置标志及绑定版本，不回读地址、凭据
或 SMTP 配置。

创建规则时显式填写警告/严重/恢复百分比、各自持续时间与最大样本年龄。
百分比最多两位小数，精确转换为整数基点。新规则始终停用；创建或保存不会发送测试
邮件。启用、停用和删除需要单独明确确认。通道或密钥不可用时仍可保存停用规则；
目标版本变更后，先编辑保存重新绑定，才能启用。

保存冲突保留草稿，需重新读取配置。权限或强制改密拒绝会跨取消/返回阻断写入；
只有明确刷新成功且期间没有更新的权限拒绝，才能恢复写入。页面可见时每 30 秒读取，
后台暂停，离开页签停止；读取失败保留上次快照并标明过期。

规则严重度与 unknown/stale 证据分开呈现。最新 50 条不可变事件保留当时策略与采样
证据；交付详情显示尝试次数、重试时间、有限失败/取消原因及可能重复。
**SMTP 服务器已接受**不表示收件箱送达。不增加浏览器严重度评估、收件人持久化、
自动启用或自动发送。邮件目标网页管理、模板预览、明确合成测试与重放留给后续小批；
不改变生产中继、收件人或部署配置。

## 显式配置与权限

仅平台管理员可以读写；强制改密用户不能调用。所有响应（含早期错误）使用
`Cache-Control: no-store`。每条规则绑定真实 Repository UUID 与已有邮件目标。
同一仓库至多一条未删除规则；规则总数最多 100，包含保留的软删除历史。
收件人、SMTP、凭据、对象路径与日志内容不进入规则或事件响应。

| API | 行为 |
| --- | --- |
| `GET/POST /api/v2/repository-quota-alert-rules` | 列出规则 / 默认停用创建 |
| `GET/PUT /api/v2/repository-quota-alert-rules/{ruleId}` | 查询状态 / `If-Match` 配置 CAS |
| `DELETE /api/v2/repository-quota-alert-rules/{ruleId}` | `If-Match` 软删除，保留状态与事件 |
| `GET /api/v2/repository-quota-alert-rules/{ruleId}/events` | 最新 50 条事件与安全邮件状态 |

PUT 替换配置，仓库作用域不可改变。原样保存不递增配置版本或产生变更审计。
开启要求部署邮件配置启用、加密能力可用、邮件目标启用；只离线校验，不探测 SMTP。
目标版本写入规则。目标变更后需管理员 CAS 更新规则，才能绑定其新版本。

所有阈值、持续与新鲜度必须显式提供，没有生产默认策略。比例采用基点：
10000 表示 100%；须满足 `0 < recovery < warning < critical <= 10000`。
三种持续时间为 1–86400 秒，新鲜度为 30–3600 秒。
以下仅是合成示例，不会自动写入或启用部署：

```json
{
  "repositoryId": "00000000-0000-4000-8000-000000000001",
  "targetId": "00000000-0000-4000-8000-000000000002",
  "enabled": false,
  "policy": {
    "warningBasisPoints": 8500,
    "criticalBasisPoints": 9500,
    "recoveryBelowBasisPoints": 8000,
    "warningForSeconds": 300,
    "criticalForSeconds": 120,
    "recoveryForSeconds": 180,
    "maxSampleAgeSeconds": 60
  }
}
```

## 状态与证据

逻辑容量沿用各格式现有统计口径；Cargo 排除 `collected_at` 已设置的回收对象。
正配额与使用量从同一 PostgreSQL 语句快照读取，比例用精确整数比较，避免溢出、
舍入或分开读取配额与用量。邮件只展示该逻辑口径，不推断物理剩余空间。

警告与严重分别累计连续有效证据，严重可先于较长的警告持续时间触发。
normal → warning → critical；critical 保持，直到逻辑使用比例严格低于恢复阈值
并持续达到 recoveryForSeconds，再生成 resolved。仅达到警告以下不会把 critical
降级。一次关联周期每种转换最多一条事件；本批关闭周期提醒，无重复告警邮件。
恢复后新一轮超限仍须完整持续证据才能开启下一周期。

quota=0 或缺失表示未配置/无限配额，`not_configured` 不参与告警。
仓库停用/删除、取数失败、陈旧、未来或无效采样均清空连续计时，保留 severity、
关联周期和最后有效值，绝不生成恢复。Scheduler 每 15 秒取样，超过 30 秒的采样
缺口重新累计，重启不把停机期间算入连续时间。GET 只投影新鲜度 stale，不写状态、
评估或发送。配置修改、停用与软删除同样保留活动严重性，不代表资源已恢复。

状态分为 normal/pending/firing 与独立 dataState。使用值是最后有效证据；unknown/
stale 时不能当当前用量。配置 version 与评估 stateVersion 分开，正常采样不破坏
配置 CAS。事件保留规则版本、仓库名/ID、用量/配额、完整策略、UTC 时间、连续证据
起点、event/episode/前一事件 ID 和规则全局顺序；可关联已有 email-delivery ID。

## 一致性与交付

Scheduler 节点运行评估，Worker 节点运行已有 `email` worker。每轮最多 100 条规则、
总预算 5 秒。PostgreSQL 行锁、SKIP LOCKED 与到期时间避免多实例重复推进。
状态、关联周期、不可变事件和邮件快照同事务提交，事务内没有外部网络 I/O。
持有配置行锁时评估，不以应用节点 wall clock 计算持续时间。

邮件目标关闭、版本改变、加密能力不可用或部署邮件关闭时，事件记录明确未交付原因；恢复配置后
不会偷偷补发这些历史转换，也不会因重新绑定目标而重复发送仍在持续的告警。
每轮离线检测加密能力，不解密目标或探测 SMTP。缺少加密能力的新转换记录
`encryption_key_unavailable` 且不创建邮件；此前已入队邮件仍遵守既有有限重试语义。
有效路由复用已有 1000 条活动邮件上限，队列满时保留 `queue_full` 的 dead 投递，
可在容量允许后由管理员明确 CAS 重放。自动告警不占合成测试的每分钟 5 次额度。

同一规则跨周期按全局 sequence 领取，前序 pending/retrying/delivering 阻塞后续，
accepted/dead 后才继续。配置改变、停用或删除取消尚未领取的旧 pending/retrying
邮件，状态为 dead 和 `rule_changed`/`rule_disabled`/`rule_deleted`。已领取的 SMTP
许可可完成一次，无法撤回网络发送；持久撤销标记禁止本次失败或租约过期后的自动
重试/重领。管理员显式历史重放清除此标记，重新授权有限重试，仍受目标版本和中继门禁。

重用已有租约、fencing、8 次有限重试与 possibleDuplicate 语义。accepted 仅表示
SMTP 中继接受，不表示邮箱送达；DATA 后断开或重启可能造成重复，无法 exactly-once。
From、部署 Console origin、加密收件人、locale 与事件证据不可变，重试不漂移。

配额模板 `quota-1` 中英三态均有颜色与文字标识、响应式 HTML、等价纯文本以及固定
Outlook wrapper；无外部图片、字体、跟踪像素或附件。只使用预验证 HTTPS origin
生成固定诊断链接。新合成预览为版本 2；已排队版本 1 保留原文案与渲染供历史重放。

## 验证与后续

公开 HTTP、存储与 TLS 发送边界使用合成数据：Raw HTTP 实际写入 → 逻辑容量变化
→ PG 状态/事件 → 新进程邮件领取 → 自有 TLS 收信夹具 → 实际双语 multipart MIME。
固定时间轴覆盖持续、滞回、直接严重、unknown/stale、重启缺口、大整数；隔离 PG
覆盖双实例、配置围栏、队列满、读失败和事件写失败的原子回滚。

这不等于真实 Gmail/Outlook/Apple Mail 客户端矩阵、真实渠道启用或生产验收。
终态历史保留/归档、Console 手动邮件重放、显式本地挂载告警和进程
5xx 告警仍是后续切片。
真实 relay、收件人和部署阈值需单独授权；本批不创建凭据、不发送真实邮件、不改生产。
