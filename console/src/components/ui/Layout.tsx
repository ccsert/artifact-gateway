import { DownOutlined } from "@ant-design/icons";
import { Button, Card as AntdCard } from "antd";
import type { ReactNode } from "react";
import { usePreferences } from "../../lib/preferences";

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
    <div className="ag-page-header flex flex-wrap items-start justify-between gap-4">
      <div className="min-w-0">
        <h1 className="text-[22px] font-semibold tracking-tight text-fg-strong">
          {title}
        </h1>
        {description && (
          <p className="mt-1 text-sm text-fg-tertiary">{description}</p>
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
  bodyClassName = "",
}: {
  children: ReactNode;
  className?: string;
  bodyClassName?: string;
}) {
  return (
    <AntdCard
      variant="outlined"
      className={`ag-card ${className}`}
      styles={{ body: { padding: 0 } }}
    >
      {bodyClassName ? (
        <div className={bodyClassName}>{children}</div>
      ) : (
        children
      )}
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
    <div className="flex items-center justify-between border-b border-line px-5 py-3">
      <h2 className="text-sm font-semibold tracking-tight text-fg-strong">
        {title}
      </h2>
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
    <Card bodyClassName="px-5 py-4">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium uppercase tracking-wider text-fg-tertiary">
          {label}
        </span>
        {icon && <span className="text-fg-disabled">{icon}</span>}
      </div>
      <div className="mt-2 text-2xl font-semibold tracking-tight text-fg-strong">
        {value}
      </div>
      {sub && <div className="mt-1 text-xs text-fg-tertiary">{sub}</div>}
    </Card>
  );
}

export function Pagination({
  hasMore,
  loading,
  onMore,
  label,
  disabled,
}: {
  hasMore: boolean;
  loading?: boolean;
  onMore: () => void;
  label?: string;
  disabled?: boolean;
}) {
  const { text } = usePreferences();
  if (!hasMore) return null;
  const actionLabel = label ?? text("加载更多", "Load more");
  return (
    <div className="ag-pagination-footer flex justify-end border-t border-line px-4 py-3">
      <Button
        icon={<DownOutlined />}
        onClick={onMore}
        loading={loading}
        disabled={disabled}
        aria-label={actionLabel}
      >
        {actionLabel}
      </Button>
    </div>
  );
}

export function Field({
  label,
  children,
  hint,
  group,
  id,
  error,
}: {
  label: string;
  children: ReactNode;
  hint?: string;
  group?: boolean;
  id?: string;
  error?: string;
}) {
  if (group) {
    return (
      <fieldset className="min-w-0 border-0 p-0">
        <legend className="mb-1.5 block text-xs font-medium text-fg-secondary">
          {label}
        </legend>
        {children}
        {hint && (
          <span className="mt-1 block text-xs text-fg-disabled">{hint}</span>
        )}
      </fieldset>
    );
  }
  return (
    <div className="block">
      <label className="block" htmlFor={id}>
        <span className="mb-1.5 block text-xs font-medium text-fg-secondary">
          {label}
        </span>
        {children}
      </label>
      {hint && (
        <span
          id={id && `${id}-hint`}
          className="mt-1 block text-xs text-fg-disabled"
        >
          {hint}
        </span>
      )}
      {error && (
        <span
          id={id && `${id}-error`}
          className="mt-1 block text-xs text-[var(--ag-status-danger)]"
        >
          {error}
        </span>
      )}
    </div>
  );
}
