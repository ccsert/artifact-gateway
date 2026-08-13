import type { ReactNode } from "react";
import { Card } from "../../components/Layout";

export function PolicyCard({
  eyebrow,
  title,
  description,
  status,
  children,
}: {
  eyebrow: string;
  title: string;
  description: string;
  status: ReactNode;
  children: ReactNode;
}) {
  return (
    <Card className="min-w-0">
      <div className="flex flex-wrap items-start justify-between gap-5 border-b border-zinc-800/70 px-6 py-5">
        <div className="min-w-0 max-w-3xl">
          <div className="text-[11px] font-semibold uppercase tracking-[0.14em] text-zinc-600">
            {eyebrow}
          </div>
          <h3 className="mt-1.5 text-base font-semibold text-zinc-100">
            {title}
          </h3>
          <p className="mt-1.5 text-sm leading-6 text-zinc-500">
            {description}
          </p>
        </div>
        <div className="flex min-h-8 shrink-0 items-center gap-2">{status}</div>
      </div>
      <div className="space-y-5 px-6 py-5">{children}</div>
    </Card>
  );
}

export function ScopeFact({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 bg-[var(--ag-table-header)] px-4 py-3">
      <div className="text-[11px] font-medium text-zinc-600">{label}</div>
      <div
        className="mt-1 truncate text-xs font-medium text-zinc-300"
        title={value}
      >
        {value}
      </div>
    </div>
  );
}

export function PolicySectionHeader({
  title,
  description,
}: {
  title: string;
  description: string;
}) {
  return (
    <div className="border-t border-zinc-800/70 pt-5 first:border-t-0 first:pt-0">
      <h4 className="text-sm font-semibold text-zinc-200">{title}</h4>
      <p className="mt-1 text-xs leading-5 text-zinc-500">{description}</p>
    </div>
  );
}
