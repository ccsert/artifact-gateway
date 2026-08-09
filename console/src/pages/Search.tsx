import { useEffect, useMemo, useState } from "react";
import { DownOutlined, LinkOutlined, UpOutlined } from "@ant-design/icons";
import { Button, Descriptions, Input, Space, Table, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { searchArtifacts } from "../client";
import type { GlobalArtifactSearchHit } from "../client";
import { PageHeader } from "../components/Layout";
import { Loading, ErrorBanner, EmptyState } from "../components/Feedback";
import { Badge, FormatBadge } from "../components/Badge";
import { formatBytes, formatDate } from "../lib/format";
import { mavenGA, mavenVersion } from "../lib/usage";
import {
  MavenArtifactDetail,
  ConanArtifactDetail,
  RawArtifactDetail,
} from "../components/ArtifactRowDetail";
import { OciImageDetail } from "../components/OciImageDetail";
import { NpmPackageDetail } from "../components/NpmPackageDetail";
import { CopyableValue, MetricStrip } from "../components/ConsolePrimitives";
import { usePreferences } from "../lib/preferences";

const SEARCH_PAGE_SIZE = 100;

type MavenGroup = {
  key: string;
  repositoryId: string;
  repositoryName: string;
  format: "maven";
  coordinate: string;
  hits: GlobalArtifactSearchHit[];
};

type SearchRow = GlobalArtifactSearchHit | MavenGroup;

type SearchTableRow = {
  key: string;
  row: SearchRow;
  representative: GlobalArtifactSearchHit;
};

function isMavenGroup(row: SearchRow): row is MavenGroup {
  return row.format === "maven" && "hits" in row;
}

export function artifactTarget(hit: GlobalArtifactSearchHit): string {
  const params = new URLSearchParams({ artifact: hit.coordinate });
  if (hit.buildNumber && hit.buildNumber > 0)
    params.set("build", String(hit.buildNumber));
  if (hit.format === "oci" && hit.digest) params.set("reference", hit.digest);
  if (hit.format === "npm" && hit.version) params.set("version", hit.version);
  return `/repositories/${hit.repositoryId}?${params.toString()}`;
}

export function artifactVersionSizeLabel(hit: GlobalArtifactSearchHit): string {
  const size = formatBytes(hit.size);
  return hit.format === "npm" && hit.version
    ? `${hit.version} · ${size}`
    : size;
}

function buildRows(hits: GlobalArtifactSearchHit[]): SearchRow[] {
  const rows: SearchRow[] = [];
  const groups = new Map<string, MavenGroup>();
  for (const hit of hits) {
    const ga = hit.format === "maven" ? mavenGA(hit.coordinate) : null;
    if (!ga) {
      rows.push(hit);
      continue;
    }
    const key = `${hit.repositoryId}:${ga}`;
    const existing = groups.get(key);
    if (existing) existing.hits.push(hit);
    else {
      const group: MavenGroup = {
        key,
        repositoryId: hit.repositoryId,
        repositoryName: hit.repositoryName,
        format: "maven",
        coordinate: ga,
        hits: [hit],
      };
      groups.set(key, group);
      rows.push(group);
    }
  }
  for (const row of rows) {
    if (isMavenGroup(row)) {
      row.hits.sort(
        (a, b) =>
          (b.createdAt ?? "").localeCompare(a.createdAt ?? "") ||
          (b.buildNumber ?? 0) - (a.buildNumber ?? 0),
      );
    }
  }
  return rows;
}

function mavenVersionLabel(hit: GlobalArtifactSearchHit): string {
  const version = mavenVersion(hit.coordinate) ?? hit.coordinate;
  return hit.buildNumber && hit.buildNumber > 0
    ? `${version} · build #${hit.buildNumber}`
    : version;
}

export function SearchPage() {
  const { locale, text } = usePreferences();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const q = (params.get("q") ?? "").trim();
  const [query, setQuery] = useState(q);
  const [hits, setHits] = useState<GlobalArtifactSearchHit[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [searchedRepos, setSearchedRepos] = useState(0);
  const [nextPageToken, setNextPageToken] = useState<string | undefined>();
  const [expandedRow, setExpandedRow] = useState<string | null>(null);
  const [selectedVersion, setSelectedVersion] = useState<string | null>(null);

  useEffect(() => {
    setQuery(q);
    if (!q) {
      setHits([]);
      setLoading(false);
      setError(null);
      setSearchedRepos(0);
      setNextPageToken(undefined);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    setHits([]);
    setSearchedRepos(0);
    setNextPageToken(undefined);
    void searchArtifacts({ query: { q, pageSize: SEARCH_PAGE_SIZE } }).then(
      (response) => {
        if (cancelled) return;
        if (response.error || !response.data)
          setError(
            response.error ??
              new Error(text("搜索制品失败", "Artifact search failed")),
          );
        else {
          setHits(response.data.items);
          setSearchedRepos(response.data.searchedRepositories);
          setNextPageToken(response.data.nextPageToken);
        }
        setLoading(false);
      },
    );
    return () => {
      cancelled = true;
    };
  }, [q, text]);

  const rows = useMemo(() => buildRows(hits), [hits]);
  const loadMore = async () => {
    if (!nextPageToken || loadingMore) return;
    setLoadingMore(true);
    setError(null);
    const response = await searchArtifacts({
      query: { q, pageSize: SEARCH_PAGE_SIZE, pageToken: nextPageToken },
    });
    setLoadingMore(false);
    if (response.error || !response.data) {
      setError(
        response.error ??
          new Error(
            text("加载更多搜索结果失败", "Failed to load more results"),
          ),
      );
      return;
    }
    setHits((current) => [...current, ...response.data.items]);
    setSearchedRepos(response.data.searchedRepositories);
    setNextPageToken(response.data.nextPageToken);
  };

  const openHit = (hit: GlobalArtifactSearchHit) =>
    navigate(artifactTarget(hit));

  const tableRows: SearchTableRow[] = rows.map((row, index) => ({
    key: isMavenGroup(row)
      ? row.key
      : `${row.repositoryId}-${row.coordinate}-${row.buildNumber ?? 0}-${index}`,
    row,
    representative: isMavenGroup(row) ? row.hits[0] : row,
  }));

  const setRowExpanded = (tableRow: SearchTableRow, next: boolean) => {
    setExpandedRow(next ? tableRow.key : null);
    setSelectedVersion(
      next && isMavenGroup(tableRow.row)
        ? `${tableRow.row.key}:${tableRow.row.hits[0].coordinate}:${tableRow.row.hits[0].buildNumber ?? 0}`
        : null,
    );
  };

  const columns: ColumnsType<SearchTableRow> = [
    {
      title: text("制品 / 坐标", "Artifact / coordinate"),
      key: "coordinate",
      width: 420,
      render: (_, tableRow) => {
        const expanded = expandedRow === tableRow.key;
        const row = tableRow.row;
        return (
          <div>
            <div className="flex min-w-0 items-center gap-2">
              <span className="truncate font-mono text-xs text-zinc-200">
                {row.coordinate}
              </span>
              <Badge
                tone={
                  tableRow.representative.matchKind === "digest"
                    ? "cyan"
                    : "zinc"
                }
              >
                {tableRow.representative.matchKind === "digest"
                  ? text("SHA-256 匹配", "SHA-256 match")
                  : text("坐标匹配", "Coordinate match")}
              </Badge>
            </div>
            {!isMavenGroup(row) && row.digest && (
              <div className="mt-1">
                <CopyableValue
                  value={row.digest}
                  label={row.digest.slice(0, 20)}
                />
              </div>
            )}
            <span className="sr-only">
              {expanded
                ? text("已展开", "Expanded")
                : text("已收起", "Collapsed")}
            </span>
          </div>
        );
      },
    },
    {
      title: text("仓库", "Repository"),
      key: "repository",
      width: 200,
      render: (_, tableRow) => (
        <Link
          to={artifactTarget(tableRow.representative)}
          onClick={(event) => event.stopPropagation()}
          className="text-xs text-cyan-400 hover:text-cyan-300"
        >
          {tableRow.row.repositoryName}
        </Link>
      ),
    },
    {
      title: text("格式", "Format"),
      key: "format",
      width: 110,
      render: (_, tableRow) => <FormatBadge format={tableRow.row.format} />,
    },
    {
      title: text("版本 / 大小", "Versions / size"),
      key: "versionOrSize",
      width: 150,
      render: (_, tableRow) => (
        <span className="text-xs text-zinc-400">
          {isMavenGroup(tableRow.row)
            ? text(
                `${tableRow.row.hits.length} 个版本`,
                `${tableRow.row.hits.length} versions`,
              )
            : artifactVersionSizeLabel(tableRow.row)}
        </span>
      ),
    },
    {
      title: text("更新时间", "Updated"),
      key: "createdAt",
      width: 180,
      render: (_, tableRow) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(tableRow.representative.createdAt, locale)}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 100,
      render: (_, tableRow) => {
        const expanded = expandedRow === tableRow.key;
        return (
          <Space size={2}>
            <Tooltip
              title={
                expanded
                  ? text("收起详情", "Collapse details")
                  : text("展开详情", "Expand details")
              }
            >
              <Button
                type="text"
                size="small"
                aria-label={`${expanded ? text("收起", "Collapse") : text("展开", "Expand")} ${tableRow.row.coordinate}`}
                icon={expanded ? <UpOutlined /> : <DownOutlined />}
                onClick={(event) => {
                  event.stopPropagation();
                  setRowExpanded(tableRow, !expanded);
                }}
              />
            </Tooltip>
            <Tooltip
              title={text("在仓库中精确定位", "Open exact repository location")}
            >
              <Button
                type="text"
                size="small"
                icon={<LinkOutlined />}
                aria-label={text(
                  `在仓库中打开 ${tableRow.row.coordinate}`,
                  `Open ${tableRow.row.coordinate} in repository`,
                )}
                onClick={(event) => {
                  event.stopPropagation();
                  openHit(tableRow.representative);
                }}
              />
            </Tooltip>
          </Space>
        );
      },
    },
  ];

  const expandedRowRender = (tableRow: SearchTableRow) => {
    const row = tableRow.row;
    if (isMavenGroup(row)) {
      const hit =
        row.hits.find(
          (item) =>
            `${row.key}:${item.coordinate}:${item.buildNumber ?? 0}` ===
            selectedVersion,
        ) ?? row.hits[0];
      return (
        <div className="px-2 py-1">
          <div className="mb-3 flex items-center justify-between gap-3">
            <div>
              <div className="text-sm font-medium text-zinc-200">
                {text("版本与构建", "Versions and builds")}
              </div>
              <div className="mt-1 text-xs text-zinc-500">
                {text(
                  "选择版本查看 POM、digest 和发布信息",
                  "Select a version to view its POM, digest, and publication details",
                )}
              </div>
            </div>
            <Button
              size="small"
              icon={<LinkOutlined />}
              onClick={() => openHit(tableRow.representative)}
            >
              {text("打开最新版本", "Open latest version")}
            </Button>
          </div>
          <div className="max-h-64 overflow-y-auto rounded-md border border-zinc-800/80">
            {row.hits.map((versionHit) => {
              const versionKey = `${row.key}:${versionHit.coordinate}:${versionHit.buildNumber ?? 0}`;
              const selected = selectedVersion === versionKey;
              return (
                <div
                  key={versionKey}
                  className={`flex items-center justify-between gap-4 border-b border-zinc-800/60 px-3 py-2 last:border-b-0 ${selected ? "bg-cyan-400/10" : ""}`}
                >
                  <button
                    type="button"
                    className="min-w-0 flex-1 text-left"
                    onClick={() => setSelectedVersion(versionKey)}
                  >
                    <div className="truncate font-mono text-xs text-zinc-200">
                      {mavenVersionLabel(versionHit)}
                    </div>
                    <div className="mt-0.5 text-[11px] text-zinc-500">
                      {formatDate(versionHit.createdAt, locale)} ·{" "}
                      {formatBytes(versionHit.size)} ·{" "}
                      {versionHit.publisher ??
                        text("发布者未记录", "Publisher not recorded")}
                    </div>
                  </button>
                  <Space size={2}>
                    <Tooltip
                      title={
                        versionHit.digest ?? text("暂无 digest", "No digest")
                      }
                    >
                      <span>
                        {versionHit.digest ? (
                          <CopyableValue
                            value={versionHit.digest}
                            label={versionHit.digest.slice(0, 14)}
                          />
                        ) : (
                          <span className="text-xs text-zinc-600">
                            {text("无 digest", "No digest")}
                          </span>
                        )}
                      </span>
                    </Tooltip>
                    <Button
                      type="text"
                      size="small"
                      icon={<LinkOutlined />}
                      aria-label={text(
                        `打开 ${versionHit.coordinate}`,
                        `Open ${versionHit.coordinate}`,
                      )}
                      onClick={() => openHit(versionHit)}
                    />
                  </Space>
                </div>
              );
            })}
          </div>
          <Descriptions
            size="small"
            column={4}
            className="my-4"
            items={[
              {
                key: "publisher",
                label: text("发布者", "Publisher"),
                children: hit.publisher ?? text("未记录", "Not recorded"),
              },
              {
                key: "createdAt",
                label: text("发布时间", "Published"),
                children: formatDate(hit.createdAt, locale),
              },
              {
                key: "size",
                label: text("大小", "Size"),
                children: formatBytes(hit.size),
              },
              {
                key: "matchKind",
                label: text("匹配依据", "Matched by"),
                children:
                  hit.matchKind === "digest"
                    ? text("SHA-256 摘要", "SHA-256 digest")
                    : text("制品坐标", "Artifact coordinate"),
              },
            ]}
          />
          {hit.digest && (
            <div className="mb-4 rounded-md border border-zinc-800/80 bg-zinc-950/40 px-3 py-2.5">
              <div className="mb-1 text-[11px] font-medium text-zinc-500">
                SHA-256 Digest
              </div>
              <CopyableValue
                value={hit.digest}
                label={hit.digest}
                className="w-full text-xs text-zinc-300"
              />
            </div>
          )}
          <MavenArtifactDetail
            repoId={hit.repositoryId}
            repoName={hit.repositoryName}
            meta={{
              coordinate: hit.coordinate,
              digest: hit.digest,
              size: hit.size,
              createdAt: hit.createdAt,
              publisher: hit.publisher,
              buildNumber: hit.buildNumber,
            }}
          />
          <div className="mt-4 flex justify-end">
            <Button
              type="primary"
              icon={<LinkOutlined />}
              onClick={() => openHit(hit)}
            >
              {text("在仓库中打开此版本", "Open this version in repository")}
            </Button>
          </div>
        </div>
      );
    }
    return (
      <div className="px-2 py-1">
        <Descriptions
          size="small"
          column={4}
          className="mb-4"
          items={[
            {
              key: "repository",
              label: text("仓库", "Repository"),
              children: row.repositoryName,
            },
            {
              key: "publisher",
              label: text("发布者", "Publisher"),
              children: row.publisher ?? text("未记录", "Not recorded"),
            },
            {
              key: "contentType",
              label: text("内容类型", "Content type"),
              children: row.contentType ?? text("未记录", "Not recorded"),
            },
            {
              key: "createdAt",
              label: text("发布时间", "Published"),
              children: formatDate(row.createdAt, locale),
            },
          ]}
        />
        {row.digest && (
          <div className="mb-4 rounded-md border border-zinc-800/80 bg-zinc-950/40 px-3 py-2.5">
            <div className="mb-1 flex items-center gap-2 text-[11px] font-medium text-zinc-500">
              <span>SHA-256 Digest</span>
              <Badge tone={row.matchKind === "digest" ? "cyan" : "zinc"}>
                {row.matchKind === "digest"
                  ? text("精确匹配", "Exact match")
                  : text("校验信息", "Checksum")}
              </Badge>
            </div>
            <CopyableValue
              value={row.digest}
              label={row.digest}
              className="w-full text-xs text-zinc-300"
            />
          </div>
        )}
        {row.format === "oci" && (
          <OciImageDetail
            repositoryId={row.repositoryId}
            repository={row.repositoryName}
            image={row.coordinate}
            initialReference={row.digest}
          />
        )}
        {row.format === "conan" && (
          <ConanArtifactDetail
            repoId={row.repositoryId}
            repoName={row.repositoryName}
            managed
            canDelete={false}
            meta={{
              coordinate: row.coordinate,
              digest: row.digest,
              size: row.size,
              createdAt: row.createdAt,
              publisher: row.publisher,
            }}
          />
        )}
        {row.format === "raw" && (
          <RawArtifactDetail
            repoName={row.repositoryName}
            meta={{
              coordinate: row.coordinate,
              digest: row.digest,
              size: row.size,
              contentType: row.contentType,
              createdAt: row.createdAt,
              publisher: row.publisher,
            }}
          />
        )}
        {row.format === "npm" && (
          <NpmPackageDetail
            repoName={row.repositoryName}
            packageName={row.coordinate}
            initialVersion={row.version}
            size={row.size}
            publisher={row.publisher}
          />
        )}
        <div className="mt-4 flex justify-end">
          <Button
            type="primary"
            icon={<LinkOutlined />}
            onClick={() => openHit(row)}
          >
            {text("在仓库中打开", "Open in repository")}
          </Button>
        </div>
      </div>
    );
  };

  return (
    <div>
      <PageHeader
        title={text("全局搜索", "Global search")}
        description={
          q
            ? text(
                `在所有仓库中搜索 “${q}”`,
                `Searching all repositories for “${q}”`,
              )
            : text(
                "跨仓库搜索制品坐标、包名、路径、引用与 SHA-256",
                "Search artifact coordinates, package names, paths, references, and SHA-256 digests across repositories",
              )
        }
      />
      <Input.Search
        allowClear
        enterButton={
          <Button type="primary" disabled={!query.trim()}>
            {text("搜索", "Search")}
          </Button>
        }
        className="mb-5 max-w-3xl"
        placeholder={text(
          "输入坐标、路径、镜像名前缀或 SHA-256…",
          "Enter a coordinate, path, image prefix, or SHA-256 digest…",
        )}
        value={query}
        loading={loading}
        onChange={(event) => setQuery(event.target.value)}
        onSearch={(value) => {
          const next = value.trim();
          if (next) navigate(`/search?q=${encodeURIComponent(next)}`);
        }}
      />
      {!q ? (
        <EmptyState
          title={text("输入关键词开始搜索", "Enter a keyword to search")}
          hint={text(
            "支持 Maven 坐标、npm 包名、OCI 镜像、Conan 引用、Raw 路径和完整 SHA-256",
            "Supports Maven coordinates, npm packages, OCI images, Conan references, Raw paths, and full SHA-256 digests",
          )}
        />
      ) : error && hits.length === 0 ? (
        <ErrorBanner error={error} />
      ) : loading ? (
        <Loading
          label={
            searchedRepos
              ? text(
                  `正在搜索 ${searchedRepos} 个仓库…`,
                  `Searching ${searchedRepos} repositories…`,
                )
              : text("加载中…", "Loading…")
          }
        />
      ) : rows.length === 0 ? (
        <EmptyState
          title={text("没有匹配的制品", "No matching artifacts")}
          hint={text(
            `已在 ${searchedRepos} 个仓库中搜索 “${q}”，未找到结果`,
            `No results for “${q}” across ${searchedRepos} repositories`,
          )}
        />
      ) : (
        <>
          <MetricStrip
            items={[
              {
                label: text("结果条目", "Results"),
                value: rows.length,
                hint: text(
                  `${hits.length} 个版本或文件`,
                  `${hits.length} versions or files`,
                ),
              },
              {
                label: text("检索仓库", "Repositories searched"),
                value: searchedRepos,
                hint: text(
                  "仅包含当前身份可读仓库",
                  "Only repositories readable by the current identity",
                ),
              },
              {
                label: text("匹配方式", "Match mode"),
                value:
                  hits[0]?.matchKind === "digest"
                    ? text("SHA-256 精确", "Exact SHA-256")
                    : text("坐标前缀", "Coordinate prefix"),
                hint:
                  hits[0]?.matchKind === "digest"
                    ? text(
                        "包含历史可见版本",
                        "Includes older visible versions",
                      )
                    : text("按格式校验输入", "Input is validated by format"),
              },
              {
                label: text("分页状态", "Pagination"),
                value: nextPageToken
                  ? text("还有更多", "More available")
                  : text("已完成", "Complete"),
                hint: nextPageToken
                  ? text(
                      "继续加载不会丢失结果",
                      "Loading more preserves current results",
                    )
                  : text("已到达末尾", "End of results"),
              },
            ]}
          />
          {error && (
            <div className="my-4">
              <ErrorBanner error={error} />
            </div>
          )}
          <div className="mt-4 overflow-hidden rounded-lg border border-zinc-800/80 bg-zinc-900/20">
            <Table<SearchTableRow>
              className="ag-console-table"
              rowKey="key"
              size="middle"
              dataSource={tableRows}
              columns={columns}
              pagination={false}
              scroll={{ x: 1200 }}
              rowClassName="cursor-pointer"
              expandable={{
                expandedRowKeys: expandedRow ? [expandedRow] : [],
                expandedRowRender,
                expandRowByClick: true,
                showExpandColumn: false,
                onExpandedRowsChange: (keys) => {
                  setExpandedRow(
                    keys[0] === undefined ? null : String(keys[0]),
                  );
                },
              }}
              onRow={(tableRow) => ({
                onClick: () =>
                  setRowExpanded(tableRow, expandedRow !== tableRow.key),
              })}
            />
          </div>
          {nextPageToken && (
            <div className="mt-4 flex justify-center">
              <Button loading={loadingMore} onClick={() => void loadMore()}>
                {text("加载更多", "Load more")}
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
