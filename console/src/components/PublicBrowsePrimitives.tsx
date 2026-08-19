import { CheckOutlined, CopyOutlined } from "@ant-design/icons";
import { Button, Select, Tooltip } from "antd";
import type { UsageSnippet } from "../lib/usage";
import { usePreferences } from "../lib/preferences";

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
  const { text } = usePreferences();
  return (
    <div className="rounded-lg border border-zinc-800 bg-zinc-950/70 p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="text-xs font-medium text-zinc-400">
          {snippet.label}
        </span>
        <Tooltip
          title={
            copied
              ? text("已复制", "Copied")
              : text("复制使用方式", "Copy usage")
          }
        >
          <Button
            type="text"
            size="small"
            aria-label={`${text("复制", "Copy")} ${snippet.label}`}
            onClick={onCopy}
            icon={copied ? <CheckOutlined /> : <CopyOutlined />}
          />
        </Tooltip>
      </div>
      <pre
        className={`max-w-full overflow-x-auto whitespace-pre font-mono text-xs leading-5 text-cyan-100 ${compact ? "max-h-24" : ""}`}
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
      <div className="text-xs font-medium text-zinc-600">{label}</div>
      <div
        className={`mt-1 truncate text-zinc-300 ${mono ? "font-mono text-xs" : "text-xs"}`}
        title={value}
      >
        {value}
      </div>
    </div>
  );
}
