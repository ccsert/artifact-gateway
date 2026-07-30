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

// 版本列表通用渲染：结构化表格 + 语义化排序 + 折叠 + 过滤，版本多也可读。

// 语义化版本排序（新→旧）。非数字段按字典序，数字段按数值。
function compareVersions(a: string, b: string): number {
  const pa = a.split(/[.\-+_]/);
  const pb = b.split(/[.\-+_]/);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const xa = pa[i] ?? '';
    const xb = pb[i] ?? '';
    const na = Number(xa);
    const nb = Number(xb);
    const aNum = xa !== '' && !Number.isNaN(na);
    const bNum = xb !== '' && !Number.isNaN(nb);
    if (aNum && bNum) {
      if (na !== nb) return nb - na;
    } else if (aNum) {
      return 1; // 数字段排前（较新）
    } else if (bNum) {
      return -1;
    } else {
      const c = xb.localeCompare(xa);
      if (c !== 0) return c;
    }
  }
  return 0;
}

const COLLAPSE_THRESHOLD = 8;
const COLLAPSED_COUNT = 5;

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
  const [expanded, setExpanded] = useState(false);
  const [filter, setFilter] = useState('');

  const sorted = [...items].sort((x, y) => compareVersions(x.label, y.label));
  const filtered = filter ? sorted.filter((v) => v.label.toLowerCase().includes(filter.toLowerCase())) : sorted;
  const collapsible = !filter && filtered.length > COLLAPSE_THRESHOLD;
  const shown = collapsible && !expanded ? filtered.slice(0, COLLAPSED_COUNT) : filtered;

  if (items.length === 0) return null;
  return (
    <div>
      <div className="mb-2 flex items-center justify-between gap-3">
        <div className="text-sm font-medium text-zinc-200">
          {title} <Badge tone="zinc">{items.length}</Badge>
        </div>
        {items.length > COLLAPSE_THRESHOLD && (
          <input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="过滤版本…"
            className="w-36 rounded-md border border-zinc-700 bg-zinc-800/60 px-2 py-1 text-xs text-zinc-200 placeholder-zinc-600 outline-none focus:border-cyan-500/60"
          />
        )}
      </div>
      <div className="overflow-hidden rounded-lg border border-zinc-800">
        <table className="w-full text-left">
          <tbody className="divide-y divide-zinc-800/60">
            {shown.map((v, idx) => {
              const active = v.active || v.label === current;
              const isLatest = !filter && idx === 0;
              return (
                <tr
                  key={v.label}
                  onClick={() => onSelect?.(v.label)}
                  className={`cursor-pointer transition-colors ${
                    active ? 'bg-cyan-500/10' : 'hover:bg-zinc-800/40'
                  }`}
                >
                  <td className="px-3 py-2">
                    <span className={`font-mono text-xs ${active ? 'font-medium text-cyan-300' : 'text-zinc-200'}`}>
                      {v.label}
                    </span>
                    {isLatest && (
                      <span className="ml-2 rounded-full bg-emerald-500/15 px-1.5 py-0.5 text-[10px] text-emerald-400">
                        最新
                      </span>
                    )}
                  </td>
                  <td className="px-3 py-2 text-right text-xs text-zinc-500">{v.hint ?? ''}</td>
                </tr>
              );
            })}
            {shown.length === 0 && (
              <tr>
                <td className="px-3 py-4 text-center text-xs text-zinc-600" colSpan={2}>
                  无匹配版本
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {collapsible && (
        <button
          onClick={() => setExpanded(!expanded)}
          className="mt-1.5 text-xs text-zinc-500 hover:text-cyan-300"
        >
          {expanded ? '收起 ▲' : `显示全部 ${filtered.length} 个版本 ▼`}
        </button>
      )}
    </div>
  );
}
