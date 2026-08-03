import type { ReactNode } from 'react';

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-tight text-zinc-50">{title}</h1>
        {description && <p className="mt-1 text-sm text-zinc-500">{description}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Card({ children, className = '' }: { children: ReactNode; className?: string }) {
  return (
    <div className={`rounded-xl border border-zinc-800/80 bg-zinc-900/60 ${className}`}>{children}</div>
  );
}

export function CardHeader({ title, extra }: { title: string; extra?: ReactNode }) {
  return (
    <div className="flex items-center justify-between border-b border-zinc-800/80 px-4 py-3">
      <h2 className="text-sm font-semibold text-zinc-200">{title}</h2>
      {extra}
    </div>
  );
}

export function StatCard({
  label,
  value,
  sub,
  icon,
}: {
  label: string;
  value: ReactNode;
  sub?: ReactNode;
  icon?: ReactNode;
}) {
  return (
    <Card className="px-5 py-4">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium uppercase tracking-wider text-zinc-500">{label}</span>
        {icon && <span className="text-zinc-600">{icon}</span>}
      </div>
      <div className="mt-2 text-2xl font-semibold tracking-tight text-zinc-50">{value}</div>
      {sub && <div className="mt-1 text-xs text-zinc-500">{sub}</div>}
    </Card>
  );
}

export function DataTable({
  columns,
  children,
  className = '',
  columnClassNames,
}: {
  columns: ReactNode[];
  children: ReactNode;
  className?: string;
  columnClassNames?: string[];
}) {
  return (
    <div className="overflow-x-auto">
      <table className={`w-full text-left text-sm ${className}`}>
        <thead>
          <tr className="border-b border-zinc-800 text-xs uppercase tracking-wider text-zinc-500">
            {columns.map((c, i) => (
              <th
                key={i}
                className={`px-4 py-2.5 font-medium ${columnClassNames?.[i] ?? ''}`}
              >
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-zinc-800/60">{children}</tbody>
      </table>
    </div>
  );
}

export function Pagination({
  hasMore,
  loading,
  onMore,
}: {
  hasMore: boolean;
  loading?: boolean;
  onMore: () => void;
}) {
  if (!hasMore) return null;
  return (
    <div className="flex justify-center border-t border-zinc-800/60 px-4 py-3">
      <button
        onClick={onMore}
        disabled={loading}
        className="rounded-md border border-zinc-700 px-4 py-1.5 text-sm text-zinc-300 hover:bg-zinc-800 disabled:opacity-50"
      >
        {loading ? '加载中…' : '加载更多'}
      </button>
    </div>
  );
}

export function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-xs font-medium text-zinc-400">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-zinc-600">{hint}</span>}
    </label>
  );
}

export const inputClass =
  'w-full rounded-md border border-zinc-700 bg-zinc-800/60 px-3 py-2 text-sm text-zinc-100 placeholder-zinc-600 outline-none focus:border-cyan-500/60 focus:ring-1 focus:ring-cyan-500/30';

export const btnPrimary =
  'rounded-md bg-cyan-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-cyan-500 disabled:opacity-50';
export const btnSecondary =
  'rounded-md border border-zinc-700 px-3.5 py-2 text-sm text-zinc-300 hover:bg-zinc-800 disabled:opacity-50';
export const btnDanger =
  'rounded-md bg-rose-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-rose-500 disabled:opacity-50';
