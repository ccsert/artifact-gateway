import { CheckOutlined, CopyOutlined } from "@ant-design/icons";
import { Button, Select, Tooltip } from "antd";
import type { UsageSnippet } from "../lib/usage";

export type VersionSelectOption = { value: string; label: string };

export function SearchableVersionSelect({
  value,
  options,
  onChange,
  loading = false,
  placeholder = "搜索并选择版本",
  notFoundContent = "没有匹配版本",
  className = "",
}: {
  value: string;
  options: VersionSelectOption[];
  onChange: (value: string) => void;
  loading?: boolean;
  placeholder?: string;
  notFoundContent?: string;
  className?: string;
}) {
  return (
    <Select
      showSearch={{
        optionFilterProp: "label",
        filterOption: (input, option) =>
          String(option?.label ?? "")
            .toLowerCase()
            .includes(input.toLowerCase()),
      }}
      value={value || undefined}
      options={options}
      onChange={onChange}
      loading={loading}
      placeholder={placeholder}
      notFoundContent={notFoundContent}
      listHeight={280}
      className={`w-full ${className}`}
    />
  );
}

export function UsageSnippetBlock({
  snippet,
  copied,
  onCopy,
  compact = false,
}: {
  snippet: UsageSnippet;
  copied: boolean;
  onCopy: () => void;
  compact?: boolean;
}) {
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-950/70 p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="text-[11px] font-medium text-zinc-400">
          {snippet.label}
        </span>
        <Tooltip title={copied ? "已复制" : "复制使用方式"}>
          <Button
            type="text"
            size="small"
            aria-label={`复制${snippet.label}`}
            onClick={onCopy}
            icon={copied ? <CheckOutlined /> : <CopyOutlined />}
          />
        </Tooltip>
      </div>
      <pre
        className={`max-w-full overflow-x-auto whitespace-pre font-mono text-[11px] leading-5 text-cyan-100 ${compact ? "max-h-24" : ""}`}
      >
        {snippet.code}
      </pre>
    </div>
  );
}

export function MetadataItem({
  label,
  value,
  mono = false,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] font-medium text-zinc-600">{label}</div>
      <div
        className={`mt-1 truncate text-zinc-300 ${mono ? "font-mono text-[11px]" : "text-xs"}`}
        title={value}
      >
        {value}
      </div>
    </div>
  );
}
