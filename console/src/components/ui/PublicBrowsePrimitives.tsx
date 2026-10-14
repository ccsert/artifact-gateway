import { CheckOutlined, CopyOutlined } from "@ant-design/icons";
import { Button, Select, Tooltip } from "antd";
import type { UsageSnippet } from "../../lib/usage";
import { usePreferences } from "../../lib/preferences";

export type VersionSelectOption = { value: string; label: string };

export function SearchableVersionSelect({
  value,
  options,
  onChange,
  loading = false,
  placeholder,
  notFoundContent,
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
  const { t } = usePreferences();
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
      placeholder={placeholder ?? t("artifactDetail.searchAndSelectAVersion")}
      notFoundContent={
        notFoundContent ?? t("artifactDetail.noMatchingVersions")
      }
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
  const { t } = usePreferences();
  return (
    <div className="rounded-lg border border-line bg-surface-translucent p-3">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="text-xs font-medium text-fg-secondary">
          {snippet.label}
        </span>
        <Tooltip title={copied ? t("ui.copied") : t("ui.copyUsage")}>
          <Button
            type="text"
            size="small"
            aria-label={`${t("ui.copy")} ${snippet.label}`}
            onClick={onCopy}
            icon={copied ? <CheckOutlined /> : <CopyOutlined />}
          />
        </Tooltip>
      </div>
      <pre
        className={`max-w-full overflow-x-auto whitespace-pre font-mono text-xs leading-5 text-[var(--ag-content-secondary)] ${compact ? "max-h-24" : ""}`}
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
      <div className="text-xs font-medium text-fg-disabled">{label}</div>
      <div
        className={`mt-1 truncate text-fg-secondary ${mono ? "font-mono text-xs" : "text-xs"}`}
        title={value}
      >
        {value}
      </div>
    </div>
  );
}
