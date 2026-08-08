import type { ReactNode } from "react";

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
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 font-mono text-[11px] leading-4 ${toneClasses[tone]}`}
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
};

export function StateBadge({ state }: { state: string | undefined }) {
  const value = state ?? "unknown";
  return <Badge tone={stateTone[value] ?? "zinc"}>{value}</Badge>;
}

const formatTone: Record<string, keyof typeof toneClasses> = {
  oci: "cyan",
  maven: "amber",
  conan: "violet",
  raw: "blue",
};

export function FormatBadge({ format }: { format: string | undefined }) {
  const value = format ?? "?";
  return <Badge tone={formatTone[value] ?? "zinc"}>{value}</Badge>;
}
