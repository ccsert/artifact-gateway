import { CheckOutlined, CopyOutlined } from "@ant-design/icons";
import { App, Button, Table, Tooltip } from "antd";
import type { TableProps } from "antd/es/table";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { usePreferences } from "../../lib/preferences";
import {
  CONSOLE_PAGE_SIZE,
  useConsolePagination,
} from "./useConsolePagination";

export interface MetricItem {
  label: string;
  value: ReactNode;
  hint?: ReactNode;
  tone?: "default" | "success" | "warning" | "danger";
}

const metricTone: Record<NonNullable<MetricItem["tone"]>, string> = {
  default: "text-fg-strong",
  success: "text-[var(--ag-status-success)]",
  warning: "text-[var(--ag-status-warning)]",
  danger: "text-[var(--ag-status-danger)]",
};

export function MetricStrip({ items }: { items: MetricItem[] }) {
  const { text } = usePreferences();
  const columnCount = Math.min(Math.max(items.length, 1), 5);
  return (
    <div
      className={`ag-metric-strip ag-metric-strip-cols-${columnCount}`}
      role="group"
      aria-label={text("页面摘要", "Page summary")}
    >
      {items.map((item) => (
        <div key={item.label} className="min-w-0 px-5 py-3.5">
          <div className="text-xs font-medium tracking-wide text-fg-tertiary">
            {item.label}
          </div>
          <div
            className={`ag-metric-value mt-1 break-words text-xl font-semibold tracking-tight ${metricTone[item.tone ?? "default"]}`}
          >
            {item.value}
          </div>
          {item.hint && (
            <div className="mt-0.5 text-xs leading-5 text-fg-disabled">
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
      <span className="mb-1.5 block text-xs font-medium text-fg-tertiary">
        {label}
      </span>
      {children}
    </label>
  );
}

export function FilterBar({
  children,
  actions,
  embedded = false,
  className = "",
}: {
  children: ReactNode;
  actions?: ReactNode;
  embedded?: boolean;
  className?: string;
}) {
  return (
    <div
      className={`ag-filter-bar${embedded ? " ag-filter-bar-embedded" : ""} ${className}`}
    >
      <div className="flex min-w-0 flex-1 flex-wrap items-end gap-3">
        {children}
      </div>
      {actions && (
        <div className="flex shrink-0 items-center gap-2">{actions}</div>
      )}
    </div>
  );
}

export function useClipboardAction(resetAfterMs = 1500) {
  const { text } = usePreferences();
  const { message } = App.useApp();
  const [copiedValue, setCopiedValue] = useState<string | null>(null);
  const resetTimer = useRef<number | undefined>(undefined);

  useEffect(
    () => () => {
      if (resetTimer.current !== undefined) {
        window.clearTimeout(resetTimer.current);
      }
    },
    [],
  );

  const copy = useCallback(
    async (value: string, stateKey = value) => {
      try {
        if (!navigator.clipboard?.writeText) {
          throw new Error("Clipboard API unavailable");
        }
        await navigator.clipboard.writeText(value);
        setCopiedValue(stateKey);
        void message.success?.(text("已复制到剪贴板", "Copied to clipboard"));
        if (resetTimer.current !== undefined) {
          window.clearTimeout(resetTimer.current);
        }
        resetTimer.current = window.setTimeout(() => {
          setCopiedValue((current) => (current === stateKey ? null : current));
          resetTimer.current = undefined;
        }, resetAfterMs);
        return true;
      } catch {
        setCopiedValue(null);
        void message.error?.(
          text(
            "复制失败，请手动选择并复制该值。",
            "Copy failed. Select and copy the value manually.",
          ),
        );
        return false;
      }
    },
    [message, resetAfterMs, text],
  );

  return { copiedValue, copy };
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
  const { text } = usePreferences();
  const { copiedValue, copy } = useClipboardAction();
  const copied = copiedValue === value;
  const copyLabel = copied ? text("已复制", "Copied") : text("复制", "Copy");

  return (
    <span className={`inline-flex min-w-0 items-center gap-1 ${className}`}>
      <Tooltip title={value}>
        <span className="min-w-0 truncate font-mono">{label ?? value}</span>
      </Tooltip>
      <Tooltip title={copyLabel}>
        <Button
          type="text"
          size="small"
          className="shrink-0"
          aria-label={copyLabel}
          icon={copied ? <CheckOutlined /> : <CopyOutlined />}
          onClick={async (event) => {
            event.stopPropagation();
            await copy(value);
          }}
        />
      </Tooltip>
      <span className="sr-only" role="status" aria-live="polite">
        {copied ? text("已复制到剪贴板", "Copied to clipboard") : ""}
      </span>
    </span>
  );
}

export function TechnicalLabel({ children }: { children: ReactNode }) {
  return (
    <span className="font-mono text-xs leading-5 text-fg-tertiary">
      {children}
    </span>
  );
}

/**
 * The console's table baseline: themed header and row colors, page-level row
 * density, and the shared empty state.
 *
 * `compact` is for tables nested inside a modal or an expanded row, where the
 * smaller density reads better; a page-level table keeps the default. Nested
 * tables must not set their own vertical scroll — the surrounding modal body
 * already scrolls, and two scrollbars in one dialog read as a bug.
 */
export function ConsoleTable<RecordType extends object = object>({
  compact = false,
  className = "",
  size,
  pagination,
  ...rest
}: TableProps<RecordType> & { compact?: boolean }) {
  const baseline = useConsolePagination();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(CONSOLE_PAGE_SIZE);
  const config = pagination || {};
  const effectiveSize = config.pageSize ?? pageSize;
  const total = config.total ?? rest.dataSource?.length ?? 0;
  const current = Math.min(
    config.current ?? page,
    Math.max(1, Math.ceil(total / effectiveSize)),
  );
  return (
    <Table<RecordType>
      {...rest}
      size={size ?? (compact ? "small" : "middle")}
      className={`ag-console-table ${className}`.trim()}
      pagination={
        pagination === false
          ? false
          : {
              ...baseline,
              ...config,
              current,
              pageSize: effectiveSize,
              onChange: (nextPage, nextSize) => {
                const target = nextSize === effectiveSize ? nextPage : 1;
                setPage(target);
                setPageSize(nextSize);
                config.onChange?.(target, nextSize);
              },
            }
      }
    />
  );
}
