import { DownOutlined } from "@ant-design/icons";
import { Button, Card as AntdCard } from "antd";
import type { ReactNode } from "react";

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
        <h1 className="text-xl font-semibold tracking-tight text-zinc-50">
          {title}
        </h1>
        {description && (
          <p className="mt-1 text-sm text-zinc-500">{description}</p>
        )}
      </div>
      {actions && (
        <div className="flex flex-wrap items-center justify-end gap-2">
          {actions}
        </div>
      )}
    </div>
  );
}

export function Card({
  children,
  className = "",
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <AntdCard
      variant="outlined"
      className={`bg-zinc-900/60 ${className}`}
      styles={{ body: { padding: 0 } }}
    >
      {children}
    </AntdCard>
  );
}

export function CardHeader({
  title,
  extra,
}: {
  title: string;
  extra?: ReactNode;
}) {
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
        <span className="text-xs font-medium uppercase tracking-wider text-zinc-500">
          {label}
        </span>
        {icon && <span className="text-zinc-600">{icon}</span>}
      </div>
      <div className="mt-2 text-2xl font-semibold tracking-tight text-zinc-50">
        {value}
      </div>
      {sub && <div className="mt-1 text-xs text-zinc-500">{sub}</div>}
    </Card>
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
      <Button icon={<DownOutlined />} onClick={onMore} loading={loading}>
        加载更多
      </Button>
    </div>
  );
}

export function Field({
  label,
  children,
  hint,
  group,
}: {
  label: string;
  children: ReactNode;
  hint?: string;
  group?: boolean;
}) {
  if (group) {
    return (
      <fieldset className="min-w-0 border-0 p-0">
        <legend className="mb-1.5 block text-xs font-medium text-zinc-400">
          {label}
        </legend>
        {children}
        {hint && (
          <span className="mt-1 block text-xs text-zinc-600">{hint}</span>
        )}
      </fieldset>
    );
  }
  return (
    <div className="block">
      <label className="block">
        <span className="mb-1.5 block text-xs font-medium text-zinc-400">
          {label}
        </span>
        {children}
      </label>
      {hint && <span className="mt-1 block text-xs text-zinc-600">{hint}</span>}
    </div>
  );
}
