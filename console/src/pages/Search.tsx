import { Fragment, useEffect, useState } from "react";
import { DownOutlined, LinkOutlined, UpOutlined } from "@ant-design/icons";
import { Button, Descriptions, Input, Space, Tooltip } from "antd";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { searchArtifacts } from "../client";
import type { GlobalArtifactSearchHit } from "../client";
import { PageHeader, DataTable, StatCard } from "../components/Layout";
import { Loading, ErrorBanner, EmptyState } from "../components/Feedback";
import { FormatBadge } from "../components/Badge";
import { formatBytes, formatDate, shortDigest } from "../lib/format";
import {
  MavenArtifactDetail,
  ConanArtifactDetail,
  RawArtifactDetail,
} from "../components/ArtifactRowDetail";
import { OciImageDetail } from "../components/OciImageDetail";

const SEARCH_PAGE_SIZE = 100;

function artifactTarget(hit: GlobalArtifactSearchHit): string {
  const params = new URLSearchParams({ artifact: hit.coordinate });
  if (hit.buildNumber && hit.buildNumber > 0)
    params.set("build", String(hit.buildNumber));
  return `/repositories/${hit.repositoryId}?${params.toString()}`;
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
  const [expandedHit, setExpandedHit] = useState<string | null>(null);

  useEffect(() => {
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

    (async () => {
      const response = await searchArtifacts({
        query: { q, pageSize: SEARCH_PAGE_SIZE },
      });
      if (cancelled) return;
      if (response.error || !response.data) {
        setError(response.error ?? new Error("搜索制品失败"));
      } else {
        setHits(response.data.items);
        setSearchedRepos(response.data.searchedRepositories);
        setNextPageToken(response.data.nextPageToken);
      }
      setLoading(false);
    })();

    return () => {
      cancelled = true;
    };
  }, [q]);

  useEffect(() => setQuery(q), [q]);

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
        className="mb-6 max-w-2xl"
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
          hint="在上方搜索框输入坐标、路径或镜像名前缀后回车"
        />
      ) : error && hits.length === 0 ? (
        <ErrorBanner error={error} />
      ) : loading ? (
        <Loading
          label={
            searchedRepos ? `正在搜索 ${searchedRepos} 个仓库…` : "加载中…"
          }
        />
      ) : hits.length === 0 ? (
        <EmptyState
          title="没有匹配的制品"
          hint={`已在 ${searchedRepos} 个仓库中搜索 “${q}”，未找到结果`}
        />
      ) : (
        <>
          <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-3">
            <StatCard
              label="已加载制品"
              value={hits.length}
              sub="可继续展开或精确定位"
            />
            <StatCard
              label="已检索仓库"
              value={searchedRepos}
              sub="仅包含当前身份可读仓库"
            />
            <StatCard
              label="分页状态"
              value={nextPageToken ? "有更多" : "已完成"}
              sub={
                nextPageToken
                  ? "继续加载不会丢失仓库结果"
                  : "已到达搜索结果末尾"
              }
            />
          </div>
          {error ? (
            <div className="mb-4">
              <ErrorBanner error={error} />
            </div>
          ) : null}
          <DataTable columns={["坐标", "仓库", "格式", "大小", "创建时间", ""]}>
            {hits.map((hit, index) => {
              const rowKey = `${hit.repositoryId}-${hit.coordinate}-${hit.buildNumber ?? 0}-${index}`;
              const expanded = expandedHit === rowKey;
              const target = artifactTarget(hit);
              return (
                <Fragment key={rowKey}>
                  <tr className="hover:bg-zinc-800/30">
                    <td className="px-4 py-2.5">
                      <div className="font-mono text-xs text-zinc-200">
                        {hit.coordinate}
                      </div>
                      {hit.digest && (
                        <div className="mt-0.5 font-mono text-[11px] text-zinc-600">
                          {shortDigest(hit.digest)}
                        </div>
                      )}
                      {hit.buildNumber && hit.buildNumber > 0 ? (
                        <div className="mt-0.5 text-[11px] text-amber-300">
                          Snapshot build #{hit.buildNumber}
                        </div>
                      ) : null}
                    </td>
                    <td className="px-4 py-2.5">
                      <Link
                        to={target}
                        className="text-xs text-cyan-400 hover:text-cyan-300"
                      >
                        {hit.repositoryName}
                      </Link>
                    </td>
                    <td className="px-4 py-2.5">
                      <FormatBadge format={hit.format} />
                    </td>
                    <td className="px-4 py-2.5 text-xs text-zinc-400">
                      {formatBytes(hit.size)}
                    </td>
                    <td className="px-4 py-2.5 text-xs text-zinc-500">
                      {formatDate(hit.createdAt)}
                    </td>
                    <td className="whitespace-nowrap px-3 py-2 text-right">
                      <Space size={2}>
                        <Tooltip title={expanded ? "收起详情" : "展开详情"}>
                          <Button
                            type="text"
                            size="small"
                            aria-label={`${expanded ? "收起" : "展开"} ${hit.coordinate}`}
                            icon={expanded ? <UpOutlined /> : <DownOutlined />}
                            onClick={() =>
                              setExpandedHit(expanded ? null : rowKey)
                            }
                          />
                        </Tooltip>
                        <Tooltip title="在仓库中精确定位">
                          <Button
                            type="text"
                            size="small"
                            icon={<LinkOutlined />}
                            onClick={() => navigate(target)}
                            aria-label={`在仓库中打开 ${hit.coordinate}`}
                          />
                        </Tooltip>
                      </Space>
                    </td>
                  </tr>
                  {expanded && (
                    <tr className="bg-zinc-900/50">
                      <td colSpan={6} className="px-5 py-4">
                        <Descriptions
                          size="small"
                          column={4}
                          className="mb-4"
                          items={[
                            {
                              key: "repository",
                              label: "仓库",
                              children: hit.repositoryName,
                            },
                            {
                              key: "publisher",
                              label: "发布者",
                              children: hit.publisher ?? "未记录",
                            },
                            {
                              key: "contentType",
                              label: "内容类型",
                              children: hit.contentType ?? "未记录",
                            },
                            {
                              key: "createdAt",
                              label: "发布时间",
                              children: formatDate(hit.createdAt),
                            },
                          ]}
                        />
                        {hit.format === "oci" && (
                          <OciImageDetail
                            repositoryId={hit.repositoryId}
                            repository={hit.repositoryName}
                            image={hit.coordinate}
                          />
                        )}
                        {hit.format === "maven" && (
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
                        )}
                        {hit.format === "conan" && (
                          <ConanArtifactDetail
                            repoId={hit.repositoryId}
                            repoName={hit.repositoryName}
                            managed
                            canDelete={false}
                            meta={{
                              coordinate: hit.coordinate,
                              digest: hit.digest,
                              size: hit.size,
                              createdAt: hit.createdAt,
                              publisher: hit.publisher,
                            }}
                          />
                        )}
                        {hit.format === "raw" && (
                          <RawArtifactDetail
                            repoName={hit.repositoryName}
                            meta={{
                              coordinate: hit.coordinate,
                              digest: hit.digest,
                              size: hit.size,
                              contentType: hit.contentType,
                              createdAt: hit.createdAt,
                              publisher: hit.publisher,
                            }}
                          />
                        )}
                        <div className="mt-4 flex justify-end">
                          <Button
                            type="primary"
                            icon={<LinkOutlined />}
                            onClick={() => navigate(target)}
                          >
                            在仓库中打开
                          </Button>
                        </div>
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </DataTable>
          {nextPageToken ? (
            <div className="mt-4 flex justify-center">
              <Button loading={loadingMore} onClick={() => void loadMore()}>
                加载更多
              </Button>
            </div>
          ) : null}
        </>
      )}
    </div>
  );
}
