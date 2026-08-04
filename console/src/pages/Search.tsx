import { Fragment, useEffect, useState } from "react";
import { DownOutlined, LinkOutlined, UpOutlined } from "@ant-design/icons";
import { Button, Descriptions, Input, Space, Tooltip } from "antd";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { listRepositories, searchRepositoryArtifacts } from "../client";
import type { Repository } from "../client";
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

interface GlobalHit {
  repositoryId: string;
  repositoryName: string;
  format: Repository["format"];
  coordinate: string;
  digest?: string;
  size?: number;
  contentType?: string;
  createdAt?: string;
  buildNumber?: number;
  publisher?: string;
}

const PER_REPO_LIMIT = 25;

async function loadAllRepositories(): Promise<Repository[]> {
  const out: Repository[] = [];
  let pageToken: string | undefined;
  do {
    const r = await listRepositories({ query: { pageSize: 100, pageToken } });
    if (r.error || !r.data) throw r.error ?? new Error("加载仓库列表失败");
    out.push(...r.data.items);
    pageToken = r.data.nextPageToken;
  } while (pageToken);
  return out;
}

// A repository that errors (search unsupported, not enabled, or denied) simply
// contributes no hits rather than failing the whole search.
async function searchRepository(
  repo: Repository,
  q: string,
): Promise<GlobalHit[]> {
  const r = await searchRepositoryArtifacts({
    path: { repositoryId: repo.id },
    query: { q, pageSize: PER_REPO_LIMIT },
  });
  if (r.error || !r.data) return [];
  return (r.data.items ?? []).map((x) => ({
    repositoryId: repo.id,
    repositoryName: repo.name,
    format: repo.format,
    coordinate: x.coordinate,
    digest: x.digest,
    size: x.size,
    contentType: x.contentType,
    createdAt: x.createdAt,
    buildNumber: x.buildNumber,
    publisher: x.publisher,
  }));
}

function artifactTarget(hit: GlobalHit): string {
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

  const [hits, setHits] = useState<GlobalHit[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [searchedRepos, setSearchedRepos] = useState(0);
  const [maybeTruncated, setMaybeTruncated] = useState(0);
  const [expandedHit, setExpandedHit] = useState<string | null>(null);

  useEffect(() => {
    if (!q) {
      setHits([]);
      setLoading(false);
      setError(null);
      setSearchedRepos(0);
      setMaybeTruncated(0);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    setHits([]);
    setSearchedRepos(0);
    setMaybeTruncated(0);

    (async () => {
      try {
        const repos = await loadAllRepositories();
        if (cancelled) return;
        setSearchedRepos(repos.length);
        const perRepo = await Promise.all(
          repos.map((repo) => searchRepository(repo, q)),
        );
        if (cancelled) return;
        const flat = perRepo.flat();
        setMaybeTruncated(
          perRepo.filter((rows) => rows.length >= PER_REPO_LIMIT).length,
        );
        flat.sort((a, b) =>
          (b.createdAt ?? "").localeCompare(a.createdAt ?? ""),
        );
        setHits(flat);
      } catch (e) {
        if (!cancelled) setError(e);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [q]);

  useEffect(() => setQuery(q), [q]);

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
      ) : error ? (
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
              label="命中制品"
              value={hits.length}
              sub="按更新时间排序"
            />
            <StatCard
              label="已检索仓库"
              value={searchedRepos}
              sub="跨仓库并行搜索"
            />
            <StatCard
              label="可能截断"
              value={maybeTruncated}
              sub={
                maybeTruncated
                  ? `每个仓库最多显示 ${PER_REPO_LIMIT} 条`
                  : "当前结果完整"
              }
            />
          </div>
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
        </>
      )}
    </div>
  );
}
