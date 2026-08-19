import type { ReactNode } from "react";
import { usePreferences } from "../lib/preferences";

const toneClasses: Record<string, string> = {
  green: "border-emerald-500/30 bg-emerald-500/10 text-emerald-300",
  red: "border-rose-500/30 bg-rose-500/10 text-rose-300",
  amber: "border-amber-500/30 bg-amber-500/10 text-amber-300",
  blue: "border-sky-500/30 bg-sky-500/10 text-sky-300",
  zinc: "border-zinc-600/40 bg-zinc-700/20 text-zinc-400",
  cyan: "border-cyan-500/30 bg-cyan-500/10 text-cyan-300",
  violet: "border-violet-500/30 bg-violet-500/10 text-violet-300",
};

export function Badge({
  children,
  tone = "zinc",
}: {
  children: ReactNode;
  tone?: keyof typeof toneClasses;
}) {
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 font-mono text-xs leading-4 ${toneClasses[tone]}`}
    >
      {children}
    </span>
  );
}

const stateTone: Record<string, keyof typeof toneClasses> = {
  active: "green",
  deleting: "amber",
  deleted: "red",
  visible: "green",
  pending: "amber",
  retrying: "amber",
  delivering: "blue",
  running: "blue",
  completed: "green",
  failed: "red",
  cancelled: "zinc",
  open: "blue",
  committed: "green",
  aborted: "zinc",
  expired: "zinc",
  verified: "green",
  copying: "blue",
  success: "green",
  allow: "green",
  deny: "red",
  denied: "red",
  revoked: "red",
  enabled: "green",
  disabled: "zinc",
  online: "green",
  stale: "amber",
  offline: "red",
  healthy: "green",
  degraded: "amber",
  critical: "red",
  submitted: "green",
  succeeded: "green",
  dead: "red",
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
    <Badge tone={stateTone[value] ?? "zinc"}>
      {label ? text(label[0], label[1]) : value}
    </Badge>
  );
}

const formatTone: Record<string, keyof typeof toneClasses> = {
  oci: "cyan",
  maven: "amber",
  conan: "violet",
  raw: "blue",
  npm: "green",
  pypi: "cyan",
  go: "blue",
  apt: "amber",
};

export function FormatBadge({ format }: { format: string | undefined }) {
  const value = format ?? "?";
  return <Badge tone={formatTone[value] ?? "zinc"}>{value}</Badge>;
}
