import { CopyOutlined } from "@ant-design/icons";
import { Button, Tooltip } from "antd";
import { useState, type ReactNode } from "react";

export interface MetricItem {
  label: string;
  value: ReactNode;
  hint?: ReactNode;
  tone?: "default" | "success" | "warning" | "danger";
}

const metricTone: Record<NonNullable<MetricItem["tone"]>, string> = {
  default: "text-zinc-100",
  success: "text-emerald-300",
  warning: "text-amber-300",
  danger: "text-rose-300",
};

export function MetricStrip({ items }: { items: MetricItem[] }) {
  return (
    <div
      className="ag-metric-strip"
      style={{
        gridTemplateColumns: `repeat(${Math.min(Math.max(items.length, 1), 5)}, minmax(0, 1fr))`,
      }}
      role="group"
      aria-label="页面摘要"
    >
      {items.map((item) => (
        <div key={item.label} className="min-w-0 px-5 py-3.5">
          <div className="text-[11px] font-medium uppercase tracking-wider text-zinc-500">
            {item.label}
          </div>
          <div
            className={`mt-1 truncate text-xl font-semibold tracking-tight ${metricTone[item.tone ?? "default"]}`}
          >
            {item.value}
          </div>
          {item.hint && (
            <div className="mt-0.5 truncate text-xs text-zinc-600">
              {item.hint}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

export function FilterField({
  label,
  children,
  className = "",
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <label className={`block min-w-0 ${className}`}>
      <span className="mb-1.5 block text-[11px] font-medium text-zinc-500">
        {label}
      </span>
      {children}
    </label>
  );
}

export function FilterBar({
  children,
  actions,
  className = "",
}: {
  children: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div className={`ag-filter-bar ${className}`}>
      <div className="flex min-w-0 flex-1 flex-wrap items-end gap-3">
        {children}
      </div>
      {actions && (
        <div className="flex shrink-0 items-center gap-2">{actions}</div>
      )}
    </div>
  );
}

export function CopyableValue({
  value,
  label,
  className = "",
}: {
  value: string;
  label?: string;
  className?: string;
}) {
  const [copied, setCopied] = useState(false);
  return (
    <span className={`inline-flex min-w-0 items-center gap-1 ${className}`}>
      <Tooltip title={value}>
        <span className="min-w-0 truncate font-mono">{label ?? value}</span>
      </Tooltip>
      <Tooltip title={copied ? "已复制" : "复制"}>
        <Button
          type="text"
          size="small"
          className="shrink-0"
          aria-label={copied ? "已复制" : "复制"}
          icon={<CopyOutlined />}
          onClick={async (event) => {
            event.stopPropagation();
            try {
              await navigator.clipboard.writeText(value);
              setCopied(true);
              window.setTimeout(() => setCopied(false), 1500);
            } catch {
              // Clipboard access can be unavailable in an insecure local context.
            }
          }}
        />
      </Tooltip>
    </span>
  );
}

export function TechnicalLabel({ children }: { children: ReactNode }) {
  return (
    <span className="font-mono text-[11px] text-zinc-500">{children}</span>
  );
}
