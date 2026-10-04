import type { RepositoryQuotaAlertPolicy } from "../../client";

export type Translate = (zh: string, en: string) => string;
export function isQuotaAuthError(error: unknown) {
  return (
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    [
      "password_change_required",
      "permission_denied",
      "forbidden",
      "access_denied",
      "unauthenticated",
    ].includes(String(error.code))
  );
}
const labels: Record<string, [string, string]> = {
  normal: ["未触发", "Not firing"],
  warning: ["警告", "Warning"],
  critical: ["严重", "Critical"],
  resolved: ["恢复", "Recovered"],
  available: ["样本可用", "Sample available"],
  unknown: ["容量未知", "Capacity unknown"],
  stale: ["样本陈旧", "Sample stale"],
  not_configured: ["未配置仓库配额", "Repository quota not configured"],
  configuration_changed: ["配置已变更", "Configuration changed"],
  disabled: ["已停用", "Disabled"],
  deleted: ["已删除，保留历史", "Deleted; history retained"],
  repository_deleted: ["仓库已删除", "Repository deleted"],
  repository_inactive: ["仓库非活动状态", "Repository inactive"],
  pending: ["等待交付", "Pending delivery"],
  delivering: ["正在交付", "Delivering"],
  retrying: ["等待重试", "Retry scheduled"],
  accepted: ["SMTP 服务器已接受", "Accepted by SMTP server"],
  dead: ["交付已停止", "Delivery stopped"],
  queued: ["已进入交付队列", "Queued for delivery"],
  queue_full: ["交付队列已满", "Delivery queue full"],
  email_disabled: ["邮件通道未开启", "Email channel disabled"],
  encryption_key_unavailable: [
    "缺少可用加密密钥",
    "Encryption key unavailable",
  ],
  target_disabled: ["邮件目标已停用", "Email target disabled"],
  target_changed: ["邮件目标版本已变更", "Email target version changed"],
  target_unavailable: ["邮件目标不可用", "Email target unavailable"],
  rule_changed: [
    "规则变更取消了自动交付",
    "Automatic delivery cancelled after rule change",
  ],
  rule_disabled: [
    "规则停用取消了自动交付",
    "Automatic delivery cancelled after disabling the rule",
  ],
  rule_deleted: [
    "规则删除取消了自动交付",
    "Automatic delivery cancelled after deleting the rule",
  ],
  network_error: ["网络连接失败", "Network connection failed"],
  tls_error: ["TLS 验证失败", "TLS verification failed"],
  smtp_rejected: ["SMTP 服务器拒绝", "SMTP server rejected the message"],
  smtp_temporary: ["SMTP 暂时失败", "Temporary SMTP failure"],
  lease_expired: ["交付租约过期", "Delivery lease expired"],
  attempts_exhausted: ["已耗尽重试次数", "Retry attempts exhausted"],
  invalid_message: ["邮件内容校验失败", "Message validation failed"],
  relay_resolution_failed: [
    "邮件中继域名解析失败",
    "Email relay resolution failed",
  ],
  relay_address_denied: [
    "邮件中继地址未获批准",
    "Email relay address not approved",
  ],
  relay_connect_failed: ["邮件中继连接失败", "Email relay connection failed"],
  tls_configuration_failed: ["TLS 配置不可用", "TLS configuration unavailable"],
  tls_verification_failed: ["TLS 验证失败", "TLS verification failed"],
  tls_required: ["邮件中继不支持所需 TLS", "Required TLS unavailable"],
  authentication_configuration_failed: [
    "邮件认证配置不可用",
    "Email authentication configuration unavailable",
  ],
  authentication_failed: ["邮件认证失败", "Email authentication failed"],
  smtp_permanent_rejection: ["SMTP 永久拒绝", "Permanent SMTP rejection"],
  smtp_temporary_rejection: ["SMTP 暂时拒绝", "Temporary SMTP rejection"],
  smtp_transport_failed: ["SMTP 传输失败", "SMTP transport failed"],
  outcome_unknown: ["交付结果不确定", "Delivery outcome unknown"],
};
export function quotaLabel(code: string, text: Translate) {
  const label = Object.hasOwn(labels, code) ? labels[code] : undefined;
  return label ? text(...label) : text("状态未知", "Status unknown");
}

const errors: Record<string, [string, string]> = {
  version_conflict: [
    "规则已被其他管理员修改。返回并刷新后重新编辑，不会覆盖新配置。",
    "Another administrator changed this rule. Go back, refresh, and edit again; the newer configuration was not overwritten.",
  ],
  password_change_required: [
    "必须先修改密码，再返回此页。",
    "Change your password before returning to this page.",
  ],
  permission_denied: [
    "无权执行此操作。请确认平台管理员权限后刷新。",
    "Permission denied. Confirm platform administrator access and refresh.",
  ],
  forbidden: [
    "无权执行此操作。请确认平台管理员权限后刷新。",
    "Permission denied. Confirm platform administrator access and refresh.",
  ],
  access_denied: [
    "当前会话无权执行此操作。请重新登录或确认平台管理员权限。",
    "This session cannot perform this action. Sign in again or confirm platform administrator access.",
  ],
  unauthenticated: [
    "会话已失效，请重新登录。",
    "The session has expired. Sign in again.",
  ],
  rule_conflict: [
    "此仓库已有规则，请刷新并编辑现有规则。",
    "This repository already has a rule. Refresh and edit the existing rule.",
  ],
  rule_limit: [
    "已达到规则上限（包含保留的删除记录）。",
    "The rule limit has been reached, including retained deleted records.",
  ],
  rule_deleted: [
    "规则已删除，请返回并刷新。",
    "This rule was deleted. Go back and refresh.",
  ],
  repository_inactive: [
    "仓库当前不可用于告警，请检查仓库状态。",
    "This repository is unavailable for alerting. Check its state.",
  ],
  invalid_request: [
    "配置未通过校验。请检查阈值、持续时间和目标后重试。",
    "Configuration validation failed. Check thresholds, hold durations, and target, then retry.",
  ],
  not_found: [
    "资源或管理接口不可用，请刷新并核对 Gateway 版本。",
    "The resource or management endpoint is unavailable. Refresh and check the Gateway version.",
  ],
  target_disabled: [
    "邮件目标已停用，刷新后选择可用目标。",
    "The email target was disabled. Refresh and choose an available target.",
  ],
  email_disabled: [
    "邮件通道未开启，无法启用规则。",
    "The email channel is disabled; the rule cannot be enabled.",
  ],
  encryption_key_unavailable: [
    "缺少可用加密密钥，无法启用规则。",
    "Encryption key unavailable; the rule cannot be enabled.",
  ],
};
export function safeQuotaError(error: unknown, text: Translate): string {
  const code =
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    typeof error.code === "string"
      ? error.code
      : "";
  const label = Object.hasOwn(errors, code) ? errors[code] : undefined;
  return label
    ? text(...label)
    : text(
        "请求失败。请检查连接并刷新后重试。",
        "Request failed. Check the connection and refresh before retrying.",
      );
}

export function percent(value: number) {
  return `${value / 100}%`;
}
export function sampleBytes(value: number | undefined, text: Translate) {
  if (value === undefined || !Number.isFinite(value) || value < 0)
    return text("未知", "Unknown");
  const approximate = !Number.isSafeInteger(value);
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"];
  const unit =
    value === 0 ? 0 : Math.min(6, Math.floor(Math.log(value) / Math.log(1024)));
  return `${approximate ? "≈ " : ""}${(value / 1024 ** unit).toLocaleString(undefined, { maximumFractionDigits: 2 })} ${units[unit]}`;
}

export type PolicyDraft = Record<keyof RepositoryQuotaAlertPolicy, string>;
export function policyDraft(policy?: RepositoryQuotaAlertPolicy): PolicyDraft {
  return {
    warningBasisPoints: policy ? String(policy.warningBasisPoints / 100) : "",
    criticalBasisPoints: policy ? String(policy.criticalBasisPoints / 100) : "",
    recoveryBelowBasisPoints: policy
      ? String(policy.recoveryBelowBasisPoints / 100)
      : "",
    warningForSeconds: policy ? String(policy.warningForSeconds) : "",
    criticalForSeconds: policy ? String(policy.criticalForSeconds) : "",
    recoveryForSeconds: policy ? String(policy.recoveryForSeconds) : "",
    maxSampleAgeSeconds: policy ? String(policy.maxSampleAgeSeconds) : "",
  };
}
export function parsePolicy(
  draft: PolicyDraft,
): RepositoryQuotaAlertPolicy | undefined {
  const parsePercent = (v: string) => {
    if (!/^\d{1,3}(?:\.\d{1,2})?$/.test(v.trim())) return NaN;
    const [whole, fraction = ""] = v.trim().split(".");
    return Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
  };
  const parseSeconds = (v: string) =>
    /^\d{1,5}$/.test(v.trim()) ? Number(v.trim()) : NaN;
  const p = {
    warningBasisPoints: parsePercent(draft.warningBasisPoints),
    criticalBasisPoints: parsePercent(draft.criticalBasisPoints),
    recoveryBelowBasisPoints: parsePercent(draft.recoveryBelowBasisPoints),
    warningForSeconds: parseSeconds(draft.warningForSeconds),
    criticalForSeconds: parseSeconds(draft.criticalForSeconds),
    recoveryForSeconds: parseSeconds(draft.recoveryForSeconds),
    maxSampleAgeSeconds: parseSeconds(draft.maxSampleAgeSeconds),
  };
  if (!(
    p.recoveryBelowBasisPoints > 0 &&
    p.recoveryBelowBasisPoints < p.warningBasisPoints &&
    p.warningBasisPoints < p.criticalBasisPoints &&
    p.criticalBasisPoints <= 10000
  ))
    return;
  if (
    ![p.warningForSeconds, p.criticalForSeconds, p.recoveryForSeconds].every(
      (v) => v >= 1 && v <= 86400,
    )
  )
    return;
  if (!(p.maxSampleAgeSeconds >= 30 && p.maxSampleAgeSeconds <= 3600)) return;
  return p;
}
