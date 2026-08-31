import type { ReactNode } from "react";
import { artifactFormatVisualizationTone } from "../lib/artifactFormatVisuals";
import { usePreferences } from "../lib/preferences";

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

export function StateBadge({ state }: { state: string | undefined }) {
  const { text } = usePreferences();
  const value = state ?? "unknown";
  const labels: Record<string, [string, string]> = {
    active: ["运行中", "Active"],
    deleting: ["删除中", "Deleting"],
    deleted: ["已删除", "Deleted"],
    pending: ["待处理", "Pending"],
    retrying: ["重试中", "Retrying"],
    delivering: ["投递中", "Delivering"],
    running: ["运行中", "Running"],
    completed: ["已完成", "Completed"],
    failed: ["失败", "Failed"],
    cancelled: ["已取消", "Cancelled"],
    expired: ["已过期", "Expired"],
    revoked: ["已吊销", "Revoked"],
    enabled: ["已启用", "Enabled"],
    disabled: ["已停用", "Disabled"],
    online: ["在线", "Online"],
    offline: ["离线", "Offline"],
    stale: ["陈旧", "Stale"],
    healthy: ["健康", "Healthy"],
    degraded: ["降级", "Degraded"],
    critical: ["严重", "Critical"],
    denied: ["拒绝", "Denied"],
    submitted: ["已投递", "Submitted"],
    succeeded: ["已送达", "Succeeded"],
    dead: ["已停止", "Dead"],
    never: ["未扫描", "Not scanned"],
  };
  const label = labels[value];
  return (
    <Badge tone={stateTone[value] ?? "neutral"}>
      {label ? text(label[0], label[1]) : value}
    </Badge>
  );
}

export function FormatBadge({ format }: { format: string | undefined }) {
  const value = format ?? "?";
  return (
    <Badge tone={artifactFormatVisualizationTone(value) ?? "neutral"}>
      {value}
    </Badge>
  );
}
