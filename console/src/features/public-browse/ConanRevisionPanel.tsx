import { LinkOutlined } from "@ant-design/icons";
import { Button, Input } from "antd";
import { formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { artifactFormatVisualizationClass } from "../../lib/artifactFormatVisuals";
import { VERSION_PAGE_SIZE } from "./constants";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "../../components/ui/PublicBrowsePrimitives";

import type { ConanGroupTableProps, ConanTableRow } from "./conanGroupTypes";

/** The expanded row of one Conan package: versions, revisions and usage. */
export function ConanRevisionPanel({
  row,
  table,
}: {
  row: ConanTableRow;
  table: ConanGroupTableProps;
}) {
  const { locale, text } = usePreferences();
  const {
    versionFilter,
    copiedCoordinate,
    onSelectReference,
    onSelectRevision,
    onFilterChange,
    onLoadRevisions,
    onOpenArtifact,
    onCopyPageLink,
    onCopyUsage,
  } = table;
  return (
    <div className="grid gap-5 px-2 py-1 lg:grid-cols-[minmax(0,300px)_minmax(0,1fr)]">
      <div>
        <div className="flex items-center justify-between gap-3">
          <label className="text-xs font-medium text-fg-tertiary">
            {text("选择包版本", "Select package version")}{" "}
            <span className="font-normal text-fg-disabled">
              ({row.group.versions.length})
            </span>
          </label>
          <span className="text-xs text-fg-disabled">
            {row.referenceVersion}
          </span>
        </div>
        <SearchableVersionSelect
          className="mt-1.5"
          value={row.selectedReference}
          options={row.group.versions.map((version) => ({
            value: version.coordinate,
            label: version.coordinate,
          }))}
          onChange={(reference) => {
            onSelectReference(row.key, reference);
            onFilterChange("");
            onLoadRevisions(reference);
            onOpenArtifact(reference);
          }}
          placeholder={text(
            "搜索并选择 Conan 包版本",
            "Search Conan package versions",
          )}
        />
        <p className="mt-2 text-xs leading-5 text-fg-disabled">
          {text(
            "同一 name@user/channel 下收拢不同版本；选定版本后再查看 recipe revision。",
            "Versions are grouped under the same name@user/channel; select one to inspect its recipe revision.",
          )}
        </p>
      </div>
      <div className="min-w-0">
        <div className="flex items-center justify-between gap-3">
          <span className="text-xs font-medium text-fg-tertiary">
            Recipe revision
          </span>
          <span className="text-xs text-fg-disabled">
            {row.visibleRevisions.length}/{row.revisions.length}
          </span>
        </div>
        <div className="mt-1.5 flex gap-2">
          <Input
            className="min-w-0 flex-1 font-mono text-xs"
            placeholder={text(
              "输入 revision 或 digest",
              "Enter revision or digest",
            )}
            value={versionFilter}
            onChange={(event) => onFilterChange(event.target.value)}
            onPressEnter={() =>
              onLoadRevisions(row.selectedReference, versionFilter)
            }
          />
          <Button
            loading={row.page?.loading === true}
            onClick={() =>
              onLoadRevisions(row.selectedReference, versionFilter)
            }
          >
            {text("搜索", "Search")}
          </Button>
        </div>
        {row.page?.error && (
          <div className="mt-2 text-xs text-[var(--ag-status-danger)]">
            {row.page.error}
          </div>
        )}
        <SearchableVersionSelect
          value={row.selectedRevisionValue}
          options={row.visibleRevisions.map((revision) => ({
            value: revision.revision,
            label: `${revision.revision} · ${shortDigest(revision.digest)}`,
          }))}
          loading={row.page?.loading === true}
          notFoundContent={
            row.page?.loading && row.visibleRevisions.length === 0
              ? text("正在读取 revision…", "Loading revisions…")
              : text("没有匹配 revision", "No matching revisions")
          }
          placeholder={text(
            "搜索并选择 recipe revision",
            "Search recipe revisions",
          )}
          onChange={(revision) => {
            onSelectRevision(row.selectedReference, revision);
            onOpenArtifact(row.selectedReference, revision);
          }}
        />
        {row.page?.nextPageToken && (
          <Button
            block
            size="small"
            loading={row.page.loading}
            onClick={() =>
              onLoadRevisions(
                row.selectedReference,
                row.page?.query,
                row.page?.nextPageToken,
              )
            }
            className="mt-2"
          >
            {row.page.loading
              ? text("加载中…", "Loading…")
              : text(
                  `再加载 ${VERSION_PAGE_SIZE} 个 revision`,
                  `Load ${VERSION_PAGE_SIZE} more revisions`,
                )}
          </Button>
        )}
        {row.selectedRevisionItem ? (
          <>
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <span className="font-mono text-xs text-fg-strong">
                {row.selectedReference}
              </span>
              <span
                className={`${artifactFormatVisualizationClass("conan")} rounded px-1.5 py-0.5 text-xs`}
              >
                {row.selectedRevisionItem.revision}
              </span>
              <Button
                type="link"
                size="small"
                icon={<LinkOutlined />}
                href={row.versionHref}
              >
                {text("打开版本页", "Open version")}
              </Button>
              <Button
                type="link"
                size="small"
                onClick={() => onCopyPageLink(row.versionHref)}
              >
                {copiedCoordinate === row.versionHref
                  ? text("链接已复制", "Link copied")
                  : text("复制链接", "Copy link")}
              </Button>
            </div>
            <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-line py-3 text-xs sm:grid-cols-4">
              <MetadataItem
                label="Conan reference"
                value={row.selectedReference}
                mono
              />
              <MetadataItem
                label="Recipe revision"
                value={row.selectedRevisionItem.revision}
                mono
              />
              <MetadataItem
                label={text("发布时间", "Published")}
                value={formatDate(row.selectedRevisionItem.createdAt, locale)}
              />
              <MetadataItem
                label={text("发布者", "Publisher")}
                value={row.latest.publisher ?? text("未记录", "Not recorded")}
                mono
              />
              <MetadataItem
                label={text("校验摘要", "Digest")}
                value={
                  row.selectedRevisionItem.digest ??
                  text("未记录", "Not recorded")
                }
                mono
              />
            </div>
            <div className="mt-3 grid gap-3 sm:grid-cols-2">
              {row.snippets.map((snippet) => (
                <UsageSnippetBlock
                  key={snippet.label}
                  snippet={snippet}
                  copied={copiedCoordinate === snippet.code}
                  onCopy={() => onCopyUsage(snippet)}
                />
              ))}
            </div>
          </>
        ) : (
          <div className="mt-3 rounded-md border border-dashed border-line px-4 py-6 text-sm text-fg-disabled">
            {text(
              "选择一个 recipe revision 查看详情与使用方式。",
              "Select a recipe revision to view details and usage.",
            )}
          </div>
        )}
      </div>
    </div>
  );
}
