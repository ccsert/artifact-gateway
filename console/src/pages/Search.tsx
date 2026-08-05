import { Fragment, useEffect, useMemo, useState } from "react";
import { DownOutlined, LinkOutlined, UpOutlined } from "@ant-design/icons";
import { Button, Descriptions, Input, Space, Tooltip } from "antd";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { searchArtifacts } from "../client";
import type { GlobalArtifactSearchHit } from "../client";
import { PageHeader, DataTable } from "../components/Layout";
import { Loading, ErrorBanner, EmptyState } from "../components/Feedback";
import { FormatBadge } from "../components/Badge";
import { formatBytes, formatDate } from "../lib/format";
import { mavenGA, mavenVersion } from "../lib/usage";
import {
  MavenArtifactDetail,
  ConanArtifactDetail,
  RawArtifactDetail,
} from "../components/ArtifactRowDetail";
import { OciImageDetail } from "../components/OciImageDetail";
import { CopyableValue, MetricStrip } from "../components/ConsolePrimitives";

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

function isMavenGroup(row: SearchRow): row is MavenGroup {
  return row.format === "maven" && "hits" in row;
}

function artifactTarget(hit: GlobalArtifactSearchHit): string {
  const params = new URLSearchParams({ artifact: hit.coordinate });
  if (hit.buildNumber && hit.buildNumber > 0)
    params.set("build", String(hit.buildNumber));
  return `/repositories/${hit.repositoryId}?${params.toString()}`;
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
          setError(response.error ?? new Error("搜索制品失败"));
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
  }, [q]);

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
      setError(response.error ?? new Error("加载更多搜索结果失败"));
      return;
    }
    setHits((current) => [...current, ...response.data.items]);
    setSearchedRepos(response.data.searchedRepositories);
    setNextPageToken(response.data.nextPageToken);
  };

  const openHit = (hit: GlobalArtifactSearchHit) =>
    navigate(artifactTarget(hit));

  return (
    <div>
      <PageHeader
        title="全局搜索"
        description={
          q ? `在所有仓库中搜索 “${q}”` : "跨仓库搜索制品坐标、路径与引用"
        }
      />
      <Input.Search
        allowClear
        enterButton={
          <Button type="primary" disabled={!query.trim()}>
            搜索
          </Button>
        }
        className="mb-5 max-w-3xl"
        placeholder="输入制品坐标、路径或镜像名前缀…"
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
          title="输入关键词开始搜索"
          hint="支持 Maven 坐标、OCI 镜像、Conan 引用和 Raw 路径"
        />
      ) : error && hits.length === 0 ? (
        <ErrorBanner error={error} />
      ) : loading ? (
        <Loading
          label={
            searchedRepos ? `正在搜索 ${searchedRepos} 个仓库…` : "加载中…"
          }
        />
      ) : rows.length === 0 ? (
        <EmptyState
          title="没有匹配的制品"
          hint={`已在 ${searchedRepos} 个仓库中搜索 “${q}”，未找到结果`}
        />
      ) : (
        <>
          <MetricStrip
            items={[
              {
                label: "结果条目",
                value: rows.length,
                hint: `${hits.length} 个版本或文件`,
              },
              {
                label: "检索仓库",
                value: searchedRepos,
                hint: "仅包含当前身份可读仓库",
              },
              {
                label: "分页状态",
                value: nextPageToken ? "还有更多" : "已完成",
                hint: nextPageToken ? "继续加载不会丢失结果" : "已到达末尾",
              },
            ]}
          />
          {error && (
            <div className="my-4">
              <ErrorBanner error={error} />
            </div>
          )}
          <div className="mt-4 overflow-hidden rounded-lg border border-zinc-800/80 bg-zinc-900/20">
            <DataTable
              columns={[
                "制品 / 坐标",
                "仓库",
                "格式",
                "版本 / 大小",
                "更新时间",
                "",
              ]}
            >
              {rows.map((row, index) => {
                const rowKey = isMavenGroup(row)
                  ? row.key
                  : `${row.repositoryId}-${row.coordinate}-${row.buildNumber ?? 0}-${index}`;
                const expanded = expandedRow === rowKey;
                const representative = isMavenGroup(row) ? row.hits[0] : row;
                return (
                  <Fragment key={rowKey}>
                    <tr
                      className="cursor-pointer hover:bg-zinc-800/30"
                      onClick={() => {
                        setExpandedRow(expanded ? null : rowKey);
                        setSelectedVersion(
                          isMavenGroup(row)
                            ? `${rowKey}:${row.hits[0].coordinate}:${row.hits[0].buildNumber ?? 0}`
                            : null,
                        );
                      }}
                    >
                      <td className="max-w-[34rem] px-4 py-3">
                        <div className="flex items-center gap-2">
                          <span className="text-zinc-600">
                            {expanded ? "▾" : "▸"}
                          </span>
                          <span className="truncate font-mono text-xs text-zinc-200">
                            {isMavenGroup(row)
                              ? row.coordinate
                              : row.coordinate}
                          </span>
                        </div>
                        {!isMavenGroup(row) && row.digest && (
                          <div className="mt-1">
                            <CopyableValue
                              value={row.digest}
                              label={row.digest.slice(0, 20)}
                            />
                          </div>
                        )}
                      </td>
                      <td className="px-4 py-3">
                        <Link
                          to={artifactTarget(representative)}
                          onClick={(event) => event.stopPropagation()}
                          className="text-xs text-cyan-400 hover:text-cyan-300"
                        >
                          {row.repositoryName}
                        </Link>
                      </td>
                      <td className="px-4 py-3">
                        <FormatBadge format={row.format} />
                      </td>
                      <td className="px-4 py-3 text-xs text-zinc-400">
                        {isMavenGroup(row)
                          ? `${row.hits.length} 个版本`
                          : formatBytes(row.size)}
                      </td>
                      <td className="whitespace-nowrap px-4 py-3 text-xs text-zinc-500">
                        {formatDate(representative.createdAt)}
                      </td>
                      <td className="whitespace-nowrap px-3 py-3 text-right">
                        <Space size={2}>
                          <Tooltip title={expanded ? "收起详情" : "展开详情"}>
                            <Button
                              type="text"
                              size="small"
                              aria-label={`${expanded ? "收起" : "展开"} ${row.coordinate}`}
                              icon={
                                expanded ? <UpOutlined /> : <DownOutlined />
                              }
                            />
                          </Tooltip>
                          <Tooltip title="在仓库中精确定位">
                            <Button
                              type="text"
                              size="small"
                              icon={<LinkOutlined />}
                              aria-label={`在仓库中打开 ${row.coordinate}`}
                              onClick={(event) => {
                                event.stopPropagation();
                                openHit(representative);
                              }}
                            />
                          </Tooltip>
                        </Space>
                      </td>
                    </tr>
                    {expanded && (
                      <tr className="bg-zinc-950/45">
                        <td colSpan={6} className="px-5 py-4">
                          {isMavenGroup(row) ? (
                            <div>
                              <div className="mb-3 flex items-center justify-between gap-3">
                                <div>
                                  <div className="text-sm font-medium text-zinc-200">
                                    版本与构建
                                  </div>
                                  <div className="mt-1 text-xs text-zinc-500">
                                    选择版本查看 POM、digest 和发布信息
                                  </div>
                                </div>
                                <Button
                                  size="small"
                                  icon={<LinkOutlined />}
                                  onClick={() => openHit(representative)}
                                >
                                  打开最新版本
                                </Button>
                              </div>
                              <div className="max-h-64 overflow-y-auto rounded-md border border-zinc-800/80">
                                {row.hits.map((hit) => {
                                  const versionKey = `${row.key}:${hit.coordinate}:${hit.buildNumber ?? 0}`;
                                  const selected =
                                    selectedVersion === versionKey;
                                  return (
                                    <div
                                      key={versionKey}
                                      className={`flex items-center justify-between gap-4 border-b border-zinc-800/60 px-3 py-2 last:border-b-0 ${selected ? "bg-cyan-400/10" : ""}`}
                                    >
                                      <button
                                        type="button"
                                        className="min-w-0 flex-1 text-left"
                                        onClick={() =>
                                          setSelectedVersion(versionKey)
                                        }
                                      >
                                        <div className="truncate font-mono text-xs text-zinc-200">
                                          {mavenVersionLabel(hit)}
                                        </div>
                                        <div className="mt-0.5 text-[11px] text-zinc-500">
                                          {formatDate(hit.createdAt)} ·{" "}
                                          {formatBytes(hit.size)} ·{" "}
                                          {hit.publisher ?? "发布者未记录"}
                                        </div>
                                      </button>
                                      <Space size={2}>
                                        <Tooltip
                                          title={hit.digest ?? "暂无 digest"}
                                        >
                                          <span>
                                            {hit.digest ? (
                                              <CopyableValue
                                                value={hit.digest}
                                                label={hit.digest.slice(0, 14)}
                                              />
                                            ) : (
                                              <span className="text-xs text-zinc-600">
                                                无 digest
                                              </span>
                                            )}
                                          </span>
                                        </Tooltip>
                                        <Button
                                          type="text"
                                          size="small"
                                          icon={<LinkOutlined />}
                                          aria-label={`打开 ${hit.coordinate}`}
                                          onClick={() => openHit(hit)}
                                        />
                                      </Space>
                                    </div>
                                  );
                                })}
                              </div>
                              {(() => {
                                const hit =
                                  row.hits.find(
                                    (item) =>
                                      `${row.key}:${item.coordinate}:${item.buildNumber ?? 0}` ===
                                      selectedVersion,
                                  ) ?? row.hits[0];
                                return (
                                  <>
                                    <Descriptions
                                      size="small"
                                      column={4}
                                      className="my-4"
                                      items={[
                                        {
                                          key: "publisher",
                                          label: "发布者",
                                          children: hit.publisher ?? "未记录",
                                        },
                                        {
                                          key: "createdAt",
                                          label: "发布时间",
                                          children: formatDate(hit.createdAt),
                                        },
                                        {
                                          key: "size",
                                          label: "大小",
                                          children: formatBytes(hit.size),
                                        },
                                        {
                                          key: "digest",
                                          label: "Digest",
                                          children: hit.digest ? (
                                            <CopyableValue
                                              value={hit.digest}
                                              label={hit.digest.slice(0, 22)}
                                            />
                                          ) : (
                                            "未记录"
                                          ),
                                        },
                                      ]}
                                    />
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
                                        在仓库中打开此版本
                                      </Button>
                                    </div>
                                  </>
                                );
                              })()}
                            </div>
                          ) : (
                            <>
                              <Descriptions
                                size="small"
                                column={4}
                                className="mb-4"
                                items={[
                                  {
                                    key: "repository",
                                    label: "仓库",
                                    children: row.repositoryName,
                                  },
                                  {
                                    key: "publisher",
                                    label: "发布者",
                                    children: row.publisher ?? "未记录",
                                  },
                                  {
                                    key: "contentType",
                                    label: "内容类型",
                                    children: row.contentType ?? "未记录",
                                  },
                                  {
                                    key: "createdAt",
                                    label: "发布时间",
                                    children: formatDate(row.createdAt),
                                  },
                                ]}
                              />
                              {row.format === "oci" && (
                                <OciImageDetail
                                  repositoryId={row.repositoryId}
                                  repository={row.repositoryName}
                                  image={row.coordinate}
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
                              <div className="mt-4 flex justify-end">
                                <Button
                                  type="primary"
                                  icon={<LinkOutlined />}
                                  onClick={() => openHit(row)}
                                >
                                  在仓库中打开
                                </Button>
                              </div>
                            </>
                          )}
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </DataTable>
          </div>
          {nextPageToken && (
            <div className="mt-4 flex justify-center">
              <Button loading={loadingMore} onClick={() => void loadMore()}>
                加载更多
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
