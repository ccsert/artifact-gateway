/**
 * Human-readable labels for the audit operation codes the gateway writes.
 *
 * The audit record keeps the machine code (`repository.grants.upsert`), which
 * is what the filter and the CSV export use; the table shows this localized
 * label instead and keeps the raw code in the cell's tooltip. A code this map
 * does not know — one written by a newer backend — degrades to the raw code
 * instead of a blank or a wrong guess, so the map can lag the backend without
 * hiding information.
 *
 * The codes have three sources, and the groups below follow them:
 *
 *  - `Operation:` literals at the audit call sites in `internal/app`,
 *    `internal/aptpublication`, and the handlers' audit helper functions;
 *  - the HTTP method, which artifact traffic records as its operation;
 *  - the authorization layer's operation enum, which denial audits stringify.
 *
 * A few labels are marked as historical: current code writes no audit with
 * them, but the lifecycle job runtimes and earlier revisions used the same
 * words, so stored rows may carry them. They stay so old records keep reading
 * as words rather than as codes. Keep the map in sync when a backend change
 * adds an operation.
 */

/** Bilingual copy callback from `usePreferences().text`. */
type Localize = (chinese: string, english: string) => string;

const AUDIT_OPERATION_LABELS: Record<string, { zh: string; en: string }> = {
  // Artifact traffic, derived from the HTTP method or the protocol handler.
  get: { zh: "读取（GET）", en: "Read (GET)" },
  head: { zh: "探测（HEAD）", en: "Probe (HEAD)" },
  put: { zh: "上传（PUT）", en: "Upload (PUT)" },
  post: { zh: "提交（POST）", en: "Submit (POST)" },
  delete: { zh: "删除（DELETE）", en: "Delete (DELETE)" },
  patch: { zh: "修改（PATCH）", en: "Modify (PATCH)" },
  commit: { zh: "提交部署", en: "Commit deployment" },
  // Repository operations recorded for denied management requests. The values
  // come from the authorization layer's operation enum, so all four can appear.
  read: { zh: "读取", en: "Read" },
  write: { zh: "写入", en: "Write" },
  admin: { zh: "仓库管理", en: "Repository admin" },
  intelligence: { zh: "制品情报", en: "Artifact intelligence" },
  // Distribution, recorded as the operation itself or with a refusal suffix.
  promote: { zh: "晋升制品", en: "Promote artifact" },
  "promote.quarantine": {
    zh: "晋升被隔离拦截",
    en: "Promotion blocked by quarantine",
  },
  "promote.security_policy": {
    zh: "晋升被安全策略拦截",
    en: "Promotion blocked by security policy",
  },
  replicate: { zh: "复制制品", en: "Replicate artifact" },
  "replicate.quarantine": {
    zh: "复制被隔离拦截",
    en: "Replication blocked by quarantine",
  },
  // Lifecycle and reconciliation.
  "lifecycle.run_now": { zh: "立即执行生命周期任务", en: "Run lifecycle job" },
  "lifecycle.retry": { zh: "重试生命周期任务", en: "Retry lifecycle job" },
  "lifecycle.cancel": { zh: "取消生命周期任务", en: "Cancel lifecycle job" },
  "lifecycle.intelligence_reconcile": {
    zh: "对账制品情报",
    en: "Reconcile artifact intelligence",
  },
  // Historical, kept for rows written by earlier revisions or named after a
  // lifecycle job kind: the current code records no audit with these.
  grant: { zh: "授权", en: "Grant" },
  promotion: { zh: "晋升任务", en: "Promotion job" },
  scan: { zh: "扫描任务", en: "Scan job" },
  lifecycle: { zh: "生命周期任务", en: "Lifecycle job" },
  "intelligence-copy": { zh: "复制制品情报", en: "Copy artifact intelligence" },
  publish: { zh: "发布", en: "Publish" },
  browse: { zh: "浏览", en: "Browse" },
  restore: { zh: "恢复", en: "Restore" },
  retain: { zh: "保留", en: "Retain" },
  reclaim: { zh: "回收", en: "Reclaim" },
  // Repository grants and authorization.
  "repository.grants.replace": {
    zh: "替换仓库授权",
    en: "Replace repository grants",
  },
  "repository.grants.apply_template": {
    zh: "应用授权模板",
    en: "Apply authorization template",
  },
  "repository.grants.upsert": {
    zh: "写入仓库授权",
    en: "Upsert repository grant",
  },
  "repository.grants.delete": {
    zh: "删除仓库授权",
    en: "Delete repository grant",
  },
  "authorization_role.create": { zh: "创建自定义角色", en: "Create role" },
  "authorization_role.update": { zh: "更新自定义角色", en: "Update role" },
  "authorization_role.delete": { zh: "删除自定义角色", en: "Delete role" },
  "authorization_template.create": {
    zh: "创建授权模板",
    en: "Create authorization template",
  },
  "authorization_template.update": {
    zh: "更新授权模板",
    en: "Update authorization template",
  },
  "authorization_template.delete": {
    zh: "删除授权模板",
    en: "Delete authorization template",
  },
  // Artifacts.
  "artifact.tombstone": { zh: "墓碑化制品", en: "Tombstone artifact" },
  "artifact.quarantine": { zh: "隔离制品", en: "Quarantine artifact" },
  "artifact.release": { zh: "解除制品隔离", en: "Release artifact" },
  "artifact.intelligence.replace": {
    zh: "替换制品情报",
    en: "Replace artifact intelligence",
  },
  "artifact.scan.enqueue": { zh: "排队制品扫描", en: "Enqueue artifact scan" },
  "artifact.scan.auto_enqueue": {
    zh: "自动排队制品扫描",
    en: "Auto-enqueue artifact scan",
  },
  "artifact.scan.reconcile": { zh: "对账制品扫描", en: "Reconcile scan" },
  "conan.package_revision.tombstone": {
    zh: "墓碑化 Conan 包修订",
    en: "Tombstone Conan package revision",
  },
  // APT publication.
  "apt.publication_session.create": {
    zh: "创建 APT 发布会话",
    en: "Create APT publication session",
  },
  "apt.publication_package.stage": {
    zh: "暂存 APT 发布包",
    en: "Stage APT publication package",
  },
  "apt.repository_snapshot.publish": {
    zh: "发布 APT 快照",
    en: "Publish APT snapshot",
  },
  "apt.repository_snapshot.export": {
    zh: "导出 APT 快照",
    en: "Export APT snapshot",
  },
  "apt.repository_snapshot.restore": {
    zh: "还原 APT 快照",
    en: "Restore APT snapshot",
  },
  "apt.snapshot.prune": { zh: "清理 APT 快照", en: "Prune APT snapshots" },
  // Package-level APT operations on a published snapshot.
  "apt.package.promote": { zh: "晋升 APT 包", en: "Promote APT package" },
  "apt.package.replicate": { zh: "复制 APT 包", en: "Replicate APT package" },
  "apt.package.delete": { zh: "删除 APT 包", en: "Delete APT package" },
  "apt.package.restore": { zh: "恢复 APT 包", en: "Restore APT package" },
  "apt.package.retention": {
    zh: "按保留策略清理 APT 包",
    en: "Retain APT package",
  },
  // Authentication and users.
  "authentication.oidc.configure": { zh: "配置 OIDC", en: "Configure OIDC" },
  "authentication.oidc.test": { zh: "测试 OIDC 连接", en: "Test OIDC" },
  "user.login": { zh: "用户登录", en: "User sign-in" },
  "user.password.change": { zh: "修改密码", en: "Change password" },
  "user.password.reset": { zh: "重置密码", en: "Reset password" },
  "user.create": { zh: "创建用户", en: "Create user" },
  "user.update": { zh: "更新用户", en: "Update user" },
  "user.delete": { zh: "删除用户", en: "Delete user" },
  "user.sessions.revoke": { zh: "吊销全部会话", en: "Revoke all sessions" },
  "user.session.list": { zh: "查看用户会话", en: "List user sessions" },
  "user.session.revoke": { zh: "吊销会话", en: "Revoke session" },
  "user.identity.link": { zh: "绑定外部身份", en: "Link identity" },
  "user.identity.unlink": { zh: "解绑外部身份", en: "Unlink identity" },
  // Service accounts.
  "service_account.create": {
    zh: "创建服务账号",
    en: "Create service account",
  },
  "service_account.update": {
    zh: "更新服务账号",
    en: "Update service account",
  },
  "service_account.credential.create": {
    zh: "签发服务账号凭据",
    en: "Create service account credential",
  },
  "service_account.credential.revoke": {
    zh: "吊销服务账号凭据",
    en: "Revoke service account credential",
  },
  // Policies and settings.
  "capacity.configure": { zh: "配置仓库容量", en: "Configure capacity" },
  "quarantine_read_policy.replace": {
    zh: "替换隔离读取策略",
    en: "Replace quarantine read policy",
  },
  "security_policy.replace": {
    zh: "替换安全准入策略",
    en: "Replace security policy",
  },
  "security_policy.evaluate": {
    zh: "评估安全准入策略",
    en: "Evaluate security policy",
  },
  "site-settings.replace": { zh: "更新站点设置", en: "Update site settings" },
  "console-theme.install": { zh: "安装控制台主题", en: "Install theme" },
  "console-theme.replace": { zh: "替换控制台主题", en: "Replace theme" },
  "console-theme.delete": { zh: "删除控制台主题", en: "Delete theme" },
  // Groups, webhooks, and scheduled tasks.
  "group.membership_denied": {
    zh: "分组成员校验拒绝",
    en: "Group membership denied",
  },
  "webhook.subscription.create": {
    zh: "创建 Webhook 订阅",
    en: "Create webhook subscription",
  },
  "webhook.subscription.update": {
    zh: "更新 Webhook 订阅",
    en: "Update webhook subscription",
  },
  "webhook.delivery.replay": {
    zh: "重放 Webhook 投递",
    en: "Replay webhook delivery",
  },
  "scheduled_task.create": {
    zh: "创建计划任务",
    en: "Create scheduled task",
  },
  "scheduled_task.update": {
    zh: "更新计划任务",
    en: "Update scheduled task",
  },
  "scheduled_task.delete": {
    zh: "删除计划任务",
    en: "Delete scheduled task",
  },
  "scheduled_task.run": { zh: "执行计划任务", en: "Run scheduled task" },
  "scheduled_task.run_failed": {
    zh: "计划任务执行失败",
    en: "Scheduled task run failed",
  },
};

/**
 * The localized label for an audit operation code, or the raw code when the
 * map does not know it.
 */
export function auditOperationLabel(value: string, text: Localize): string {
  const label = AUDIT_OPERATION_LABELS[value];
  return label ? text(label.zh, label.en) : value;
}
