import { LinkOutlined, ReloadOutlined } from "@ant-design/icons";
import { Button } from "antd";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "../../components/ui/PublicBrowsePrimitives";
import { formatBytes, formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import type { UsageSnippet } from "../../lib/usage";
import { VERSION_PAGE_SIZE } from "./constants";
import type { PublicArtifactTableRow } from "./publicArtifactRows";

/** The expanded OCI row: version picker on the left, the chosen version on the right. */
export function OciVersionPanel({
  row,
  copiedCoordinate,
  onSelectVersion,
  onLoadOciTags,
  onLoadOciManifest,
  onCopyPageLink,
  onCopyUsage,
}: {
  row: PublicArtifactTableRow;
  copiedCoordinate: string | null;
  onSelectVersion: (value: string) => void;
  onLoadOciTags: (coordinate: string, after?: string) => Promise<void>;
  onLoadOciManifest: (coordinate: string, tag: string) => Promise<void>;
  onCopyPageLink: (href: string) => unknown;
  onCopyUsage: (snippet: UsageSnippet) => unknown;
}) {
  const { locale, text } = usePreferences();
  const loadOciTags = onLoadOciTags;
  const loadOciManifest = onLoadOciManifest;
  const copyPageLink = onCopyPageLink;
  const copyUsage = onCopyUsage;
  return (
    <div className="grid gap-5 px-2 py-1 lg:grid-cols-[minmax(0,300px)_minmax(0,1fr)]">
      <div>
        <div className="flex items-center justify-between gap-3">
          <span className="block text-xs font-medium text-fg-tertiary">
            {text("选择镜像版本", "Select image version")}
          </span>
          <span className="text-xs text-fg-disabled">
            {text("已加载", "Loaded")} {row.protocolVersions.length}
          </span>
        </div>
        {row.protocolVersionsError && (
          <div className="mt-2 flex items-center justify-between gap-2 text-xs text-[var(--ag-status-danger)]">
            <span>{row.protocolVersionsError}</span>
            <Button
              type="link"
              size="small"
              icon={<ReloadOutlined />}
              onClick={() => void loadOciTags(row.item.coordinate)}
            >
              {text("重试", "Retry")}
            </Button>
          </div>
        )}
        <SearchableVersionSelect
          className="mt-1.5"
          value={row.selectedProtocolVersionValue}
          options={row.protocolVersions.map((version) => ({
            value: version.value,
            label: version.label,
          }))}
          loading={row.protocolVersionsLoading}
          notFoundContent={
            row.protocolVersionsLoading && row.protocolVersions.length === 0
              ? text("正在读取版本…", "Loading versions…")
              : text("没有匹配版本", "No matching versions")
          }
          placeholder={text("搜索并选择镜像版本", "Search image versions")}
          onChange={onSelectVersion}
        />
        {row.nextProtocolPage && (
          <Button
            block
            size="small"
            loading={row.protocolVersionsLoading}
            onClick={() =>
              void loadOciTags(row.item.coordinate, row.nextProtocolPage)
            }
            className="mt-2"
          >
            {row.protocolVersionsLoading
              ? text("加载中…", "Loading…")
              : text(
                  `再加载 ${VERSION_PAGE_SIZE} 个版本`,
                  `Load ${VERSION_PAGE_SIZE} more versions`,
                )}
          </Button>
        )}
        <p className="mt-2 text-xs leading-5 text-fg-disabled">
          {text(
            `每次最多读取 ${VERSION_PAGE_SIZE} 个版本；选择后可查看详情与使用方式。`,
            `Up to ${VERSION_PAGE_SIZE} versions are loaded at a time; select one to view details and usage.`,
          )}
        </p>
      </div>
      <div className="min-w-0">
        {row.selectedProtocolVersionItem ? (
          <>
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-xs text-fg-strong">
                {row.item.coordinate}
              </span>
              <span className="rounded bg-[var(--ag-surface-hover)] px-1.5 py-0.5 text-xs text-[var(--ag-content-secondary)]">
                {row.selectedProtocolVersionItem.label}
              </span>
              <Button
                type="link"
                size="small"
                icon={<LinkOutlined />}
                href={row.selectedProtocolHref}
              >
                {text("打开版本页", "Open version")}
              </Button>
              <Button
                type="link"
                size="small"
                onClick={() => void copyPageLink(row.selectedProtocolHref)}
              >
                {copiedCoordinate === row.selectedProtocolHref
                  ? text("链接已复制", "Link copied")
                  : text("复制链接", "Copy link")}
              </Button>
            </div>
            <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-line py-3 text-xs sm:grid-cols-4">
              <MetadataItem
                label={text("镜像", "Image")}
                value={row.item.coordinate}
                mono
              />
              <MetadataItem
                label={text("版本", "Version")}
                value={row.selectedProtocolVersionItem.value}
                mono
              />
              <MetadataItem
                label={text("发布时间", "Published")}
                value={
                  row.ociDetail?.loading
                    ? text("读取中…", "Loading…")
                    : formatDate(
                        row.ociDetail?.createdAt ?? row.item.createdAt,
                        locale,
                      )
                }
              />
              <MetadataItem
                label={text("发布者", "Publisher")}
                value={
                  row.ociDetail?.publisher ??
                  row.item.publisher ??
                  text("未记录", "Not recorded")
                }
                mono
              />
              <MetadataItem
                label={text("校验摘要", "Digest")}
                value={
                  row.ociDetail?.loading
                    ? text("读取中…", "Loading…")
                    : (row.ociDetail?.digest ??
                      row.selectedProtocolVersionItem.digest ??
                      row.item.digest ??
                      text("未记录", "Not recorded"))
                }
                mono
              />
              <MetadataItem
                label={text("镜像大小", "Image size")}
                value={
                  row.ociDetail?.loading
                    ? text("读取中…", "Loading…")
                    : formatBytes(row.ociDetail?.size ?? row.item.size)
                }
              />
            </div>
            {row.ociDetail?.error && (
              <div className="mt-2 flex items-center gap-2 text-xs text-[var(--ag-status-danger)]">
                <span>{row.ociDetail.error}</span>
                <Button
                  type="link"
                  size="small"
                  icon={<ReloadOutlined />}
                  onClick={() =>
                    row.selectedTag &&
                    void loadOciManifest(row.item.coordinate, row.selectedTag)
                  }
                >
                  {text("重试", "Retry")}
                </Button>
              </div>
            )}
            <div className="mt-3 grid gap-3 sm:grid-cols-2">
              {row.snippets.map((snippet) => (
                <UsageSnippetBlock
                  key={snippet.label}
                  snippet={snippet}
                  copied={copiedCoordinate === snippet.code}
                  onCopy={() => void copyUsage(snippet)}
                />
              ))}
            </div>
          </>
        ) : (
          <div className="rounded-md border border-dashed border-line px-4 py-6 text-sm text-fg-disabled">
            {text(
              "版本加载完成后，可在左侧搜索并选择一个版本。",
              "Once versions load, search and select one on the left.",
            )}
          </div>
        )}
      </div>
    </div>
  );
}
