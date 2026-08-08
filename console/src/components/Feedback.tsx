import type { ReactNode } from "react";
import { ReloadOutlined } from "@ant-design/icons";
import { Alert, Button, Empty, Spin } from "antd";
import type { Problem } from "../client";

export function Spinner({ className = "" }: { className?: string }) {
  return <Spin className={className} size="small" />;
}

export function Loading({ label = "加载中…" }: { label?: string }) {
  return (
    <div className="ag-feedback-enter flex items-center justify-center gap-3 py-16 text-zinc-400">
      <Spinner />
      <span className="text-sm">{label}</span>
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
}: {
  error: unknown;
  onRetry?: () => void;
}) {
  const problem = error as Problem | undefined;
  const message =
    problem?.message ??
    (error instanceof Error ? error.message : "请求失败，请检查网络或 Token");
  return (
    <Alert
      className="ag-feedback-enter"
      type="error"
      showIcon
      title="请求出错"
      description={
        <span>
          {message}
          {problem?.code && (
            <span className="ml-2 font-mono text-xs text-rose-400/70">
              [{problem.code}]
            </span>
          )}
          {problem?.requestId && (
            <span className="ml-2 font-mono text-xs text-rose-400/50">
              req: {problem.requestId}
            </span>
          )}
        </span>
      }
      action={
        onRetry ? (
          <Button
            danger
            size="small"
            icon={<ReloadOutlined />}
            onClick={onRetry}
          >
            重试
          </Button>
        ) : undefined
      }
    />
  );
}

export function EmptyState({
  title,
  hint,
  action,
}: {
  title: string;
  hint?: string;
  action?: ReactNode;
}) {
  return (
    <Empty
      className="ag-feedback-enter py-12"
      image={Empty.PRESENTED_IMAGE_SIMPLE}
      description={
        <div className="space-y-1 text-center">
          <p className="text-sm font-medium text-zinc-400">{title}</p>
          {hint && <p className="text-xs text-zinc-600">{hint}</p>}
        </div>
      }
    >
      {action}
    </Empty>
  );
}
