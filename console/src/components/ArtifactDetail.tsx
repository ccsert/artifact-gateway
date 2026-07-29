import { useState } from 'react';
import { usageFor } from '../lib/usage';
import type { UsageSnippet } from '../lib/usage';
import { Badge } from './Badge';
import { formatBytes, formatDate, shortDigest } from '../lib/format';

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        } catch {
          /* ignore */
        }
      }}
      className="shrink-0 rounded border border-zinc-700 px-2 py-0.5 text-[10px] text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200"
    >
      {copied ? '已复制 ✓' : '复制'}
    </button>
  );
}

function Snippet({ snippet }: { snippet: UsageSnippet }) {
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-950/60 px-3 py-2">
      <div className="mb-1 flex items-center justify-between">
        <span className="text-[10px] uppercase tracking-wider text-zinc-500">{snippet.label}</span>
        <CopyButton text={snippet.code} />
      </div>
      <code className="block whitespace-pre-wrap break-all font-mono text-xs leading-5 text-cyan-300">
        {snippet.code}
      </code>
    </div>
  );
}

export interface ArtifactMeta {
  coordinate: string;
  digest?: string;
  size?: number;
  contentType?: string;
  createdAt?: string;
  publisher?: string;
  state?: string;
}

// 统一的制品详情：使用方法 + 元信息。版本列表由父组件按格式单独渲染。
export function ArtifactDetailView({
  format,
  repoName,
  meta,
  tag,
  versions,
}: {
  format: string;
  repoName: string;
  meta: ArtifactMeta;
  tag?: string;
  versions?: React.ReactNode;
}) {
  const snippets = usageFor(format, repoName, meta.coordinate, tag);

  return (
    <div className="space-y-4">
      {/* 元信息 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {meta.publisher && (
          <div className="rounded-lg border border-zinc-800 px-3 py-2">
            <div className="text-[10px] uppercase tracking-wider text-zinc-500">发布者</div>
            <div className="mt-0.5 truncate font-mono text-xs text-zinc-100" title={meta.publisher}>
              {meta.publisher}
            </div>
          </div>
        )}
        {meta.size !== undefined && (
          <div className="rounded-lg border border-zinc-800 px-3 py-2">
            <div className="text-[10px] uppercase tracking-wider text-zinc-500">大小</div>
            <div className="mt-0.5 text-xs font-semibold text-zinc-100">{formatBytes(meta.size)}</div>
          </div>
        )}
        {meta.createdAt && (
          <div className="rounded-lg border border-zinc-800 px-3 py-2">
            <div className="text-[10px] uppercase tracking-wider text-zinc-500">发布时间</div>
            <div className="mt-0.5 text-xs font-semibold text-zinc-100">{formatDate(meta.createdAt)}</div>
          </div>
        )}
        {meta.state && (
          <div className="rounded-lg border border-zinc-800 px-3 py-2">
            <div className="text-[10px] uppercase tracking-wider text-zinc-500">状态</div>
            <div className="mt-0.5 text-xs font-semibold text-zinc-100">{meta.state}</div>
          </div>
        )}
      </div>
      {meta.digest && (
        <div className="rounded-lg border border-zinc-800 px-3 py-2">
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">摘要 (digest)</div>
          <div className="mt-0.5 flex items-center justify-between gap-2">
            <code className="break-all font-mono text-xs text-zinc-300" title={meta.digest}>
              {shortDigest(meta.digest)}
            </code>
            <CopyButton text={meta.digest} />
          </div>
        </div>
      )}

      {/* 使用方法 */}
      {snippets.length > 0 && (
        <div>
          <div className="mb-2 text-sm font-medium text-zinc-200">使用方法</div>
          <div className="space-y-2">
            {snippets.map((s) => (
              <Snippet key={s.label} snippet={s} />
            ))}
          </div>
        </div>
      )}

      {/* 版本列表（由父组件提供） */}
      {versions}
    </div>
  );
}

// 版本列表通用渲染
export function VersionList({
  title,
  items,
  current,
  onSelect,
}: {
  title: string;
  items: { label: string; hint?: string; active?: boolean }[];
  current?: string;
  onSelect?: (label: string) => void;
}) {
  if (items.length === 0) return null;
  return (
    <div>
      <div className="mb-2 text-sm font-medium text-zinc-200">
        {title} <Badge tone="zinc">{items.length}</Badge>
      </div>
      <div className="flex flex-wrap gap-1.5">
        {items.map((v) => (
          <button
            key={v.label}
            onClick={() => onSelect?.(v.label)}
            title={v.hint}
            className={`rounded-full border px-2.5 py-1 font-mono text-[11px] transition-colors ${
              v.active || v.label === current
                ? 'border-cyan-500/60 bg-cyan-500/10 text-cyan-300'
                : 'border-zinc-700 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200'
            }`}
          >
            {v.label}
          </button>
        ))}
      </div>
    </div>
  );
}
