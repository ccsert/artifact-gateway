import type { MessageKey } from "../../messages";
import type { ReactNode } from "react";
import { artifactFormatVisualizationSlot } from "../../lib/artifactFormatVisuals";
import { usePreferences } from "../../lib/preferences";

const toneClasses = {
  success:
    "border-[var(--ag-status-success-border)] bg-[var(--ag-status-success-soft)] text-[var(--ag-status-success)]",
  danger:
    "border-[var(--ag-status-danger-border)] bg-[var(--ag-status-danger-soft)] text-[var(--ag-status-danger)]",
  warning:
    "border-[var(--ag-status-warning-border)] bg-[var(--ag-status-warning-soft)] text-[var(--ag-status-warning)]",
  info: "border-[var(--ag-status-info-border)] bg-[var(--ag-status-info-soft)] text-[var(--ag-status-info)]",
  neutral:
    "border-[var(--ag-border-default)] bg-[var(--ag-surface-disabled)] text-[var(--ag-content-secondary)]",
  "visualization-1": "ag-visualization-tone ag-visualization-tone-1",
  "visualization-2": "ag-visualization-tone ag-visualization-tone-2",
  "visualization-3": "ag-visualization-tone ag-visualization-tone-3",
  "visualization-4": "ag-visualization-tone ag-visualization-tone-4",
  "visualization-5": "ag-visualization-tone ag-visualization-tone-5",
  "visualization-6": "ag-visualization-tone ag-visualization-tone-6",
  "visualization-7": "ag-visualization-tone ag-visualization-tone-7",
  "visualization-8": "ag-visualization-tone ag-visualization-tone-8",
  "visualization-9": "ag-visualization-tone ag-visualization-tone-9",
} as const;

export type BadgeTone = keyof typeof toneClasses;

export function Badge({
  children,
  tone = "neutral",
}: {
  children: ReactNode;
  tone?: BadgeTone;
}) {
  return (
    <span
      className={`inline-flex items-center gap-1 whitespace-nowrap rounded-full border px-2 py-0.5 font-mono text-xs leading-4 ${toneClasses[tone]}`}
    >
      {children}
    </span>
  );
}

const stateTone: Record<string, BadgeTone> = {
  active: "success",
  deleting: "warning",
  deleted: "danger",
  visible: "success",
  pending: "warning",
  retrying: "warning",
  delivering: "info",
  running: "info",
  completed: "success",
  failed: "danger",
  cancelled: "neutral",
  open: "info",
  committed: "success",
  aborted: "neutral",
  expired: "neutral",
  verified: "success",
  copying: "info",
  success: "success",
  allow: "success",
  deny: "danger",
  denied: "danger",
  revoked: "danger",
  enabled: "success",
  disabled: "neutral",
  online: "success",
  stale: "warning",
  offline: "danger",
  healthy: "success",
  degraded: "warning",
  critical: "danger",
  submitted: "success",
  succeeded: "success",
  dead: "danger",
};

/**
 * A lifecycle state as a dot and a word. Accounts, keys and credentials are
 * `subject="identity"`: their `active` reads as enabled, not as running.
 */
export function StateBadge({
  state,
  subject,
}: {
  state: string | undefined;
  subject?: "identity";
}) {
  const { t } = usePreferences();
  const value =
    subject === "identity" && state === "active"
      ? "enabled"
      : (state ?? "unknown");
  const labels: Record<string, MessageKey> = {
    active: "ui.active",
    deleting: "ui.deleting",
    deleted: "ui.deleted",
    pending: "ui.pending",
    retrying: "ui.retrying",
    delivering: "ui.delivering",
    running: "ui.running",
    completed: "ui.completed",
    failed: "ui.failed",
    cancelled: "ui.cancelled",
    expired: "ui.expired",
    revoked: "ui.revoked",
    enabled: "ui.enabled",
    disabled: "ui.disabled",
    online: "ui.online",
    offline: "ui.offline",
    stale: "ui.stale",
    healthy: "ui.healthy",
    degraded: "ui.degraded",
    critical: "ui.critical",
    denied: "ui.denied",
    submitted: "ui.submitted",
    succeeded: "ui.succeeded",
    dead: "ui.dead",
    never: "ui.notScanned",
  };
  const label = labels[value];
  return (
    <StatusText tone={statusTone(stateTone[value])}>
      {label ? t(label) : value}
    </StatusText>
  );
}

export type StatusTone = "success" | "warning" | "danger" | "info" | "neutral";

/** Narrows a badge tone to the status families `StatusText` can render. */
export function statusTone(tone: BadgeTone | undefined): StatusTone {
  return tone === "success" ||
    tone === "warning" ||
    tone === "danger" ||
    tone === "info"
    ? tone
    : "neutral";
}

const statusColor: Record<StatusTone, string> = {
  success: "var(--ag-status-success)",
  warning: "var(--ag-status-warning)",
  danger: "var(--ag-status-danger)",
  info: "var(--ag-status-info)",
  neutral: "var(--ag-content-tertiary)",
};

/**
 * Design language v2 states: a dot plus readable text in the status color.
 * The text always carries the meaning; the dot only aids scanning.
 */
export function StatusText({
  children,
  tone = "neutral",
}: {
  children: ReactNode;
  tone?: StatusTone;
}) {
  return (
    <span
      className="inline-flex items-center gap-1.5 whitespace-nowrap text-xs font-medium leading-5"
      style={{ color: statusColor[tone] }}
    >
      <span
        aria-hidden="true"
        className="size-1.5 shrink-0 rounded-full"
        style={{ background: statusColor[tone] }}
      />
      {children}
    </span>
  );
}

/**
 * Design language v2 format identity: a categorical swatch beside neutral
 * monospace text, so the label stays readable in every theme.
 */
export function FormatBadge({ format }: { format: string | undefined }) {
  const value = format ?? "?";
  const slot = artifactFormatVisualizationSlot(value);
  const swatch =
    slot === undefined
      ? "var(--ag-visualization-fallback)"
      : `var(--ag-visualization-${slot + 1})`;
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap font-mono text-xs leading-5 text-[var(--ag-content-secondary)]">
      <span
        aria-hidden="true"
        className="size-2 shrink-0 rounded-[2px]"
        style={{ background: swatch }}
      />
      {value}
    </span>
  );
}

/** Hosted/Proxy is structure, not data: neutral monospace text, no color. */
export function RepositoryTypeBadge({ type }: { type: string | undefined }) {
  return (
    <span className="whitespace-nowrap font-mono text-xs leading-5 text-[var(--ag-content-secondary)]">
      {type ?? "hosted"}
    </span>
  );
}
