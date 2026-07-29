import type { ReactNode } from 'react';
import type { Problem } from '../client';

export function Spinner({ className = '' }: { className?: string }) {
  return (
    <svg
      className={`animate-spin ${className}`}
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      aria-label="加载中"
    >
      <circle cx="12" cy="12" r="10" stroke="currentColor" strokeOpacity="0.2" strokeWidth="3" />
      <path d="M22 12a10 10 0 0 0-10-10" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
}

export function Loading({ label = '加载中…' }: { label?: string }) {
  return (
    <div className="flex items-center justify-center gap-3 py-16 text-zinc-400">
      <Spinner />
      <span className="text-sm">{label}</span>
    </div>
  );
}

export function isNotFound(error: unknown): boolean {
  // JSON Problem 形态
  const p = error as Problem | undefined;
  if (p?.status === 404 || p?.code === 'not_found') return true;
  // 后端未挂载路由时返回纯文本 "404 page not found"
  if (typeof error === 'string' && /404|not found/i.test(error)) return true;
  return false;
}

export function ErrorBanner({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const problem = error as Problem | undefined;
  const message =
    problem?.message ?? (error instanceof Error ? error.message : '请求失败，请检查网络或 Token');
  return (
    <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">
      <div className="flex items-center justify-between gap-4">
        <div>
          <span className="font-medium">请求出错：</span>
          {message}
          {problem?.code && <span className="ml-2 font-mono text-xs text-rose-400/70">[{problem.code}]</span>}
          {problem?.requestId && (
            <span className="ml-2 font-mono text-xs text-rose-400/50">req: {problem.requestId}</span>
          )}
        </div>
        {onRetry && (
          <button
            onClick={onRetry}
            className="shrink-0 rounded-md border border-rose-500/40 px-2.5 py-1 text-xs hover:bg-rose-500/20"
          >
            重试
          </button>
        )}
      </div>
    </div>
  );
}

export function EmptyState({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-16 text-center">
      <div className="text-4xl opacity-30">◌</div>
      <p className="text-sm font-medium text-zinc-400">{title}</p>
      {hint && <p className="text-xs text-zinc-600">{hint}</p>}
      {action}
    </div>
  );
}
