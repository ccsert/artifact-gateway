import type { ReactNode } from "react";
import {
  CloseOutlined,
  InboxOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { Alert, Button, Empty, Spin } from "antd";
import type { Problem } from "../../client";
import { usePreferences } from "../../lib/preferences";

export function Spinner({ className = "" }: { className?: string }) {
  return <Spin className={className} size="small" />;
}

export function Loading({ label }: { label?: string }) {
  const { text } = usePreferences();
  return (
    <div
      className="ag-feedback-enter flex items-center justify-center gap-3 py-16 text-zinc-400"
      role="status"
      aria-live="polite"
      aria-busy="true"
    >
      <Spinner />
      <span className="text-sm">{label ?? text("加载中…", "Loading…")}</span>
    </div>
  );
}

export function isNotFound(error: unknown): boolean {
  // JSON Problem 形态
  const p = error as Problem | undefined;
  if (p?.status === 404 || p?.code === "not_found") return true;
  // 后端未挂载路由时返回纯文本 "404 page not found"
  if (typeof error === "string" && /404|not found/i.test(error)) return true;
  return false;
}

export function ErrorBanner({
  error,
  onRetry,
  title,
  tone = "error",
}: {
  error: unknown;
  onRetry?: () => void;
  title?: string;
  /**
   * `warning` is for a failure the page can live with — a refresh that failed
   * while earlier data stays on screen, or one optional source being
   * unavailable. `error` is for "there is nothing to show".
   */
  tone?: "error" | "warning";
}) {
  const { text } = usePreferences();
  const problem =
    typeof error === "object" && error !== null
      ? (error as Problem)
      : undefined;
  const plainText = typeof error === "string" ? error.trim() : "";
  const routeUnavailable = /(?:^|\b)404(?:\b|$).*not found/i.test(plainText);
  const message = routeUnavailable
    ? text(
        "当前 Gateway 未提供此接口，Console 与 Gateway 版本可能不一致。请更新或重启 Gateway 后重试。",
        "The connected Gateway does not expose this endpoint. The Console and Gateway versions may not match; update or restart Gateway and retry.",
      )
    : problem?.message ||
      (error instanceof Error ? error.message : plainText) ||
      text(
        "请求失败，请检查网络或 Token",
        "Request failed. Check the network or token.",
      );
  return (
    <Alert
      className="ag-feedback-enter"
      type={tone}
      showIcon
      title={title ?? text("请求出错", "Request failed")}
      description={
        <span>
          {message}
          {problem?.code && (
            <span className="ml-2 font-mono text-xs text-[var(--ag-status-danger)] opacity-70">
              [{problem.code}]
            </span>
          )}
          {problem?.requestId && (
            <span className="ml-2 font-mono text-xs text-[var(--ag-status-danger)] opacity-50">
              req: {problem.requestId}
            </span>
          )}
        </span>
      }
      action={
        onRetry ? (
          <Button
            danger={tone === "error"}
            size="small"
            icon={<ReloadOutlined />}
            onClick={onRetry}
          >
            {text("重试", "Retry")}
          </Button>
        ) : undefined
      }
    />
  );
}

/**
 * A notice about something that already happened or a standing explanation,
 * as opposed to `ErrorBanner`, which is about a request that failed.
 *
 * Success and warning notices are closable by default; pass `onClose` to clear
 * the caller's state so a later notice of the same text shows again. Standing
 * explanations use `tone="info"`, which drops the icon and stays open.
 */
export function Notice({
  tone,
  title,
  description,
  action,
  closable,
  onClose,
}: {
  tone: "success" | "info" | "warning";
  title: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
  closable?: boolean;
  onClose?: () => void;
}) {
  return (
    <Alert
      className="ag-feedback-enter"
      type={tone}
      showIcon={tone !== "info"}
      // antd 6.5 treats an object `closable` without `closeIcon` as not
      // closable at all, so the callback alone would silently remove the button.
      closable={
        (closable ?? tone !== "info")
          ? { closeIcon: <CloseOutlined />, onClose }
          : false
      }
      title={title}
      description={description}
      action={action}
    />
  );
}

export function EmptyState({
  title,
  hint,
  action,
  icon,
  compact = false,
  className = "",
}: {
  title: string;
  hint?: string;
  action?: ReactNode;
  icon?: ReactNode;
  compact?: boolean;
  className?: string;
}) {
  return (
    <Empty
      className={`ag-feedback-enter ag-empty-state ${compact ? "ag-empty-state-compact py-5" : "py-10"} ${className}`}
      style={compact ? { marginBlock: 0, marginInline: 0 } : undefined}
      image={
        <span className="ag-empty-state-icon" aria-hidden="true">
          {icon ?? <InboxOutlined />}
        </span>
      }
      description={
        <div className="space-y-1 text-center">
          <p className="text-sm font-medium text-[var(--ag-content-primary)]">
            {title}
          </p>
          {hint && (
            <p className="mx-auto max-w-xl text-xs leading-5 text-[var(--ag-content-tertiary)]">
              {hint}
            </p>
          )}
        </div>
      }
    >
      {action}
    </Empty>
  );
}
