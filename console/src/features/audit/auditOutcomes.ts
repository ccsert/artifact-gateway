import type { MessageKey } from "../../messages";
import type { Translate } from "../../lib/preferences";
import type { BadgeTone } from "../../components/ui/Badge";

const AUDIT_OUTCOME_LABELS: Record<string, MessageKey> = {
  resolved: "audit.resolved",
  denied: "audit.denied",
  access_denied: "audit.accessDenied",
  internal_preferred: "audit.internalCopyPreferred",
  not_found: "audit.notFound",
  group_disabled: "audit.groupDisabled",
  proxy_denied: "audit.proxyDenied",
  upstream_error: "audit.upstreamError",
  storage_error: "audit.storageError",
  failed: "audit.failed",
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

export function auditOutcomeLabel(value: string, t: Translate): string {
  const label = AUDIT_OUTCOME_LABELS[value];
  return label ? t(label) : value;
}

export function auditOutcomeTone(value: string | undefined): BadgeTone {
  return AUDIT_OUTCOME_TONES[value ?? ""] ?? "neutral";
}
