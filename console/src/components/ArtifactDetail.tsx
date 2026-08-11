import { useState } from "react";
import { CheckOutlined, CopyOutlined } from "@ant-design/icons";
import { Button, Collapse, Select, Tooltip } from "antd";
import { usageFor } from "../lib/usage";
import type { UsageSnippet } from "../lib/usage";
import { Badge } from "./Badge";
import { formatBytes, formatDate, shortDigest } from "../lib/format";
import { usePreferences } from "../lib/preferences";
import { ArtifactIntelligencePanel } from "./ArtifactIntelligencePanel";
import { ArtifactScanStatus } from "./ArtifactScanStatus";
import { ArtifactQuarantinePanel } from "./ArtifactQuarantinePanel";

function CopyButton({ text }: { text: string }) {
  const { text: localizedText } = usePreferences();
  const [copied, setCopied] = useState(false);
  const label = copied
    ? localizedText("已复制", "Copied")
    : localizedText("复制", "Copy");
  return (
    <Tooltip title={label}>
      <Button
        type="text"
        size="small"
        aria-label={label}
        icon={copied ? <CheckOutlined /> : <CopyOutlined />}
        onClick={async () => {
          try {
            await navigator.clipboard.writeText(text);
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          } catch {
            /* ignore */
          }
        }}
      />
    </Tooltip>
  );
}

function Snippet({ snippet }: { snippet: UsageSnippet }) {
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-950/60 px-3 py-2">
      <div className="mb-1 flex items-center justify-between">
        <span className="text-[10px] uppercase tracking-wider text-zinc-500">
          {snippet.label}
        </span>
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
  cachedAt?: string;
  sourceUrl?: string;
  publisher?: string;
  buildNumber?: number;
  state?: string;
}

// 统一的制品详情：使用方法 + 元信息。版本列表由父组件按格式单独渲染。
export function ArtifactDetailView({
  format,
  repositoryId,
  repoName,
  meta,
  tag,
  timestampLabel,
  extra,
  versions,
  canQuarantine = false,
  showQuarantine = true,
}: {
  format: string;
  repositoryId?: string;
  repoName: string;
  meta: ArtifactMeta;
  tag?: string;
  timestampLabel?: string;
  extra?: React.ReactNode;
  versions?: React.ReactNode;
  canQuarantine?: boolean;
  showQuarantine?: boolean;
}) {
  const { locale, text } = usePreferences();
  const snippets = usageFor(format, repoName, meta.coordinate, tag);

  return (
    <div className="space-y-4">
      {showQuarantine && (
        <ArtifactQuarantinePanel
          repositoryId={repositoryId}
          coordinate={meta.coordinate}
          digest={meta.digest}
          canManage={canQuarantine}
        />
      )}
      <ArtifactScanStatus
        repositoryId={repositoryId}
        format={format}
        coordinate={meta.coordinate}
        digest={meta.digest}
      />
      <ArtifactIntelligencePanel
        repositoryId={repositoryId}
        format={format}
        coordinate={meta.coordinate}
        digest={meta.digest}
      />
      {/* 元信息 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {meta.publisher && (
          <div className="rounded-lg border border-zinc-800 px-3 py-2">
            <div className="text-[10px] uppercase tracking-wider text-zinc-500">
              {text("发布者", "Publisher")}
            </div>
            <div
              className="mt-0.5 truncate font-mono text-xs text-zinc-100"
              title={meta.publisher}
            >
              {meta.publisher}
            </div>
          </div>
        )}
        {meta.size !== undefined && (
          <div className="rounded-lg border border-zinc-800 px-3 py-2">
            <div className="text-[10px] uppercase tracking-wider text-zinc-500">
              {text("大小", "Size")}
            </div>
            <div className="mt-0.5 text-xs font-semibold text-zinc-100">
              {formatBytes(meta.size)}
            </div>
          </div>
        )}
        {meta.createdAt && (
          <div className="rounded-lg border border-zinc-800 px-3 py-2">
            <div className="text-[10px] uppercase tracking-wider text-zinc-500">
              {timestampLabel ?? text("发布时间", "Published")}
            </div>
            <div className="mt-0.5 text-xs font-semibold text-zinc-100">
              {formatDate(meta.createdAt, locale)}
            </div>
          </div>
        )}
        {meta.contentType && (
          <div className="rounded-lg border border-zinc-800 px-3 py-2">
            <div className="text-[10px] uppercase tracking-wider text-zinc-500">
              {text("内容类型", "Content type")}
            </div>
            <div
              className="mt-0.5 truncate font-mono text-xs text-zinc-100"
              title={meta.contentType}
            >
              {meta.contentType}
            </div>
          </div>
        )}
        {meta.state && (
          <div className="rounded-lg border border-zinc-800 px-3 py-2">
            <div className="text-[10px] uppercase tracking-wider text-zinc-500">
              {text("状态", "State")}
            </div>
            <div className="mt-0.5 text-xs font-semibold text-zinc-100">
              {meta.state}
            </div>
          </div>
        )}
      </div>
      {meta.digest && (
        <div className="rounded-lg border border-zinc-800 px-3 py-2">
          <div className="text-[10px] uppercase tracking-wider text-zinc-500">
            {text("摘要 (digest)", "Digest")}
          </div>
          <div className="mt-0.5 flex items-center justify-between gap-2">
            <code
              className="break-all font-mono text-xs text-zinc-300"
              title={meta.digest}
            >
              {shortDigest(meta.digest)}
            </code>
            <CopyButton text={meta.digest} />
          </div>
        </div>
      )}

      {extra}

      {/* 使用方法 */}
      {snippets.length > 0 && (
        <Collapse
          ghost
          size="small"
          items={[
            {
              key: "usage",
              label: text("使用方法", "Usage"),
              children: (
                <div className="space-y-2">
                  {snippets.map((s) => (
                    <Snippet key={s.label} snippet={s} />
                  ))}
                </div>
              ),
            },
          ]}
        />
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
    const xa = pa[i] ?? "";
    const xb = pb[i] ?? "";
    const na = Number(xa);
    const nb = Number(xb);
    const aNum = xa !== "" && !Number.isNaN(na);
    const bNum = xb !== "" && !Number.isNaN(nb);
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
  const { text } = usePreferences();
  const sorted = [...items].sort((x, y) => compareVersions(x.label, y.label));

  if (items.length === 0) return null;
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 text-sm font-medium text-zinc-200">
        {title} <Badge tone="zinc">{items.length}</Badge>
      </div>
      <Select
        className="w-full"
        showSearch={{
          optionFilterProp: "label",
          filterOption: (input, option) => {
            const item = sorted.find((entry) => entry.label === option?.value);
            return `${item?.label ?? ""} ${item?.hint ?? ""}`
              .toLowerCase()
              .includes(input.toLowerCase());
          },
        }}
        value={
          current && sorted.some((item) => item.label === current)
            ? current
            : undefined
        }
        placeholder={text("搜索并选择版本", "Search and select a version")}
        options={sorted.map((item) => ({
          value: item.label,
          label: item.label,
        }))}
        optionRender={(option) => {
          const item = sorted.find((entry) => entry.label === option.value);
          return (
            <div className="flex items-center justify-between gap-3">
              <span className="font-mono text-xs">
                {item?.label ?? option.label}
              </span>
              {item?.hint && (
                <span className="truncate text-[11px] text-zinc-500">
                  {item.hint}
                </span>
              )}
            </div>
          );
        }}
        onChange={(value) => onSelect?.(value)}
        notFoundContent={text("没有匹配版本", "No matching versions")}
        listHeight={280}
      />
    </div>
  );
}
