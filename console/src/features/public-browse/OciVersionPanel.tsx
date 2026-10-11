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
  const { locale, t } = usePreferences();
  const loadOciTags = onLoadOciTags;
  const loadOciManifest = onLoadOciManifest;
  const copyPageLink = onCopyPageLink;
  const copyUsage = onCopyUsage;
  return (
    <div className="grid gap-5 px-2 py-1 lg:grid-cols-[minmax(0,300px)_minmax(0,1fr)]">
      <div>
        <div className="flex items-center justify-between gap-3">
          <span className="block text-xs font-medium text-fg-tertiary">
            {t("publicBrowse.selectImageVersion")}
          </span>
          <span className="text-xs text-fg-disabled">
            {t("publicBrowse.loaded")} {row.protocolVersions.length}
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
              {t("publicBrowse.retry")}
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
              ? t("publicBrowse.loadingVersions")
              : t("publicBrowse.noMatchingVersions")
          }
          placeholder={t("publicBrowse.searchImageVersions")}
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
              ? t("publicBrowse.loadingVersionPage")
              : t("publicBrowse.loadMoreVersions", {
                  pageSize: VERSION_PAGE_SIZE,
                })}
          </Button>
        )}
        <p className="mt-2 text-xs leading-5 text-fg-disabled">
          {t("publicBrowse.versionPageHint", { pageSize: VERSION_PAGE_SIZE })}
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
                {t("publicBrowse.openVersionPage")}
              </Button>
              <Button
                type="link"
                size="small"
                onClick={() => void copyPageLink(row.selectedProtocolHref)}
              >
                {copiedCoordinate === row.selectedProtocolHref
                  ? t("publicBrowse.linkCopied")
                  : t("publicBrowse.copyLink")}
              </Button>
            </div>
            <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-line py-3 text-xs sm:grid-cols-4">
              <MetadataItem
                label={t("publicBrowse.image")}
                value={row.item.coordinate}
                mono
              />
              <MetadataItem
                label={t("publicBrowse.version")}
                value={row.selectedProtocolVersionItem.value}
                mono
              />
              <MetadataItem
                label={t("publicBrowse.published")}
                value={
                  row.ociDetail?.loading
                    ? t("publicBrowse.loading")
                    : formatDate(
                        row.ociDetail?.createdAt ?? row.item.createdAt,
                        locale,
                      )
                }
              />
              <MetadataItem
                label={t("publicBrowse.publisher")}
                value={
                  row.ociDetail?.publisher ??
                  row.item.publisher ??
                  t("publicBrowse.notRecorded")
                }
                mono
              />
              <MetadataItem
                label={t("publicBrowse.checksum")}
                value={
                  row.ociDetail?.loading
                    ? t("publicBrowse.loading")
                    : (row.ociDetail?.digest ??
                      row.selectedProtocolVersionItem.digest ??
                      row.item.digest ??
                      t("publicBrowse.notRecorded"))
                }
                mono
              />
              <MetadataItem
                label={t("publicBrowse.imageSize")}
                value={
                  row.ociDetail?.loading
                    ? t("publicBrowse.loading")
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
                  {t("publicBrowse.retry")}
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
            {t("publicBrowse.selectLoadedVersionHint")}
          </div>
        )}
      </div>
    </div>
  );
}
