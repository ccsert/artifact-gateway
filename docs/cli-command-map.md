# agctl command map

[简体中文](cli-command-map.zh-CN.md) · [Documentation index](README.md) · [Project README](../README.md)

`agctl` exposes one command surface for two navigation views: CI (onboarding,
identity, publication, retrieval and verification, audit) and platform
management (identity and authority, repositories and Groups, data governance,
configuration policy, logs/audit/diagnostics/capacity, alerts and
notifications, asynchronous operations and local entry points). Resource
commands, HTTP/JSON behavior, credentials, and permissions exist once; agents
use the same introspection for both views.

This page classifies the full contract; it does not describe the installed
binary. Apart from the #125 skeleton (`version`, `info`, `commands`) and the
rows whose phase says `implemented` (the #130 repository and Group commands,
the #131 artifact and format views, the #134 grant, role, template, access
check, and anonymous-switch commands, and the read-only #135 audit, status,
diagnostics, runtime-node and runtime-log, overview, and lifecycle-job
commands, each
accepted against a real Gateway by `make cli-e2e`), every row is a plan. A
row may claim implemented/verified only once its command is
registered, its documentation and tests exist, and it passed acceptance
against a real Gateway.

Baseline: `api/openapi/management-runtime-v1.json` at commit
`a504ba48cd93371badcb1f2781b120b359e57b69` (127 paths / 177 operations), plus
the protocol endpoints of `api/openapi/native-hosted-v1.json` the CLI needs to
reach. Counts are re-derived by the drift guard below, not frozen here; the
map is updated first whenever the contract changes. Last reviewed: 2026-10-11
(issue #126 supplement).

## Phase legend

- M0 — foundation: registration, configuration, secure output, introspection,
  testing, distribution, and this map (#125–#128).
- M1 — first-release plan (#129–#136 plus #335); the first release is not the
  whole platform-management surface.
- M2 — later plan (#137–#139 plus #336, and the advanced rows marked in
  #131/#134).
- N/A — explicitly not exposed by the CLI (for example the compatible
  whole-table grant replacement PUT); the API itself stays.
- LOCAL — already present in the local `gateway` binary on the host; it does
  not mean `agctl` implements a matching remote command.
- BLOCKED — no management API or backend capability has shipped; the row
  records the dependency, and no invented HTTP endpoint may claim coverage.

## Permission legend (the server is the final authority)

The server remains the final authority; the default identity and account state
must be rejected truthfully, and the CLI adds no authorization:

- PUBLIC: public read; I: the current authenticated actor; I/A: self lookup,
  where impersonating another subject needs platform admin.
- A: platform admin; R/W/RA/RI: the repository's
  repositories:read/write/admin/intelligence (the server keeps its existing
  platform-admin behavior).
- R*: the read authorization of the repository/Group browse helper plus an
  explicitly configured anonymous read branch; it must not be anonymous by
  default.
- FR/FA: server-side filter over readable/manageable repositories; ST:
  repository tasks need RA, platform tasks need A, and lists filter by
  manageable scope.
- G: the existing Group membership management gate (platform A, or admin of
  every bound member; unknown or unbound members need A); RA×2: source and
  target authorizations are governed separately.

Basis: `internal/app/hosted_repository_api.go`,
`repository_access_api.go`, `hosted_group_api.go`, `scheduled_tasks_api.go`
and the domain handlers; [accepted ADR 0006](adr/0006-account-levels-and-repository-grants.md).

## Acceptance code legend

Every acceptance code must be verified with synthetic input for the mapped
operationId, known literal expectations, and a real isolated Gateway E2E — not
just an HTTP 200:

- Q: exact schema/fields/visibility, real pagination range and cursor, empty
  results and permission denial, honoring the #125 partial-result/unknown
  contract.
- W: read state/audit back through existing read entry points after a change;
  no permission, wrong input, dangerous confirmations, and client-plan dry-run
  produce no business change.
- C: W plus real If-Match/version conflicts; a 412 must not overwrite the new
  value.
- S: one-time secrets delivered through a secure file; a missing destination
  is refused before the request; default output and logs carry no secret; a
  failed write does not re-issue.
- G: single-row authorization concurrency without lost updates, template
  apply effects and CAS, effective-access and anonymous-combination gates
  (#134).
- J: accepted and terminal states are separate, bounded wait on the original
  ID, failure/timeout/retry and real cancel support, declared idempotency/CAS
  (#125/#137).
- U: publish sessions and per-object size/digest, staged idempotent read-back,
  unknown commit and partial failure (#132).
- L: local/session logs, cursors, restart/503 and bounded NDJSON, redaction
  and administrator gates (#135).
- D: uses a real server preview or an explicit client plan; configuration,
  objects, and notification queues stay unchanged; its output must not claim
  full execution acceptance.
- N: this direct command is not offered; the guard keeps the explicit reason
  on the row and the CLI does not reach the same compatible PUT through
  another path.

## Management contract operation map

All 177 rows below cover the complete operation set of the baseline
`api/openapi/management-runtime-v1.json`; the drift guard re-derives the set
from the file rather than trusting this count. Paths are relative to `/api/v2`,
the contract's server base. Planned commands omit the leading `agctl`.

| API (all under /api/v2) / operationId | Planned command (agctl omitted) | Owning issue | Phase | Permission | Acceptance |
| --- | --- | --- | --- | --- | --- |
| `GET /repository-quota-alert-rules/{ruleId}/events`<br>`listRepositoryQuotaAlertEvents` | `quota-alert events <rule>` | #336 | M2 | A | Q |
| `GET /repository-quota-alert-rules/{ruleId}`<br>`getRepositoryQuotaAlertRule` | `quota-alert show <rule>` | #336 | M2 | A | Q |
| `PUT /repository-quota-alert-rules/{ruleId}`<br>`updateRepositoryQuotaAlertRule` | `quota-alert update <rule>` | #336 | M2 | A | C |
| `DELETE /repository-quota-alert-rules/{ruleId}`<br>`deleteRepositoryQuotaAlertRule` | `quota-alert delete <rule>` | #336 | M2 | A | C |
| `POST /repository-quota-alert-rules`<br>`createRepositoryQuotaAlertRule` | `quota-alert create` | #336 | M2 | A | W |
| `GET /repository-quota-alert-rules`<br>`listRepositoryQuotaAlertRules` | `quota-alert list` | #336 | M2 | A | Q |
| `GET /email-notifications`<br>`getEmailNotificationCapability` | `email capability` | #336 | M2 | A | Q |
| `POST /email-notifications:test`<br>`testEmailNotification` | `email test` | #336 | M2 | A | J |
| `GET /email-targets`<br>`listEmailTargets` | `email target list` | #336 | M2 | A | Q |
| `POST /email-targets`<br>`createEmailTarget` | `email target create` | #336 | M2 | A | W |
| `GET /email-targets/{targetId}`<br>`getEmailTarget` | `email target show <id>` | #336 | M2 | A | Q |
| `PUT /email-targets/{targetId}`<br>`updateEmailTarget` | `email target update <id>` | #336 | M2 | A | C |
| `GET /email-deliveries`<br>`listEmailDeliveries` | `email delivery list` | #336 | M2 | A | Q |
| `GET /email-deliveries/{deliveryId}`<br>`getEmailDelivery` | `email delivery show <id>` | #336 | M2 | A | Q |
| `POST /email-deliveries/{deliveryId}:replay`<br>`replayEmailDelivery` | `email delivery replay <id>` | #336 | M2 | A | J |
| `POST /email-notifications:preview`<br>`previewEmailNotification` | `email preview` | #336 | M2 | A | D |
| `GET /formats`<br>`listFormatProfiles` | `formats list` (info reuses the same read) | #125 | M0 | I | Q |
| `GET /identity`<br>`getCurrentIdentity` | `auth whoami` (info reuses the same read) | #129 | M1 | I | Q |
| `GET /artifact-search`<br>`searchArtifacts` | `artifact search --global` | #131 | M1 implemented | FR | Q |
| `GET /anonymous-access-policy`<br>`getAnonymousAccessPolicy` | `access anonymous show` | #134 | M1 implemented | A | Q |
| `PUT /anonymous-access-policy`<br>`replaceAnonymousAccessPolicy` | `access anonymous set` | #134 | M1 implemented | A | C |
| `GET /site-settings`<br>`getSiteSettings` | `site-settings show` (theme list reuses availableThemes) | #139 | M2 | PUBLIC | Q |
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
| `GET /diagnostics`<br>`getDiagnostics` | `diagnostics` (status reuses the same read) | #135 | M1 implemented | A | Q |
| `GET /runtime/logs`<br>`listRuntimeLogs` | `runtime logs` | #135 | M1 implemented | A | L |
| `GET /api-keys`<br>`listApiKeys` | `api-key list` | #133 | M1 | A | Q |
| `POST /api-keys`<br>`createApiKey` | `api-key create` | #133 | M1 | A | S |
| `DELETE /api-keys/{apiKeyId}`<br>`revokeApiKey` | `api-key delete <id>` | #133 | M1 | A | W |
| `GET /service-accounts`<br>`listServiceAccounts` | `service-account list` (show derives from the list by ID) | #133 | M1 | A | Q |
| `POST /service-accounts`<br>`createServiceAccount` | `service-account create` | #133 | M1 | A | W |
| `PUT /service-accounts/{serviceAccountId}`<br>`updateServiceAccount` | `service-account update <id>` | #133 | M1 | A | C |
| `GET /service-accounts/{serviceAccountId}/credentials`<br>`listServiceAccountCredentials` | `credential list --service-account <id>` | #133 | M1 | A | Q |
| `POST /service-accounts/{serviceAccountId}/credentials`<br>`createServiceAccountCredential` | `credential issue --service-account <id>` | #133 | M1 | A | S |
| `DELETE /service-accounts/{serviceAccountId}/credentials/{credentialId}`<br>`revokeServiceAccountCredential` | `credential revoke <id> --service-account <id>` | #133 | M1 | A | W |
| `GET /users`<br>`listUsers` | `user list` | #335 | M1 | A | Q |
| `POST /users`<br>`createUser` | `user create` | #335 | M1 | A | W |
| `GET /users/{userId}`<br>`getUser` | `user show <id>` | #335 | M1 | A | Q |
| `PATCH /users/{userId}`<br>`updateUser` | `user update <id>` | #335 | M1 | A | W |
| `DELETE /users/{userId}`<br>`deleteUser` | `user delete <id>` | #335 | M1 | A | W |
| `POST /users/{userId}/password`<br>`resetUserPassword` | `user reset-password <id>` | #335 | M1 | A | C |
| `POST /users/{userId}/sessions:revoke`<br>`revokeUserSessions` | `user sessions revoke <id> --all` | #335 | M1 | A | C |
| `GET /users/{userId}/sessions`<br>`listUserSessions` | `user sessions list <id>` | #335 | M1 | A | Q |
| `DELETE /users/{userId}/sessions/{sessionId}`<br>`revokeUserSession` | `user sessions revoke <id> --session <sid>` | #335 | M1 | A | W |
| `GET /users/{userId}/identities`<br>`listUserIdentities` | `user identities list <id>` | #335 | M1 | A | Q |
| `POST /users/{userId}/identities`<br>`createUserIdentity` | `user identities link <id>` | #335 | M1 | A | W |
| `DELETE /users/{userId}/identities/{identityId}`<br>`deleteUserIdentity` | `user identities unlink <id> --identity <iid>` | #335 | M1 | A | W |
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
| `DELETE /repositories/{repositoryId}`<br>`deleteRepository` | `repo delete <id>` (asynchronous deleting → deleted) | #130 | M1 implemented | RA | J |
| `PATCH /repositories/{repositoryId}`<br>`updateRepository` | `repo update <id>` | #130 | M1 implemented | RA | C |
| `POST /repositories/{repositoryId}/egress-proxy:test`<br>`testEgressProxy` | `egress-proxy test --repo <id>` | #139 | M2 | RA | W |
| `GET /repositories/{repositoryId}/grants`<br>`listGrants` | `grant list --repo <id>` | #134 | M1 implemented | RA | Q |
| `PUT /repositories/{repositoryId}/grants`<br>`replaceGrants` | not exposed as a whole-table PUT; use single-row grant writes or `template apply` | #134 | N/A | RA | N |
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
| `GET /repositories/{repositoryId}/lifecycle-jobs`<br>`listRepositoryLifecycleJobs` | `lifecycle-job list --repo <id>` (show/wait filter the same response by ID) | #135 | M1 implemented | RA | Q |
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
| `POST /repositories/{repositoryId}/publish-sessions`<br>`createPublishSession` | `publish create --repo <id>` | #132 | M1 | W | U |
| `GET /repositories/{repositoryId}/artifacts`<br>`listArtifacts` | `artifact list --repo <id>` | #131 | M1 implemented | R* | Q |
| `GET /repositories/{repositoryId}/artifact-usage`<br>`listRepositoryArtifactUsage` | `artifact usage --repo <id>` | #131 | M1 implemented | R* | Q |
| `GET /repositories/{repositoryId}/artifact-search`<br>`searchRepositoryArtifacts` | `artifact search --repo <id>` | #131 | M1 implemented | R* | Q |
| `GET /repositories/{repositoryId}/browse`<br>`browseRepository` | `artifact browse --repo <id>` (repo browse alias) | #131 | M1 implemented | R* | Q |
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
| `GET /repositories/{repositoryId}/apt/lifecycle`<br>`getAPTLifecycleState` | `apt lifecycle show` (snapshot list reuses snapshots) | #138 | M2 | RA | Q |
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
| `GET /publish-sessions/{sessionId}`<br>`getPublishSession` | `publish status <session>` | #132 | M1 | R | U |
| `PUT /publish-sessions/{sessionId}/objects/{objectName}`<br>`uploadPublishObject` | `publish upload <session>` | #132 | M1 | W | U |
| `POST /publish-sessions/{sessionId}:commit`<br>`commitPublishSession` | `publish commit <session>` | #132 | M1 | W | U |

## Additional protocol operations in the native bundle

Management operations repeat in the native bundle and reuse the table above;
they are not implemented twice. The 39 additional operations below are
classified one by one. Native URLs follow each protocol's server override and
real routes and must not be joined to `/api/v2` blindly. Nexus-compatible data
paths are not the Nexus management REST API.

| Protocol API / operationId | Planned command / handling | Owning issue | Phase | Permission | Acceptance |
| --- | --- | --- | --- | --- | --- |
| `GET /raw/{repository}/{path}`<br>`readRawContent` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `HEAD /raw/{repository}/{path}`<br>`headRawContent` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `PUT /raw/{repository}/{path}`<br>`putRawHostedContent` | publish raw upload | #132 | M1 | W | U: size/digest plus conflict and read-back |
| `POST /raw/{repository}/{path}`<br>`startRawHostedResumableUpload` | `publish raw resumable` adapter | #132 | M2 | W | U: offset/digest and interrupted replay |
| `PATCH /raw/{repository}/{path}`<br>`appendRawHostedResumableUpload` | `publish raw resumable` adapter | #132 | M2 | W | U: offset/digest and interrupted replay |
| `DELETE /raw/{repository}/{path}`<br>`deleteRawHostedContent` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `GET /raw/{repository}/`<br>`listRawRootDirectory` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `GET /apt/{repository}/{path}`<br>`getAptAsset` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `HEAD /apt/{repository}/{path}`<br>`headAptAsset` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `POST /v2/{name}/blobs/uploads/`<br>`startOCIUpload` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `GET /v2/{name}/blobs/uploads/{uuid}`<br>`getOCIUploadStatus` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `PATCH /v2/{name}/blobs/uploads/{uuid}`<br>`appendOCIUpload` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `PUT /v2/{name}/blobs/uploads/{uuid}`<br>`completeOCIUpload` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `DELETE /v2/{name}/blobs/uploads/{uuid}`<br>`cancelOCIUpload` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `GET /v2/{name}/blobs/{digest}`<br>`readOCIBlob` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `HEAD /v2/{name}/blobs/{digest}`<br>`headOCIBlob` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `GET /v2/{name}/manifests/{reference}`<br>`readOCIManifest` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `HEAD /v2/{name}/manifests/{reference}`<br>`headOCIManifest` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `PUT /v2/{name}/manifests/{reference}`<br>`putOCIManifest` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `DELETE /v2/{name}/manifests/{reference}`<br>`deleteOCIManifest` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `GET /v2/{name}/tags/list`<br>`listOCITags` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `HEAD /v2/{name}/tags/list`<br>`headOCITags` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `GET /repository/maven/{repository}/{assetPath}`<br>`readMavenAsset` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `PUT /repository/maven/{repository}/{assetPath}`<br>`stageMavenProtocolAsset` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `GET /repository/{repository}/{path}`<br>`readNexusCompatibleRepositoryPath` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `HEAD /repository/{repository}/{path}`<br>`headNexusCompatibleRepositoryPath` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `PUT /repository/{repository}/{path}`<br>`putNexusCompatibleRepositoryPath` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `POST /repository/{repository}/{path}`<br>`postNexusCompatibleRepositoryPath` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `PATCH /repository/{repository}/{path}`<br>`patchNexusCompatibleRepositoryPath` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `DELETE /repository/{repository}/{path}`<br>`deleteNexusCompatibleRepositoryPath` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `POST /repository/{repository}/`<br>`uploadNexusCompatiblePyPIDistribution` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `POST /repository/maven/{repository}/coordinates/{coordinate}:commit`<br>`commitMavenCoordinate` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `GET /go/{repository}/{modulePath}`<br>`readGoModuleRepresentation` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `HEAD /go/{repository}/{modulePath}`<br>`headGoModuleRepresentation` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `PUT /go/{repository}/{modulePath}`<br>`publishGoHostedModule` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `GET /cargo/{repository}/{path}`<br>`getCargoRegistryRepresentation` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `HEAD /cargo/{repository}/{path}`<br>`headCargoRegistryRepresentation` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `PUT /cargo/{repository}/{path}`<br>`putCargoHostedRegistryMutation` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |
| `DELETE /cargo/{repository}/{path}`<br>`yankCargoHostedCrateVersion` | no separate CLI protocol client; native client and onboarding configuration | #130 | N/A | native-format authorization | N: the CLI neither registers nor sends this protocol action |

This does not block the Maven/OCI/Conan management views already served under
`/api/v2`, and it does not turn the earlier "as needed" protocol-client helpers
into a first-release commitment. Choosing to add an orchestration action later
starts by updating this map and the phase, reusing the existing resource
implementation.

## Contract-external HTTP, local entry points, and capabilities without an API

Each row states the planned entry point, owning issue, phase, permission, and
acceptance. "Already exists" for a local entry point means the gateway binary
only, never that agctl implements it:

| Capability and actual entry point | Planned CLI/documentation and owning issue | Phase | Permission/acceptance |
| --- | --- | --- | --- |
| GET /livez, GET /readyz | info/status, #125/#135 | M0/M1 | existing public health routes; verify 204, unhealthy, and timeout separately; not equivalent to full business success |
| POST /auth/login, POST /auth/change-password, GET /auth/session, POST /auth/logout | auth login/change-password/logout plus the identity description, #129 | M1 | the actor's existing authentication/session state; missing private input fails bounded; the token is never echoed; non-interactive browser OIDC login is not promised |
| client profiles/config, commands introspection, version | #125/#129 | M0/M1 | local client files and input only, never the server .env; precedence/0600/schema and both navigation views are tested |
| gateway version | #125 documents the CLI/remote/server binary version distinction | LOCAL, already exists | local execution identity; the local version is never reported as the remote instance version |
| gateway preflight list/run | #137 points at the existing entry point | LOCAL, already exists | host/server local configuration privileges; explicit checks and redacted results; an API token does not replace host privileges |
| gateway preflight capacity --input | #137, reusing #202 | LOCAL, already exists | explicit offline inventory/capacity plan; unique objects, peak, and logical quota are distinguished, unknown/insufficient blocks; not a live reservation |
| gateway preflight backup --input | #137, reusing #200 | LOCAL, already exists | explicit backup manifest and private file reads; integrity/compatibility verification; readyz cannot substitute |
| gateway backup export/restore --spec --bundle | #137 points at the existing operations; #200/#201 keep the remaining scope | LOCAL, already exists; remote BLOCKED | additional PG/S3/file/Docker privileges and explicit isolated targets; digest/software/schema/semantic read-back; never overwrites an existing target implicitly |
| gateway evidence collect --gateway-url --output | #137 points at the existing collection; #135 reuses it read-only for remote | LOCAL, already exists | explicit target and output directory plus administrator credentials; redacted evidence and version identity; not a durable server-side job |
| gateway snapshot-import verify/dry-run/apply/takeover | #137 documentation, related #257/#259/#202 | LOCAL, already exists; online migration BLOCKED | frozen Maven bundle / private spec / target DB-S3 privileges; identity/bytes/checkpoint/capacity; no appends after takeover, no Nexus enumeration |
| gateway apt-snapshot verify | #138 documentation; remote archive/restore see the table above | LOCAL, already exists | local archive read and digest integrity; the output signatures=not_checked must not pretend trusted signature verification passed |
| gateway theme validate/install/list | #139 distinguishes the local directory from remote managed | LOCAL, already exists | explicit local directory/file privileges; package validation and ownership; a remote admin token does not buy local write access |
| complete system backup/restore, host/deployment configuration, worker/PG/S3/SMTP relay/auth/CA | #126/#137/#139 define the deployment entry points; remote needs a dedicated API | LOCAL/remote BLOCKED | no generic remote management endpoint yet; no implicit SSH/container/database access; no automatic probing from real secrets |
| Nexus source connection, online full and continuous incremental migration jobs | #269, related to #199/#202/#204/#205; a future #137 accepts the real API | BLOCKED | no durable server-side migration contract has shipped; source read-only and artifact/principal permission migration are separated by default; offline import commands cannot claim online synchronization completed |
| physical mount alerts, cluster sender readiness/backlog, and more complete notifications | #255/#256 and #209–#213/#229/#230; a future #336 extends it | BLOCKED | the existing partial capability/heartbeat is not proof that every role shipped; wait for real API/schema/deployment acceptance; do not fabricate fields or commands |

Sources: `cmd/gateway/main.go`; `internal/{backupops,snapshotimport,preflight,evidence,aptpublication,consoletheme}/cli*.go`;
`docs/repository-deletion-contract.md`, `user-governance.md`,
`runtime-log-product-design.md`, `email-notification-delivery.md`,
`maven-snapshot-import.md`. Platform authority on the management API and local
operator file/OS privileges are two different authorization boundaries.

## Drift guard and definition of done

- The "classify every operation" requirement stays. `make cli-contract-check`
  enumerates the operation set of both bundles (method + path + operationId)
  and requires every operation to be classified on this page; an unclassified
  contract operation fails with "new operation needs explicit classification".
  Renames and removals that leave stale rows fail too, and so does an N/A row
  that drops its reason or its `N` acceptance code. Management operations and
  the native protocol extras are both in scope; the sets are derived from the
  bundles, never a hardcoded ignore list.
- Map coverage and implementation coverage are separate checks: every row
  must be classified, but only implemented rows or rows in the currently
  delivered milestone require command registration and real acceptance.
  Planned M2 or blocked rows do not force first-release implementation. A row
  claims implementation by carrying `implemented` in its Phase cell (for
  example `M1 implemented`); the guard reads the installed command registry
  through `agctl commands --json` and fails when an implemented row has no
  registered command, or when the command does not declare the row's
  endpoint (`METHOD /api/v2/path`) in its metadata. One command may serve
  several rows, such as `artifact search --global` and `--repo`, only because
  each row's endpoint is checked against what the command declares it sends.
- One resource command/alias uses one registration implementation; the CLI
  tree's CI/platform tags do not duplicate operations, and JSON output schema,
  permission, mutation, pagination, idempotency, dry-run, and wait/cancel
  metadata all come from one metadata source.
- Supplementary acceptance: injecting a new unclassified operationId fails;
  a rename or removal that is still referenced fails; planned M2 rows are not
  misjudged as a missing first release; an implemented row without a
  registered command fails, and so does one whose endpoint the command does
  not declare;
  and #140 runnable recipes are generated only for implemented commands.
- The map records the contract baseline SHA and review date above; the guard
  re-derives the operation counts and the page never pins the old counts as
  fact.
- Bilingual registration: `docs/cli-command-map.md` and
  `docs/cli-command-map.zh-CN.md` link both ways and appear in
  `docs/site-map.json`; `make docs-check` covers the pair.
