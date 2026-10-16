# agctl 命令地图

[English](cli-command-map.md) · [文档索引](README.zh-CN.md) · [项目中文 README](../README.zh-CN.md)

`agctl` 以一份命令面服务两类导航：CI（接入、身份、发布、检索/验证、审计）与
平台管理（身份授权、仓库/Group、数据治理、配置策略、日志审计诊断容量、告警
通知、异步运维/本地入口）。资源命令、HTTP/JSON、凭据与权限只有一份；agent
面向两类都用同一自省。

本页是对完整契约的归类，不是对已安装二进制的描述。除 #125 骨架（`version`、
`info`、`commands`）和阶段列标为 `implemented` 的行（#130 的仓库与 Group 命令、
#131 的制品与格式视图、#132 的 Raw 与 Maven 发布命令、#133 的服务账号、凭据与 API key 命令、#134 的授权、角色、
模板、访问检查与匿名开关命令、#336 的配额告警与邮件命令（M2）、#335 的用户、会话与身份命令，以及
#135 的只读审计、status、诊断、运行节点与运行日志、概览和生命周期任务命令，均由 `make cli-e2e` 在真实 Gateway 上验收）外，
全部为计划，不宣称已实现；只有注册命令、文档/测试和真实 Gateway 验收齐备，
某一行才转为 implemented/verified。

覆盖基线：`api/openapi/management-runtime-v1.json`（提交
`a504ba48cd93371badcb1f2781b120b359e57b69`，127 路径 / 177 操作），加上 #366 新纳入契约的四条 Conan 会话操作（当前 131 路径 / 181 操作），以及
`api/openapi/native-hosted-v1.json` 中 CLI 需要触达的协议端点。计数由下方
漂移守卫重新推导，不固定在本页；契约变化时先更新本页。最近一次核对：
2026-10-11（#126 补充）。

## 阶段图例

- M0：基础——注册/配置/安全输出/自省/测试/分发与完整地图，#125–#128。
- M1：首版计划，#129–#136 + #335；首版范围不是全部平台管理能力。
- M2：后续计划，#137–#139 + #336，及 #131/#134 中标注的进阶行。
- N/A：明确不由 CLI 直接公开（例如兼容整表 grant 替换 PUT）；API 本身保留。
- LOCAL：已存在于 gateway 本地二进制，不等于 agctl 已有同名远程命令。
- BLOCKED：无管理 API 或后端能力尚未交付；列依赖，不能通过虚构 HTTP 端点补“覆盖”。

## 权限图例（服务端为最终权威）

权限以服务端为最终权威，默认身份/账户状态必须照实拒绝；CLI 不增加授权：

- PUBLIC：公开读取；I：当前已认证主体；I/A：本人查询，模拟其他主体需平台 admin。
- A：平台 admin；R/W/RA/RI：对应仓库 repositories:read/write/admin/intelligence（平台 admin 保留服务端已有行为）。
- R*：仓库/Group browse helper 的读取授权及明确配置的匿名只读分支，不能默认匿名。
- FR/FA：服务端跨仓可读/可管理范围过滤；ST：仓库任务需 RA，平台任务需 A，列表按可管理范围过滤。
- G：现有 Group 成员管理门禁（平台 A 或管理全部已绑定成员；未知/无绑定成员需 A）；RA×2：源、目标分别治理授权。

依据：`internal/app/hosted_repository_api.go`、`repository_access_api.go`、
`hosted_group_api.go`、`scheduled_tasks_api.go` 及各领域 handler；
[已接受 ADR 0006](adr/0006-account-levels-and-repository-grants.zh-CN.md)。

## 验收代码图例

每行验收代码必须落实为对应 operationId 的合成输入、已知字面期望和真实隔离
Gateway E2E，不只检查 HTTP 200：

- Q：准确 schema/字段/可见性、实际分页范围/续页、空结果及权限拒绝，遵守 #125 部分结果/unknown 契约。
- W：变更后按既有读入口回读状态/审计；无权、错误输入、危险确认与 client-plan dry-run 不产生业务变更。
- C：W 加实际 If-Match/版本冲突，412 不覆盖新值。
- S：一次性秘密安全文件交付，缺目的地请求前拒绝，默认输出/日志无秘密，写失败不重签（#133）。
- G：单行授权并发互不覆盖/模板 apply 影响与 CAS、effective-access 和匿名组合门禁（#134）。
- J：accepted 与终态分开，原 ID 有界 wait、失败/超时/重试与实际 cancel 支持、已声明幂等/CAS（#125/#137）。
- U：publish session 与逐对象大小/摘要、分阶段幂等回读、未知提交和部分失败（#132）。
- L：local/session 日志、游标与重启/503/有限 NDJSON、脱敏及管理员门禁（#135）。
- D：使用真实 server-preview 或明确 client-plan，配置/对象/通知队列不变；其输出不得称完整执行验收。
- N：不提供该直接命令；守卫保留行上的明示理由，不通过 CLI 绕到同一兼容 PUT。

## 管理契约逐操作映射

下表 181 行覆盖基线 `api/openapi/management-runtime-v1.json` 的完整操作集；
守卫从文件重新推导该集合，不信任此计数。路径均相对 `/api/v2`（契约的
server base）。计划命令省略前导 `agctl`。

| API（均相对 /api/v2）/ operationId | 计划命令（省略 agctl） | 承接 | 阶段 | 权限 | 验收 |
| --- | --- | --- | --- | --- | --- |
| `GET /repository-quota-alert-rules/{ruleId}/events`<br>`listRepositoryQuotaAlertEvents` | `quota-alert events <rule>` | #336 | M2 implemented | A | Q |
| `GET /repository-quota-alert-rules/{ruleId}`<br>`getRepositoryQuotaAlertRule` | `quota-alert show <rule>` | #336 | M2 implemented | A | Q |
| `PUT /repository-quota-alert-rules/{ruleId}`<br>`updateRepositoryQuotaAlertRule` | `quota-alert update <rule>` | #336 | M2 implemented | A | C |
| `DELETE /repository-quota-alert-rules/{ruleId}`<br>`deleteRepositoryQuotaAlertRule` | `quota-alert delete <rule>` | #336 | M2 implemented | A | C |
| `POST /repository-quota-alert-rules`<br>`createRepositoryQuotaAlertRule` | `quota-alert create` | #336 | M2 implemented | A | W |
| `GET /repository-quota-alert-rules`<br>`listRepositoryQuotaAlertRules` | `quota-alert list` | #336 | M2 implemented | A | Q |
| `GET /email-notifications`<br>`getEmailNotificationCapability` | `email capability` | #336 | M2 implemented | A | Q |
| `POST /email-notifications:test`<br>`testEmailNotification` | `email test` | #336 | M2 implemented | A | J |
| `GET /email-targets`<br>`listEmailTargets` | `email target list` | #336 | M2 implemented | A | Q |
| `POST /email-targets`<br>`createEmailTarget` | `email target create` | #336 | M2 implemented | A | W |
| `GET /email-targets/{targetId}`<br>`getEmailTarget` | `email target show <id>` | #336 | M2 implemented | A | Q |
| `PUT /email-targets/{targetId}`<br>`updateEmailTarget` | `email target update <id>` | #336 | M2 implemented | A | C |
| `GET /email-deliveries`<br>`listEmailDeliveries` | `email delivery list` | #336 | M2 implemented | A | Q |
| `GET /email-deliveries/{deliveryId}`<br>`getEmailDelivery` | `email delivery show <id>` | #336 | M2 implemented | A | Q |
| `POST /email-deliveries/{deliveryId}:replay`<br>`replayEmailDelivery` | `email delivery replay <id>` | #336 | M2 implemented | A | J |
| `POST /email-notifications:preview`<br>`previewEmailNotification` | `email preview` | #336 | M2 implemented | A | D |
| `GET /formats`<br>`listFormatProfiles` | `formats list（info 复用）` | #125 | M0 | I | Q |
| `GET /identity`<br>`getCurrentIdentity` | `auth whoami（info 复用）` | #129 | M1 | I | Q |
| `GET /artifact-search`<br>`searchArtifacts` | `artifact search --global` | #131 | M1 implemented | FR | Q |
| `GET /anonymous-access-policy`<br>`getAnonymousAccessPolicy` | `access anonymous show` | #134 | M1 implemented | A | Q |
| `PUT /anonymous-access-policy`<br>`replaceAnonymousAccessPolicy` | `access anonymous set` | #134 | M1 implemented | A | C |
| `GET /site-settings`<br>`getSiteSettings` | `site-settings show（theme list 复用 availableThemes）` | #139 | M2 | PUBLIC | Q |
| `PUT /site-settings`<br>`replaceSiteSettings` | `site-settings set` | #139 | M2 | A | C |
| `POST /console-themes:validate`<br>`validateConsoleThemePackage` | `theme validate --remote` | #139 | M2 | A | W |
| `POST /console-themes`<br>`installConsoleThemePackage` | `theme install --remote` | #139 | M2 | A | W |
| `PUT /console-themes/{themeId}`<br>`replaceConsoleThemePackage` | `theme replace --remote <id>` | #139 | M2 | A | C |
| `DELETE /console-themes/{themeId}`<br>`deleteConsoleThemePackage` | `theme delete --remote <id>` | #139 | M2 | A | C |
| `GET /authentication/oidc`<br>`getOIDCSettings` | `oidc settings show` | #139 | M2 | A | Q |
| `PUT /authentication/oidc`<br>`replaceOIDCSettings` | `oidc settings set` | #139 | M2 | A | C |
| `POST /authentication/oidc:test`<br>`testOIDCSettings` | `oidc settings test` | #139 | M2 | A | W |
| `GET /webhook-subscriptions`<br>`listWebhookSubscriptions` | `webhook list` | #137 | M2 | A | Q |
| `POST /webhook-subscriptions`<br>`createWebhookSubscription` | `webhook create` | #137 | M2 | A | W |
| `GET /webhook-subscriptions/{subscriptionId}`<br>`getWebhookSubscription` | `webhook show <id>` | #137 | M2 | A | Q |
| `PUT /webhook-subscriptions/{subscriptionId}`<br>`updateWebhookSubscription` | `webhook update <id>` | #137 | M2 | A | C |
| `GET /webhook-deliveries`<br>`listWebhookDeliveries` | `webhook delivery list` | #137 | M2 | A | Q |
| `GET /webhook-deliveries/{deliveryId}`<br>`getWebhookDelivery` | `webhook delivery show <id>` | #137 | M2 | A | Q |
| `POST /webhook-deliveries/{deliveryId}:replay`<br>`replayWebhookDelivery` | `webhook delivery replay <id>` | #137 | M2 | A | W |
| `GET /runtime/nodes`<br>`listRuntimeNodes` | `runtime nodes` | #135 | M1 implemented | A | Q |
| `GET /diagnostics`<br>`getDiagnostics` | `diagnostics（status 复用）` | #135 | M1 implemented | A | Q |
| `GET /runtime/logs`<br>`listRuntimeLogs` | `runtime logs` | #135 | M1 implemented | A | L |
| `GET /api-keys`<br>`listApiKeys` | `api-key list` | #133 | M1 implemented | A | Q |
| `POST /api-keys`<br>`createApiKey` | `api-key create` | #133 | M1 implemented | A | S |
| `DELETE /api-keys/{apiKeyId}`<br>`revokeApiKey` | `api-key revoke <id>` | #133 | M1 implemented | A | W |
| `GET /service-accounts`<br>`listServiceAccounts` | `service-account list（show 按 ID 从列表派生）` | #133 | M1 implemented | A | Q |
| `POST /service-accounts`<br>`createServiceAccount` | `service-account create` | #133 | M1 implemented | A | W |
| `PUT /service-accounts/{serviceAccountId}`<br>`updateServiceAccount` | `service-account update <id>` | #133 | M1 implemented | A | C |
| `GET /service-accounts/{serviceAccountId}/credentials`<br>`listServiceAccountCredentials` | `credential list --service-account <id>` | #133 | M1 implemented | A | Q |
| `POST /service-accounts/{serviceAccountId}/credentials`<br>`createServiceAccountCredential` | `credential issue --service-account <id>` | #133 | M1 implemented | A | S |
| `DELETE /service-accounts/{serviceAccountId}/credentials/{credentialId}`<br>`revokeServiceAccountCredential` | `credential revoke <id> --service-account <id>` | #133 | M1 implemented | A | W |
| `GET /users`<br>`listUsers` | `user list` | #335 | M1 implemented | A | Q |
| `POST /users`<br>`createUser` | `user create` | #335 | M1 implemented | A | W |
| `GET /users/{userId}`<br>`getUser` | `user show <id>` | #335 | M1 implemented | A | Q |
| `PATCH /users/{userId}`<br>`updateUser` | `user update <id>` | #335 | M1 implemented | A | W |
| `DELETE /users/{userId}`<br>`deleteUser` | `user delete <id>` | #335 | M1 implemented | A | W |
| `POST /users/{userId}/password`<br>`resetUserPassword` | `user reset-password <id>` | #335 | M1 implemented | A | C |
| `POST /users/{userId}/sessions:revoke`<br>`revokeUserSessions` | `user sessions revoke <id> --all` | #335 | M1 implemented | A | C |
| `GET /users/{userId}/sessions`<br>`listUserSessions` | `user sessions list <id>` | #335 | M1 implemented | A | Q |
| `DELETE /users/{userId}/sessions/{sessionId}`<br>`revokeUserSession` | `user sessions revoke <id> --session <sid>` | #335 | M1 implemented | A | W |
| `GET /users/{userId}/identities`<br>`listUserIdentities` | `user identities list <id>` | #335 | M1 implemented | A | Q |
| `POST /users/{userId}/identities`<br>`createUserIdentity` | `user identities link <id>` | #335 | M1 implemented | A | W |
| `DELETE /users/{userId}/identities/{identityId}`<br>`deleteUserIdentity` | `user identities unlink <id> --identity <iid>` | #335 | M1 implemented | A | W |
| `GET /audits`<br>`listAudits` | `audit list --bounded` | #135 | M1 implemented | A | Q |
| `GET /audits/page`<br>`listAuditPage` | `audit list --cursor <cursor>` | #135 | M1 implemented | A | Q |
| `GET /overview-statistics`<br>`getOverviewStatistics` | `overview show` | #135 | M1 implemented | FR | Q |
| `GET /audit-retention-policy`<br>`getAuditRetentionPolicy` | `audit retention show` | #138 | M2 | A | Q |
| `PUT /audit-retention-policy`<br>`replaceAuditRetentionPolicy` | `audit retention set` | #138 | M2 | A | C |
| `POST /audit-retention:execute`<br>`executeAuditRetention` | `audit retention execute` | #138 | M2 | A | W |
| `GET /audit-retention/jobs`<br>`listAuditRetentionJobs` | `audit retention jobs` | #138 | M2 | A | Q |
| `GET /repositories`<br>`listRepositories` | `repo list` | #130 | M1 implemented | FR | Q |
| `POST /repositories`<br>`createRepository` | `repo create` | #130 | M1 implemented | A | W |
| `GET /repository-grants`<br>`listRepositoryGrants` | `grant list --all-repositories` | #134 | M1 implemented | FA | Q |
| `GET /authorization-templates`<br>`listAuthorizationTemplates` | `template list` | #134 | M1 implemented | A | Q |
| `POST /authorization-templates`<br>`createAuthorizationTemplate` | `template create` | #134 | M2 | A | W |
| `GET /authorization-templates/{templateId}`<br>`getAuthorizationTemplate` | `template show <id>` | #134 | M1 implemented | A | Q |
| `PUT /authorization-templates/{templateId}`<br>`updateAuthorizationTemplate` | `template update <id>` | #134 | M2 | A | C |
| `DELETE /authorization-templates/{templateId}`<br>`deleteAuthorizationTemplate` | `template delete <id>` | #134 | M2 | A | W |
| `POST /authorization-templates/{templateId}/apply`<br>`applyAuthorizationTemplate` | `template apply <id> --repo <id>` | #134 | M1 implemented | RA | G |
| `GET /authorization-roles`<br>`listAuthorizationRoles` | `role list` | #134 | M1 implemented | A | Q |
| `POST /authorization-roles`<br>`createAuthorizationRole` | `role create` | #134 | M2 | A | W |
| `GET /authorization-roles/{roleId}`<br>`getAuthorizationRole` | `role show <id>` | #134 | M1 implemented | A | Q |
| `PUT /authorization-roles/{roleId}`<br>`updateAuthorizationRole` | `role update <id>` | #134 | M2 | A | C |
| `DELETE /authorization-roles/{roleId}`<br>`deleteAuthorizationRole` | `role delete <id>` | #134 | M2 | A | W |
| `GET /repository-capacities`<br>`listRepositoryCapacities` | `capacity list` | #138 | M2 | FA | Q |
| `GET /lifecycle-jobs`<br>`listLifecycleJobs` | `lifecycle-job list --all-repositories` | #135 | M1 implemented | FA | Q |
| `GET /scheduled-tasks`<br>`listScheduledTasks` | `scheduled-task list` | #137 | M2 | ST | Q |
| `POST /scheduled-tasks`<br>`createScheduledTask` | `scheduled-task create` | #137 | M2 | ST | W |
| `GET /scheduled-tasks/{taskId}`<br>`getScheduledTask` | `scheduled-task show <id>` | #137 | M2 | ST | Q |
| `PUT /scheduled-tasks/{taskId}`<br>`updateScheduledTask` | `scheduled-task update <id>` | #137 | M2 | ST | C |
| `DELETE /scheduled-tasks/{taskId}`<br>`deleteScheduledTask` | `scheduled-task delete <id>` | #137 | M2 | ST | W |
| `POST /scheduled-tasks/{taskId}/run`<br>`runScheduledTask` | `scheduled-task run <id>` | #137 | M2 | ST | J |
| `GET /scheduled-tasks/{taskId}/runs`<br>`listScheduledTaskRuns` | `scheduled-task runs <id>` | #137 | M2 | ST | Q |
| `GET /repositories/{repositoryId}`<br>`getRepository` | `repo show <id>` | #130 | M1 implemented | R | Q |
| `DELETE /repositories/{repositoryId}`<br>`deleteRepository` | `repo delete <id>（异步 deleting → deleted）` | #130 | M1 implemented | RA | J |
| `PATCH /repositories/{repositoryId}`<br>`updateRepository` | `repo update <id>` | #130 | M1 implemented | RA | C |
| `POST /repositories/{repositoryId}/egress-proxy:test`<br>`testEgressProxy` | `egress-proxy test --repo <id>` | #139 | M2 | RA | W |
| `GET /repositories/{repositoryId}/grants`<br>`listGrants` | `grant list --repo <id>` | #134 | M1 implemented | RA | Q |
| `PUT /repositories/{repositoryId}/grants`<br>`replaceGrants` | `不直接公开整表 PUT；使用 grant 单行写或 template apply` | #134 | N/A | RA | N |
| `POST /repositories/{repositoryId}/grants`<br>`upsertGrant` | `grant upsert --repo <id>` | #134 | M1 implemented | RA | G |
| `DELETE /repositories/{repositoryId}/grants`<br>`deleteGrant` | `grant delete --repo <id>` | #134 | M1 implemented | RA | G |
| `GET /repositories/{repositoryId}/retention-policy`<br>`getRetentionPolicy` | `policy retention show --repo <id>` | #139 | M2 | R | Q |
| `PUT /repositories/{repositoryId}/retention-policy`<br>`replaceRetentionPolicy` | `policy retention set --repo <id>` | #139 | M2 | RA | C |
| `GET /repositories/{repositoryId}/security-policy`<br>`getSecurityPolicy` | `policy security show --repo <id>` | #139 | M2 | RA | Q |
| `PUT /repositories/{repositoryId}/security-policy`<br>`replaceSecurityPolicy` | `policy security set --repo <id>` | #139 | M2 | RA | C |
| `POST /repositories/{repositoryId}/security-policy:evaluate`<br>`evaluateSecurityPolicy` | `policy security evaluate --repo <id>` | #139 | M2 | RA | D |
| `GET /repositories/{repositoryId}/quarantine-read-policy`<br>`getQuarantineReadPolicy` | `policy quarantine-read show --repo <id>` | #139 | M2 | RA | Q |
| `PUT /repositories/{repositoryId}/quarantine-read-policy`<br>`replaceQuarantineReadPolicy` | `policy quarantine-read set --repo <id>` | #139 | M2 | RA | C |
| `GET /repositories/{repositoryId}/capacity`<br>`getRepositoryCapacity` | `capacity show --repo <id>` | #138 | M2 | R | Q |
| `PUT /repositories/{repositoryId}/capacity`<br>`replaceRepositoryCapacity` | `capacity quota set --repo <id>` | #138 | M2 | RA | W |
| `POST /repositories/{repositoryId}/retention:dry-run`<br>`dryRunRepositoryRetention` | `retention preview --repo <id>` | #138 | M2 | RA | D |
| `POST /repositories/{repositoryId}/retention:execute`<br>`executeRepositoryRetention` | `retention execute --repo <id>` | #138 | M2 | RA | C |
| `POST /repositories/{repositoryId}/promotions`<br>`createRepositoryPromotion` | `promotion create --repo <source> --target <target>` | #138 | M2 | RA×2 | W |
| `POST /repositories/{repositoryId}/replications`<br>`createRepositoryReplication` | `replication create --repo <source> --target <target>` | #138 | M2 | RA×2 | J |
| `GET /repositories/{repositoryId}/replications`<br>`listRepositoryReplications` | `replication list --repo <id>` | #138 | M2 | RA | Q |
| `GET /repositories/{repositoryId}/replications/{replicationPlanId}`<br>`getRepositoryReplication` | `replication show <plan> --repo <id>` | #138 | M2 | RA | Q |
| `DELETE /repositories/{repositoryId}/replications/{replicationPlanId}`<br>`deleteRepositoryReplication` | `replication cancel <plan> --repo <id>` | #138 | M2 | RA | W |
| `POST /repositories/{repositoryId}/restore`<br>`restoreRepositoryArtifact` | `artifact restore --repo <id>` | #138 | M2 | RA | W |
| `GET /repositories/{repositoryId}/lifecycle-jobs`<br>`listRepositoryLifecycleJobs` | `lifecycle-job list --repo <id>（show/wait 按 ID 过滤同一响应）` | #135 | M1 implemented | RA | Q |
| `GET /repositories/{repositoryId}/artifact-scans`<br>`getRepositoryArtifactScanStatus` | `artifact scan status --repo <id>` | #137 | M2 | RI | Q |
| `POST /repositories/{repositoryId}/artifact-scans`<br>`createRepositoryArtifactScan` | `artifact scan enqueue --repo <id>` | #137 | M2 | RI | J |
| `POST /repositories/{repositoryId}/artifact-scans:reconcile`<br>`reconcileRepositoryArtifactScans` | `artifact scan reconcile --repo <id>` | #137 | M2 | RI | W |
| `POST /repositories/{repositoryId}/lifecycle-jobs:reconcile-intelligence`<br>`reconcileRepositoryArtifactIntelligence` | `lifecycle-job reconcile-intelligence --repo <id>` | #137 | M2 | RA | W |
| `POST /repositories/{repositoryId}/lifecycle-jobs/{lifecycleJobId}/run`<br>`runRepositoryLifecycleJobNow` | `lifecycle-job run --repo <id> --id <job>` | #137 | M2 | RA | W |
| `POST /repositories/{repositoryId}/lifecycle-jobs/{lifecycleJobId}/retry`<br>`retryRepositoryLifecycleJob` | `lifecycle-job retry --repo <id> --id <job>` | #137 | M2 | RA | W |
| `POST /repositories/{repositoryId}/lifecycle-jobs/{lifecycleJobId}/cancel`<br>`cancelRepositoryLifecycleJob` | `lifecycle-job cancel --repo <id> --id <job>` | #137 | M2 | RA | W |
| `GET /repositories/{repositoryId}/tombstones`<br>`listRepositoryTombstones` | `tombstone list --repo <id>` | #138 | M2 | RA | Q |
| `POST /repositories/{repositoryId}/tombstones`<br>`tombstoneRepositoryArtifact` | `artifact tombstone --repo <id>` | #138 | M2 | RA | W |
| `GET /repositories/{repositoryId}/capabilities`<br>`getRepositoryCapabilities` | `repo capabilities <id>` | #130 | M1 implemented | R | Q |
| `GET /repositories/{repositoryId}/effective-access`<br>`getRepositoryEffectiveAccess` | `access check --repo <id>` | #134 | M1 implemented | I/A | Q |
| `GET /groups`<br>`listGroups` | `group list` | #130 | M1 implemented | A | Q |
| `POST /groups`<br>`createGroup` | `group create` | #130 | M1 implemented | G | W |
| `GET /groups/{groupId}`<br>`getGroup` | `group show <id>` | #130 | M1 implemented | A | Q |
| `PUT /groups/{groupId}`<br>`replaceGroup` | `group update <id>` | #130 | M1 implemented | G | C |
| `DELETE /groups/{groupId}`<br>`deleteGroup` | `group delete <id>` | #130 | M1 implemented | G | W |
| `GET /groups/{groupId}/browse`<br>`browseGroup` | `group browse <id>` | #130 | M1 implemented | R* | Q |
| `GET /groups/{groupId}/members`<br>`listGroupMembers` | `group members list <id>` | #130 | M1 implemented | A | Q |
| `PUT /groups/{groupId}/members`<br>`replaceGroupMembers` | `group members set <id>` | #130 | M1 implemented | G | C |
| `GET /groups/{groupId}/capacity`<br>`getGroupCapacity` | `capacity show --group <id>` | #138 | M2 | A | Q |
| `GET /groups/{groupId}/resolution`<br>`getGroupResolution` | `group resolution <id>` | #130 | M1 implemented | A | Q |
| `POST /repositories/{repositoryId}/publish-sessions`<br>`createPublishSession` | `publish create --repo <id>` | #132 | M1 implemented | W | U |
| `GET /repositories/{repositoryId}/artifacts`<br>`listArtifacts` | `artifact list --repo <id>` | #131 | M1 implemented | R* | Q |
| `GET /repositories/{repositoryId}/artifact-usage`<br>`listRepositoryArtifactUsage` | `artifact usage --repo <id>` | #131 | M1 implemented | R* | Q |
| `GET /repositories/{repositoryId}/artifact-search`<br>`searchRepositoryArtifacts` | `artifact search --repo <id>` | #131 | M1 implemented | R* | Q |
| `GET /repositories/{repositoryId}/browse`<br>`browseRepository` | `artifact browse --repo <id>（repo browse 别名）` | #131 | M1 implemented | R* | Q |
| `GET /repositories/{repositoryId}/artifact-identities`<br>`listRepositoryArtifactIdentities` | `artifact identities --repo <id>` | #131 | M1 implemented | R | Q |
| `GET /repositories/{repositoryId}/artifacts/{artifactId}`<br>`getArtifact` | `artifact show <id> --repo <id>` | #131 | M1 implemented | R* | Q |
| `DELETE /repositories/{repositoryId}/artifacts/{artifactId}`<br>`deleteArtifact` | `artifact delete <id> --repo <id>` | #131 | M1 implemented | W | W |
| `GET /repositories/{repositoryId}/artifact-intelligence`<br>`getArtifactIntelligence` | `artifact intelligence show --repo <id>` | #131 | M1 implemented | R* | Q |
| `PUT /repositories/{repositoryId}/artifact-intelligence`<br>`replaceArtifactIntelligence` | `artifact intelligence set --repo <id>` | #138 | M2 | RI | C |
| `GET /repositories/{repositoryId}/artifact-quarantine`<br>`getArtifactQuarantine` | `artifact quarantine show --repo <id>` | #131 | M1 implemented | R | Q |
| `PUT /repositories/{repositoryId}/artifact-quarantine`<br>`replaceArtifactQuarantine` | `artifact quarantine set --repo <id>` | #138 | M2 | RA | C |
| `GET /repositories/{repositoryId}/oci/images`<br>`listOCIImages` | `oci images --repo <id>` | #131 | M1 implemented | R* | Q |
| `GET /repositories/{repositoryId}/oci/manifests`<br>`listOCIManifests` | `oci manifests --repo <id>` | #131 | M1 implemented | R* | Q |
| `GET /repositories/{repositoryId}/maven/coordinates`<br>`listMavenCoordinates` | `maven coordinates --repo <id>` | #131 | M1 implemented | R* | Q |
| `POST /repositories/{repositoryId}/apt/publication-sessions`<br>`createAPTPublicationSession` | `apt publication-session create --repo <id>` | #138 | M2 | W | W |
| `GET /repositories/{repositoryId}/apt/publication-sessions/{sessionId}`<br>`getAPTPublicationSession` | `apt publication-session show --repo <id> --id <session>` | #138 | M2 | W | Q |
| `PUT /repositories/{repositoryId}/apt/publication-sessions/{sessionId}/package`<br>`uploadAPTPublicationPackage` | `apt publication-session upload --repo <id> --id <session>` | #138 | M2 | W | W |
| `GET /repositories/{repositoryId}/apt/lifecycle`<br>`getAPTLifecycleState` | `apt lifecycle show（snapshot list 复用 snapshots）` | #138 | M2 | RA | Q |
| `POST /repositories/{repositoryId}/apt/lifecycle`<br>`applyAPTLifecycle` | `apt lifecycle apply --repo <id>` | #138 | M2 | RA | W |
| `POST /repositories/{repositoryId}/apt/lifecycle/preview`<br>`previewAPTLifecycle` | `apt lifecycle preview --repo <id>` | #138 | M2 | RA | D |
| `POST /repositories/{repositoryId}/apt/snapshots/prune`<br>`pruneAPTSnapshots` | `apt snapshot prune --repo <id>` | #138 | M2 | RA | W |
| `POST /repositories/{repositoryId}/apt/snapshots`<br>`publishAPTRepositorySnapshot` | `apt snapshot publish --repo <id>` | #138 | M2 | W | W |
| `POST /repositories/{repositoryId}/apt/snapshots/restore`<br>`restoreAPTRepositorySnapshot` | `apt snapshot restore --repo <id>` | #138 | M2 | RA | W |
| `GET /repositories/{repositoryId}/apt/snapshots/{snapshotId}/archive`<br>`exportAPTRepositorySnapshot` | `apt snapshot archive --repo <id> --output <file>` | #138 | M2 | RA | Q |
| `GET /repositories/{repositoryId}/apt/signing-state`<br>`getAPTRepositorySigningState` | `apt signing-state --repo <id>` | #138 | M2 | RA | Q |
| `GET /repositories/{repositoryId}/conan/references`<br>`listConanReferences` | `conan references --repo <id>` | #131 | M2 | R* | Q |
| `GET /repositories/{repositoryId}/conan/recipe-revisions`<br>`listConanRecipeRevisions` | `conan recipe-revisions --repo <id>` | #131 | M2 | R* | Q |
| `GET /repositories/{repositoryId}/conan/package-revisions`<br>`listConanPackageRevisions` | `conan package-revisions --repo <id>` | #131 | M2 | R* | Q |
| `GET /repositories/{repositoryId}/conan/package-ids`<br>`listConanPackageIds` | `conan package-ids --repo <id>` | #131 | M2 | R* | Q |
| `DELETE /repositories/{repositoryId}/conan/package-revisions/{revision}`<br>`deleteConanPackageRevision` | `conan package-revision delete --repo <id>` | #131 | M2 | W | W |
| `GET /repositories/{repositoryId}/cache/entries`<br>`listProxyCacheEntries` | `cache entries --repo <id>` | #137 | M2 | R* | Q |
| `POST /repositories/{repositoryId}/cache/invalidate`<br>`invalidateProxyCache` | `cache invalidate --repo <id>` | #137 | M2 | RA | W |
| `POST /repositories/{repositoryId}/cache/negative:clear`<br>`clearProxyNegativeCache` | `cache clear-negative --repo <id>` | #137 | M2 | RA | W |
| `POST /repositories/{repositoryId}/cache/refresh`<br>`refreshProxyCache` | `cache refresh --repo <id>` | #137 | M2 | RA | W |
| `GET /repositories/{repositoryId}/proxy/health`<br>`getProxyHealth` | `proxy health --repo <id>` | #137 | M2 | R | Q |
| `POST /repositories/{repositoryId}/conan-publish-sessions`<br>`createConanPublishSession` | 不提供：Conan 会话编排不属于 CLI M1 范围；原生 Conan 发布由 #312 跟踪 | #132 / #312 | N/A | W | N |
| `GET /conan-publish-sessions/{sessionId}`<br>`getConanPublishSession` | 不提供：Conan 会话编排不属于 CLI M1 范围；原生 Conan 发布由 #312 跟踪 | #132 / #312 | N/A | R | N |
| `PUT /conan-publish-sessions/{sessionId}/objects/{objectName}`<br>`uploadConanPublishObject` | 不提供：Conan 会话编排不属于 CLI M1 范围；原生 Conan 发布由 #312 跟踪 | #132 / #312 | N/A | W | N |
| `POST /conan-publish-sessions/{sessionId}:commit`<br>`commitConanPublishSession` | 不提供：Conan 会话编排不属于 CLI M1 范围；原生 Conan 发布由 #312 跟踪 | #132 / #312 | N/A | W | N |
| `GET /publish-sessions/{sessionId}`<br>`getPublishSession` | `publish status <session>` | #132 | M1 implemented | R | U |
| `PUT /publish-sessions/{sessionId}/objects/{objectName}`<br>`uploadPublishObject` | `publish upload <session>` | #132 | M1 implemented | W | U |
| `POST /publish-sessions/{sessionId}:commit`<br>`commitPublishSession` | `publish commit <session>` | #132 | M1 implemented | W | U |

## 原生 bundle 中的附加协议操作

管理操作在 native bundle 中重复出现时沿用上表，不重复实现。以下 43 个附加
操作逐项分类；原生 URL 按各 protocol 的 server 覆盖和真实路由使用，不能一律
拼 `/api/v2`。Nexus 兼容数据路径不是 Nexus 管理 REST API。

| 协议 API / operationId | 计划命令/处理 | 承接 | 阶段 | 权限 | 验收 |
| --- | --- | --- | --- | --- | --- |
| `GET /npm/{repository}/-/package/{package}/dist-tags`<br>`listNPMDistTags` | 由原生 npm 客户端管理 dist-tag；agctl 不另行编排此协议动作 | #308 | N/A | 原生格式授权 | N：npm dist-tag 承接此动作，不另建 agctl 命令 |
| `HEAD /npm/{repository}/-/package/{package}/dist-tags`<br>`headNPMDistTags` | 由原生 npm 客户端管理 dist-tag；agctl 不另行编排此协议动作 | #308 | N/A | 原生格式授权 | N：npm dist-tag 承接此动作，不另建 agctl 命令 |
| `PUT /npm/{repository}/-/package/{package}/dist-tags/{tag}`<br>`setNPMDistTag` | 由原生 npm 客户端管理 dist-tag；agctl 不另行编排此协议动作 | #308 | N/A | 原生格式授权 | N：npm dist-tag 承接此动作，不另建 agctl 命令 |
| `DELETE /npm/{repository}/-/package/{package}/dist-tags/{tag}`<br>`deleteNPMDistTag` | 由原生 npm 客户端管理 dist-tag；agctl 不另行编排此协议动作 | #308 | N/A | 原生格式授权 | N：npm dist-tag 承接此动作，不另建 agctl 命令 |
| `GET /raw/{repository}/{path}`<br>`readRawContent` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `HEAD /raw/{repository}/{path}`<br>`headRawContent` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `PUT /raw/{repository}/{path}`<br>`putRawHostedContent` | `publish raw` | #132 | M1 implemented | W | U：大小/摘要与冲突/回读 |
| `POST /raw/{repository}/{path}`<br>`startRawHostedResumableUpload` | publish raw resumable 适配 | #132 | M2 | W | U：offset/摘要/中断重放 |
| `PATCH /raw/{repository}/{path}`<br>`appendRawHostedResumableUpload` | publish raw resumable 适配 | #132 | M2 | W | U：offset/摘要/中断重放 |
| `DELETE /raw/{repository}/{path}`<br>`deleteRawHostedContent` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `GET /raw/{repository}/`<br>`listRawRootDirectory` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `GET /apt/{repository}/{path}`<br>`getAptAsset` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `HEAD /apt/{repository}/{path}`<br>`headAptAsset` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `POST /v2/{name}/blobs/uploads/`<br>`startOCIUpload` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `GET /v2/{name}/blobs/uploads/{uuid}`<br>`getOCIUploadStatus` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `PATCH /v2/{name}/blobs/uploads/{uuid}`<br>`appendOCIUpload` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `PUT /v2/{name}/blobs/uploads/{uuid}`<br>`completeOCIUpload` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `DELETE /v2/{name}/blobs/uploads/{uuid}`<br>`cancelOCIUpload` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `GET /v2/{name}/blobs/{digest}`<br>`readOCIBlob` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `HEAD /v2/{name}/blobs/{digest}`<br>`headOCIBlob` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `GET /v2/{name}/manifests/{reference}`<br>`readOCIManifest` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `HEAD /v2/{name}/manifests/{reference}`<br>`headOCIManifest` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `PUT /v2/{name}/manifests/{reference}`<br>`putOCIManifest` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `DELETE /v2/{name}/manifests/{reference}`<br>`deleteOCIManifest` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `GET /v2/{name}/tags/list`<br>`listOCITags` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `HEAD /v2/{name}/tags/list`<br>`headOCITags` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `GET /repository/maven/{repository}/{assetPath}`<br>`readMavenAsset` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `PUT /repository/maven/{repository}/{assetPath}`<br>`stageMavenProtocolAsset` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `GET /repository/{repository}/{path}`<br>`readNexusCompatibleRepositoryPath` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `HEAD /repository/{repository}/{path}`<br>`headNexusCompatibleRepositoryPath` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `PUT /repository/{repository}/{path}`<br>`putNexusCompatibleRepositoryPath` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `POST /repository/{repository}/{path}`<br>`postNexusCompatibleRepositoryPath` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `PATCH /repository/{repository}/{path}`<br>`patchNexusCompatibleRepositoryPath` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `DELETE /repository/{repository}/{path}`<br>`deleteNexusCompatibleRepositoryPath` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `POST /repository/{repository}/`<br>`uploadNexusCompatiblePyPIDistribution` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `POST /repository/maven/{repository}/coordinates/{coordinate}:commit`<br>`commitMavenCoordinate` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `GET /go/{repository}/{modulePath}`<br>`readGoModuleRepresentation` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `HEAD /go/{repository}/{modulePath}`<br>`headGoModuleRepresentation` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `PUT /go/{repository}/{modulePath}`<br>`publishGoHostedModule` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `GET /cargo/{repository}/{path}`<br>`getCargoRegistryRepresentation` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `HEAD /cargo/{repository}/{path}`<br>`headCargoRegistryRepresentation` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `PUT /cargo/{repository}/{path}`<br>`putCargoHostedRegistryMutation` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |
| `DELETE /cargo/{repository}/{path}`<br>`yankCargoHostedCrateVersion` | 不另建 CLI 协议客户端；原生客户端/接入配置 | #130 | N/A | 原生格式授权 | N：CLI 不另行注册/发送该协议动作 |

这不妨碍已通过 `/api/v2` 提供的 Maven/OCI/Conan 等管理视图，也不把协议客户端
辅助原先的“视需要”扩展当作首版硬承诺。未来选择新增编排动作时，先改本映射/
能力阶段，并复用现有资源实现。

## 契约外 HTTP、本地入口和没有 API 的能力

下表每行分别说明计划入口/承接/阶段/权限/验收；本地入口“已有”只指 gateway，
不表示 agctl 已实现：

| 能力与实际入口 | 计划 CLI/文档与承接 | 阶段 | 权限/验收 |
| --- | --- | --- | --- |
| GET /livez、GET /readyz | info/status，#125/#135 | M0/M1 | 既有公开健康路由；204/不健康/超时分别验证，不等同全部业务成功 |
| POST /auth/login、POST /auth/change-password、GET /auth/session、POST /auth/logout | auth login/change-password/logout 与身份说明，#129 | M1 | 本人既有认证/会话状态；缺私有输入有界失败，token 不回显；不承诺浏览器 OIDC 登录可无交互 |
| 客户端 profiles/config、commands 自省、version | #125/#129 | M0/M1 | 仅本地客户端文件/输入，不读服务器 .env；优先级/0600/schema/两类命令导航测试 |
| gateway version | #125 文档区分 CLI/远端/服务端二进制版本 | LOCAL 已有 | 本地执行身份；不会将本地版本当远程实例版本 |
| gateway preflight list/run | #137 指向现有入口 | LOCAL 已有 | 主机/服务端本地配置权限；显式检查与脱敏结果，不以 API token 代替主机权限 |
| gateway preflight capacity --input | #137，复用 #202 | LOCAL 已有 | 显式离线库存/容量计划；unique object/峰值/逻辑 quota 区分，unknown/不足阻断，不是实时预留 |
| gateway preflight backup --input | #137，复用 #200 | LOCAL 已有 | 显式备份清单/私有文件读取；完整性/兼容性验证，不能用 readyz 代替 |
| gateway backup export/restore --spec --bundle | #137 指向现有操作；#200/#201 保留剩余范围 | LOCAL 已有；远程 BLOCKED | 额外 PG/S3/文件/Docker 权限及显式隔离目标；摘要/软件/schema/语义读回，不自动覆盖既有目标 |
| gateway evidence collect --gateway-url --output | #137 指向现有采集，#135 远程只读复用 | LOCAL 已有 | 显式目标/输出目录及管理员凭据；脱敏证据/版本身份，非持久服务端 job |
| gateway snapshot-import verify/dry-run/apply/takeover | #137 文档、相关 #257/#259/#202 | LOCAL 已有；在线迁移 BLOCKED | 冻结 Maven bundle/私有 spec/目标 DB-S3 权限；身份/字节/checkpoint/容量，takeover 后不追加，不枚举 Nexus |
| gateway apt-snapshot verify | #138 文档；远程归档/恢复见上表 | LOCAL 已有 | 本地归档读/摘要完整性；输出 signatures=not_checked 不冒充可信签名已通过 |
| gateway theme validate/install/list | #139 区分本地 directory 与远程 managed | LOCAL 已有 | 显式本地目录/文件权限；包校验/ownership；不以远端 admin token 获得本地写入权 |
| 完整系统备份/恢复、主机/部署配置、worker/PG/S3/SMTP relay/auth/CA | #126/#137/#139 明确部署入口；远程需求待独立 API | LOCAL/远程 BLOCKED | 尚无通用远程管理端点；不做隐式 SSH/容器/数据库直连，不从真实 secrets 自动探测 |
| Nexus 源连接/在线全量与持续增量迁移任务 | #269，关联 #199/#202/#204/#205；未来 #137 接受实际 API | BLOCKED | 未实现持久服务端迁移契约；默认源只读/制品与用户权限秘密迁移分离；不能由离线导入命令宣称在线同步完成 |
| 物理挂载告警、集群 sender 就绪/积压与更完整通知 | #255/#256 及 #209–#213/#229/#230；未来 #336 扩展 | BLOCKED | 现有局部 capability/心跳不是全部角色交付证明；待实际 API/schema/部署验收，不伪造字段或命令 |

来源：`cmd/gateway/main.go`、`internal/{backupops,snapshotimport,preflight,evidence,aptpublication,consoletheme}/cli*.go`；
`docs/repository-deletion-contract.zh-CN.md`、`user-governance.zh-CN.md`、
`runtime-log-product-design.zh-CN.md`、`email-notification-delivery.zh-CN.md`、
`maven-snapshot-import.zh-CN.md`。管理 API 的平台权限与本地运维文件/OS 权限是
两条不同授权边界。

## 漂移守卫与完成定义

- “全部操作分类”的要求保留。`make cli-contract-check` 枚举两个 bundle 的操作集
  （方法 + 路径 + operationId），要求本页逐行归类；出现未归类操作即失败，提示
  “新增操作需要显式归类”。改名/删接口留下的旧行、丢掉理由或 `N` 验收代码的
  N/A 行同样失败。管理操作与 native 附加协议操作都在覆盖内；守卫从 bundle
  推导集合，不硬编码忽略清单。
- 映射覆盖与实现覆盖分开：全部行必须分类，只有 implemented 或当前交付里程碑
  的行要求注册命令与真实验收；计划 M2/blocked 不强迫首版实现。行通过在阶段
  列携带 `implemented` 声明实现（例如 `M1 implemented`）；守卫经
  `agctl commands --json` 读取已安装的命令注册表，implemented 行没有注册命令、
  或该命令的元数据没有声明这一行的端点（`METHOD /api/v2/path`）时失败。一个
  命令可以承接多行（例如 `artifact search --global` 与 `--repo`），前提是每行
  的端点都按命令声明的实际请求核对。
- 一个资源命令/别名使用同一注册实现；CLI tree 的 CI/管理 tags 不复制操作；
  JSON 输出 schema、权限、mutation、分页、幂等、dry-run、wait/cancel 必须来自
  同一元数据来源。
- 增补验收：注入一个新 operationId 未分类失败；改名/删接口仍引用失败；planned
  M2 不误判首版缺实现；implemented 行没有注册命令失败，命令未声明该行端点也失败；
  只为已实现命令生成 #140 可执行菜谱。
- 本页在上方记录契约基线 SHA 与核对日期；计数由守卫重新推导，不把旧计数固定
  在文档里。
- 双语注册：`docs/cli-command-map.md` 与 `docs/cli-command-map.zh-CN.md` 双向
  互链并注册进 `docs/site-map.json`；`make docs-check` 覆盖该对。

