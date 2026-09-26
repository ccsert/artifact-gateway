import type { BadgeTone } from "../../components/ui/Badge";

/**
 * Human-readable labels for the audit outcomes the gateway records.
 *
 * The outcome vocabulary lives in the backend (`internal/repository/model.go`),
 * so the console keeps its own bilingual table rather than borrowing the
 * shared state badge, whose labels cover repository and job states. A value
 * this table does not know degrades to the raw code, exactly like an unknown
 * operation code does.
 */

/** Bilingual copy callback from `usePreferences().text`. */
type Localize = (chinese: string, english: string) => string;

const AUDIT_OUTCOME_LABELS: Record<string, { zh: string; en: string }> = {
  resolved: { zh: "已放行", en: "Resolved" },
  denied: { zh: "已拒绝", en: "Denied" },
  access_denied: { zh: "访问被拒", en: "Access denied" },
  internal_preferred: { zh: "命中内部副本", en: "Internal copy preferred" },
  not_found: { zh: "未找到", en: "Not found" },
  group_disabled: { zh: "分组已停用", en: "Group disabled" },
  proxy_denied: { zh: "代理拒绝", en: "Proxy denied" },
  upstream_error: { zh: "上游错误", en: "Upstream error" },
  storage_error: { zh: "存储错误", en: "Storage error" },
  failed: { zh: "失败", en: "Failed" },
};

const AUDIT_OUTCOME_TONES: Record<string, BadgeTone> = {
  resolved: "success",
  denied: "danger",
  access_denied: "danger",
  proxy_denied: "danger",
  not_found: "warning",
  group_disabled: "warning",
  internal_preferred: "neutral",
  upstream_error: "danger",
  storage_error: "danger",
  failed: "danger",
};

/** Whether the outcome names a refusal, which the page counts separately. */
export function auditOutcomeIsDenied(value: string | undefined): boolean {
  return (
    value === "denied" || value === "access_denied" || value === "proxy_denied"
  );
}

export function auditOutcomeLabel(value: string, text: Localize): string {
  const label = AUDIT_OUTCOME_LABELS[value];
  return label ? text(label.zh, label.en) : value;
}

export function auditOutcomeTone(value: string | undefined): BadgeTone {
  return AUDIT_OUTCOME_TONES[value ?? ""] ?? "neutral";
}
