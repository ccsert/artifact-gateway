import {
  CheckOutlined,
  CopyOutlined,
  DownOutlined,
  LinkOutlined,
  UpOutlined,
} from "@ant-design/icons";
import { Button, Input, Table, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import type { ArtifactSummary } from "../../client";
import { formatDate, shortDigest } from "../../lib/format";
import { usageFor, type UsageSnippet } from "../../lib/usage";
import { usePreferences } from "../../lib/preferences";
import {
  conanReferenceParts,
  type ConanArtifactGroup,
} from "../../lib/publicBrowseModel";
import { artifactFormatVisualizationClass } from "../../lib/artifactFormatVisuals";
import type { PublicRepository } from "./types";
import { VERSION_PAGE_SIZE } from "./constants";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "../../components/ui/PublicBrowsePrimitives";

export interface ConanRevision {
  revision: string;
  digest?: string;
  createdAt?: string;
}

export interface ConanRevisionPage {
  items: ConanRevision[];
  nextPageToken?: string;
  query: string;
  loaded: boolean;
  loading: boolean;
  error?: string;
}

interface ConanGroupTableProps {
  groups: ConanArtifactGroup[];
  repository: PublicRepository;
  artifactParam: string;
  revisionParam: string;
  expandedGroup: string | null;
  selectedReferences: Record<string, string>;
  selectedRevisions: Record<string, string>;
  revisionPages: Record<string, ConanRevisionPage>;
  versionFilter: string;
  copiedCoordinate: string | null;
  onExpand: (key: string, reference: string) => void;
  onCollapse: () => void;
  onSelectReference: (key: string, reference: string) => void;
  onSelectRevision: (reference: string, revision: string) => void;
  onFilterChange: (value: string) => void;
  onLoadRevisions: (
    reference: string,
    query?: string,
    pageToken?: string,
  ) => void;
  onOpenArtifact: (reference: string, revision?: string) => void;
  onClearArtifactParams: () => void;
  artifactHref: (reference: string, revision?: string) => string;
  onCopyCoordinate: (value: string) => void;
  onCopyPageLink: (value: string) => void;
  onCopyUsage: (snippet: UsageSnippet) => void;
}

interface ConanTableRow {
  key: string;
  group: ConanArtifactGroup;
  latest: ArtifactSummary;
  selectedReference: string;
  expanded: boolean;
  page?: ConanRevisionPage;
  revisions: ConanRevision[];
  visibleRevisions: ConanRevision[];
  selectedRevisionValue: string;
  selectedRevisionItem?: ConanRevision;
  referenceVersion: string;
  versionHref: string;
  snippets: UsageSnippet[];
}

export function ConanGroupTable({
  groups,
  repository,
  artifactParam,
  revisionParam,
  expandedGroup,
  selectedReferences,
  selectedRevisions,
  revisionPages,
  versionFilter,
  copiedCoordinate,
  onExpand,
  onCollapse,
  onSelectReference,
  onSelectRevision,
  onFilterChange,
  onLoadRevisions,
  onOpenArtifact,
  onClearArtifactParams,
  artifactHref,
  onCopyCoordinate,
  onCopyPageLink,
  onCopyUsage,
}: ConanGroupTableProps) {
  const { locale, text } = usePreferences();
  const tableRows: ConanTableRow[] = groups.map((group) => {
    const latest = group.versions[0];
    const urlReference = group.versions.some(
      (version) => version.coordinate === artifactParam,
    )
      ? artifactParam
      : "";
    const selectedReference =
      urlReference || selectedReferences[group.key] || latest.coordinate;
    const expanded = expandedGroup === group.key || Boolean(urlReference);
    const page = revisionPages[selectedReference];
    const revisions = page?.items ?? [];
    const normalizedFilter = versionFilter.trim().toLowerCase();
    const visibleRevisions = normalizedFilter
      ? revisions.filter((revision) =>
          `${revision.revision} ${revision.digest ?? ""} ${revision.createdAt ?? ""}`
            .toLowerCase()
            .includes(normalizedFilter),
        )
      : revisions;
    const selectedRevision = selectedRevisions[selectedReference];
    const requestedRevision =
      artifactParam === selectedReference ? revisionParam : "";
    const preferredRevision =
      (selectedRevision &&
        revisions.some((revision) => revision.revision === selectedRevision) &&
        selectedRevision) ||
      (requestedRevision &&
        revisions.some((revision) => revision.revision === requestedRevision) &&
        requestedRevision) ||
      revisions[0]?.revision ||
      "";
    const selectedRevisionValue = visibleRevisions.some(
      (revision) => revision.revision === preferredRevision,
    )
      ? preferredRevision
      : visibleRevisions[0]?.revision || preferredRevision;
    const selectedRevisionItem = revisions.find(
      (revision) => revision.revision === selectedRevisionValue,
    );
    return {
      key: group.key,
      group,
      latest,
      selectedReference,
      expanded,
      page,
      revisions,
      visibleRevisions,
      selectedRevisionValue,
      selectedRevisionItem,
      referenceVersion: conanReferenceParts(selectedReference).version,
      versionHref: selectedRevisionValue
        ? artifactHref(selectedReference, selectedRevisionValue)
        : artifactHref(selectedReference),
      snippets: usageFor(repository.format, repository.name, selectedReference),
    };
  });

  const toggleRow = (row: ConanTableRow) => {
    if (row.expanded) {
      onCollapse();
      onClearArtifactParams();
      return;
    }
    onExpand(row.key, row.selectedReference);
    onFilterChange("");
    if (!revisionPages[row.selectedReference])
      onLoadRevisions(row.selectedReference);
    onOpenArtifact(
      row.selectedReference,
      row.selectedRevisionValue || undefined,
    );
  };

  const columns: ColumnsType<ConanTableRow> = [
    {
      title: text("Conan 包", "Conan package"),
      dataIndex: "key",
      key: "key",
      width: 260,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-100">{value}</span>
      ),
    },
    {
      title: text("最新版本", "Latest version"),
      key: "latest",
      width: 160,
      render: (_, row) => (
        <span className="font-mono text-xs text-zinc-400">
          {conanReferenceParts(row.latest.coordinate).version}
        </span>
      ),
    },
    {
      title: text("版本数", "Versions"),
      key: "versionCount",
      width: 100,
      render: (_, row) => (
        <span className="text-xs text-zinc-500">
          {row.group.versions.length}
        </span>
      ),
    },
    {
      title: text("当前 revision", "Current revision"),
      key: "revision",
      width: 240,
      render: (_, row) => (
        <span
          className="block max-w-[220px] truncate font-mono text-xs text-zinc-500"
          title={row.selectedRevisionItem?.revision}
        >
          {row.selectedRevisionItem?.revision ??
            (row.expanded
              ? text("读取中…", "Loading…")
              : text("展开后加载", "Load when expanded"))}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 270,
      render: (_, row) => (
        <div className="whitespace-nowrap text-right">
          <Button
            type="text"
            size="small"
            icon={row.expanded ? <UpOutlined /> : <DownOutlined />}
            onClick={() => toggleRow(row)}
          >
            {row.expanded
              ? text("收起", "Collapse")
              : text("选择版本", "Select version")}
          </Button>
          <Button
            type="link"
            size="small"
            icon={<LinkOutlined />}
            href={row.versionHref}
          >
            {row.selectedRevisionValue
              ? text("打开版本", "Open version")
              : text("打开", "Open")}
          </Button>
          <Tooltip
            title={
              copiedCoordinate === row.key
                ? text("已复制", "Copied")
                : text("复制 Conan 包标识", "Copy Conan package identifier")
            }
          >
            <Button
              type="text"
              size="small"
              aria-label={`${text("复制", "Copy")} ${row.key}`}
              icon={
                copiedCoordinate === row.key ? (
                  <CheckOutlined />
                ) : (
                  <CopyOutlined />
                )
              }
              onClick={() => onCopyCoordinate(row.key)}
            />
          </Tooltip>
        </div>
      ),
    },
  ];

  const expandedRowRender = (row: ConanTableRow) => (
    <div className="grid gap-5 px-2 py-1 lg:grid-cols-[minmax(0,300px)_minmax(0,1fr)]">
      <div>
        <div className="flex items-center justify-between gap-3">
          <label className="text-xs font-medium text-zinc-500">
            {text("选择包版本", "Select package version")}{" "}
            <span className="font-normal text-zinc-600">
              ({row.group.versions.length})
            </span>
          </label>
          <span className="text-xs text-zinc-600">{row.referenceVersion}</span>
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
        <p className="mt-2 text-xs leading-5 text-zinc-600">
          {text(
            "同一 name@user/channel 下收拢不同版本；选定版本后再查看 recipe revision。",
            "Versions are grouped under the same name@user/channel; select one to inspect its recipe revision.",
          )}
        </p>
      </div>
      <div className="min-w-0">
        <div className="flex items-center justify-between gap-3">
          <span className="text-xs font-medium text-zinc-500">
            Recipe revision
          </span>
          <span className="text-xs text-zinc-600">
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
          className="mt-3"
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
              <span className="font-mono text-xs text-zinc-100">
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
            <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-zinc-800/80 py-3 text-xs sm:grid-cols-4">
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
          <div className="mt-3 rounded-md border border-dashed border-zinc-800 px-4 py-6 text-sm text-zinc-600">
            {text(
              "选择一个 recipe revision 查看详情与使用方式。",
              "Select a recipe revision to view details and usage.",
            )}
          </div>
        )}
      </div>
    </div>
  );

  return (
    <Table<ConanTableRow>
      className="ag-console-table"
      rowKey="key"
      size="middle"
      dataSource={tableRows}
      columns={columns}
      pagination={false}
      scroll={{ x: 1030 }}
      expandable={{
        expandedRowKeys: tableRows
          .filter((row) => row.expanded)
          .map((row) => row.key),
        expandedRowRender,
        showExpandColumn: false,
      }}
    />
  );
}
