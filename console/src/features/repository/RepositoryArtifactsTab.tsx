import { useCallback, useEffect, useRef, useState } from "react";
import { FolderOutlined, UnorderedListOutlined } from "@ant-design/icons";
import { Button, Input, Segmented, Select } from "antd";
import type { ColumnsType } from "antd/es/table";
import { listProxyCacheEntries } from "../../client";
import type { ArtifactSearchMatch, BrowseNode, Repository } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { ArtifactSecurityBadge } from "../artifact-detail/ArtifactSecurityBadge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  isNotFound,
} from "../../components/ui/Feedback";
import { Pagination } from "../../components/ui/Layout";
import { RawUploadDialog } from "./RawUploadDialog";
import {
  artifactCoordinateForDisplay,
  formatDescriptor,
  supportsDirectoryBrowse,
} from "../../formats";
import { listArtifactPage, type ArtifactPage } from "../../formats/listers";
import { formatViews } from "../../formats/views";
import { useAuth } from "../../lib/auth";
import {
  formatBytes,
  formatDate,
  formatNumber,
  shortDigest,
} from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { decodeRawPathForDisplay } from "../../lib/rawPath";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";
import { RepositoryBrowseTree } from "./RepositoryBrowseTree";
import {
  PROXY_MAVEN_PAGE_SIZE,
  ProxyMavenCacheDetail,
  ProxyMavenUsage,
  type ProxyMavenAssetFilter,
} from "./ProxyMavenCacheSection";
import type { ArtifactRow } from "./artifactRow";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";

import { ArtifactSearchMatchControl } from "../../components/ui/ArtifactSearchMatchControl";

/** The proxy cache answered without a page; shown as its own message. */
const PROXY_CACHE_UNAVAILABLE = Symbol("proxy-cache-unavailable");

/** One page of a Maven proxy's cache, grouped by version. */
async function listProxyMavenPage(
  repositoryId: string,
  query: string,
  assetFilter: ProxyMavenAssetFilter | "all",
  pageToken?: string,
  signal?: AbortSignal,
): Promise<ArtifactPage & { totalEstimate?: number }> {
  try {
    const { data: pageData, error: requestError } = await listProxyCacheEntries(
      {
        path: { repositoryId },
        signal,
        query: {
          groupBy: "version",
          assetFilter,
          q: query || undefined,
          pageSize: PROXY_MAVEN_PAGE_SIZE,
          pageToken,
        },
      },
    );
    if (requestError || !pageData) {
      return { items: [], error: PROXY_CACHE_UNAVAILABLE };
    }
    return {
      items: pageData.items.map((item) => {
        const primary = item.assets?.filter((asset) => !asset.sidecar) ?? [];
        return {
          key: item.key,
          coordinate: item.coordinate,
          latestVersion: item.version,
          size: item.size,
          contentType: item.extensions?.join(", ") || item.contentType,
          publisher: item.member,
          fileCount: item.assetCount,
          primaryFiles: primary.map((asset) => asset.name),
          files: item.assets,
        } satisfies ArtifactRow;
      }),
      nextPageToken: pageData.nextPageToken,
      totalEstimate: pageData.totalEstimate,
      error: null,
    };
  } catch (error) {
    return { items: [], error };
  }
}

export function RepositoryArtifactsTab({
  repo,
  canWrite,
  canQuarantine = false,
  artifactTarget = "",
  buildTarget,
  assetTarget,
  referenceTarget,
  versionTarget,
  onVersionChange,
  onBrowseArtifact,
}: {
  repo: Repository;
  canWrite: boolean;
  canQuarantine?: boolean;
  artifactTarget?: string;
  buildTarget?: number;
  assetTarget?: string;
  referenceTarget?: string;
  versionTarget?: string;
  onVersionChange?: (coordinate: string, version: string) => void;
  onBrowseArtifact?: (node: BrowseNode) => void;
}) {
  const { token } = useAuth();
  const { text } = usePreferences();
  const descriptor = formatDescriptor(repo.format);
  const [q, setQ] = useState(
    artifactCoordinateForDisplay(repo.format, artifactTarget),
  );
  const [match, setMatch] = useState<ArtifactSearchMatch>(
    artifactTarget ? "prefix" : "contains",
  );
  const matchRef = useRef(match);
  const submittedQuery = useRef(q);
  const requestRef = useRef<AbortController | null>(null);
  const moreInFlight = useRef(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [view, setView] = useState<"directory" | "list">("list");
  const [rows, setRows] = useState<ArtifactRow[]>([]);
  const [nextToken, setNextToken] = useState<string | undefined>();
  const [proxyPage, setProxyPage] = useState(1);
  const [proxyTotal, setProxyTotal] = useState(0);
  const [proxyAssetFilter, setProxyAssetFilter] =
    useState<ProxyMavenAssetFilter>("primary");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [expandedImage, setExpandedImage] = useState<string | null>(null);

  const format = repo.format;
  const proxyMaven = format === "maven" && repo.type === "proxy";
  const canUploadRaw =
    canWrite && Boolean(descriptor?.consoleUpload) && repo.type !== "proxy";
  const emptyHint =
    repo.type === "hosted" || repo.type === "proxy"
      ? descriptor?.emptyHint[repo.type]
      : undefined;
  const supportsDirectory = supportsDirectoryBrowse(format, repo.type);

  useEffect(() => {
    setView("list");
    setRows([]);
    setNextToken(undefined);
    setError(null);
    setExpandedImage(null);
  }, [repo.id, format]);

  const load = useCallback(
    async (query: string, pageToken?: string) => {
      if (pageToken && moreInFlight.current) return;
      requestRef.current?.abort();
      const controller = new AbortController();
      requestRef.current = controller;
      moreInFlight.current = Boolean(pageToken);
      setLoadingMore(Boolean(pageToken));
      try {
        if (!pageToken) {
          submittedQuery.current = query;
          setNextToken(undefined);
          setLoading(true);
          setError(null);
        }
        // One await for either source: the proxy cache or the format's list.
        const result: ArtifactPage & { totalEstimate?: number } =
          await (proxyMaven
            ? listProxyMavenPage(
                repo.id,
                query,
                assetTarget ? "all" : proxyAssetFilter,
                pageToken,
                controller.signal,
              )
            : listArtifactPage(format, {
                repositoryId: repo.id,
                query,
                pageToken,
                pageSize: 50,
                match: matchRef.current,
                signal: controller.signal,
                artifactTarget,
                buildTarget,
              }));
        if (controller.signal.aborted) return;
        const err =
          result.error === PROXY_CACHE_UNAVAILABLE
            ? new Error(
                text("读取 Proxy 缓存失败", "Failed to load proxy cache"),
              )
            : result.error;
        const items = result.items;
        const next = result.nextPageToken;
        if (proxyMaven && !err) {
          setProxyTotal(result.totalEstimate ?? 0);
          setProxyPage((current) => (pageToken ? current + 1 : 1));
        }

        setLoading(false);
        if (err) {
          setError(err);
          return;
        }
        setRows((prev) => (pageToken ? [...prev, ...items] : items));
        setNextToken(next);
        if (!pageToken && artifactTarget) {
          const target = items.find(
            (item) =>
              item.coordinate === artifactTarget &&
              (proxyMaven || !buildTarget || item.buildNumber === buildTarget),
          );
          setExpandedImage(target?.key ?? null);
        }
      } catch (error) {
        if (!controller.signal.aborted) setError(error);
      } finally {
        if (!controller.signal.aborted) {
          moreInFlight.current = false;
          setLoading(false);
          setLoadingMore(false);
        }
      }
    },
    [
      repo.id,
      format,
      proxyMaven,
      proxyAssetFilter,
      artifactTarget,
      buildTarget,
      assetTarget,
      text,
      // A state setter never changes; the React Compiler infers it as a
      // dependency of this callback, and listing it keeps the memoization.
      setExpandedImage,
    ],
  );

  useEffect(() => {
    const targetQuery = proxyMaven
      ? (assetTarget ?? artifactTarget)
      : artifactCoordinateForDisplay(format, artifactTarget);
    const matching = artifactTarget ? "prefix" : "contains";
    matchRef.current = matching;
    setMatch(matching);
    setQ(targetQuery);
    void load(targetQuery);
    return () => requestRef.current?.abort();
  }, [artifactTarget, assetTarget, format, proxyMaven, load]);

  const searchPlaceholder = descriptor
    ? text(...descriptor.searchPlaceholder)
    : undefined;

  // Proxy Maven rows are cache entries, which carry no security summary.
  const showSecurityColumn =
    descriptor?.securityColumn === "always" ||
    (descriptor?.securityColumn === "versionSearch" &&
      Boolean(q) &&
      !proxyMaven);
  const securityColumn: ColumnsType<ArtifactRow>[number] = {
    title: text("安全", "Security"),
    key: "intelligence",
    width: 150,
    render: (_, record) => (
      <ArtifactSecurityBadge summary={record.intelligence} text={text} />
    ),
  };

  const columns: ColumnsType<ArtifactRow> = descriptor?.nameListing
    ? [
        {
          title: text("名称", "Name"),
          dataIndex: "coordinate",
          key: "coordinate",
          ellipsis: true,
          render: (value: string, record) => (
            <span
              className="font-mono text-xs text-fg"
              title={record.coordinate}
            >
              {value}
            </span>
          ),
        },
      ]
    : proxyMaven
      ? [
          {
            title: text("Maven 坐标", "Maven coordinate"),
            dataIndex: "coordinate",
            key: "coordinate",
            ellipsis: true,
            render: (value: string, record) => (
              <span
                className="font-mono text-xs text-fg"
                title={record.coordinate}
              >
                {value}
              </span>
            ),
          },
          {
            title: text("文件", "Files"),
            key: "fileCount",
            width: 120,
            render: (_, record) => (
              <Badge tone="neutral">
                {text(
                  `${record.fileCount ?? record.files?.length ?? 0} 个文件`,
                  `${record.fileCount ?? record.files?.length ?? 0} files`,
                )}
              </Badge>
            ),
          },
          {
            title: text("主文件大小", "Primary size"),
            key: "size",
            width: 140,
            render: (_, record) => (
              <span className="text-xs text-fg-secondary">
                {formatBytes(record.size)}
              </span>
            ),
          },
          {
            title: text("类型", "Type"),
            dataIndex: "contentType",
            key: "contentType",
            width: 180,
            ellipsis: true,
            render: (value: string | undefined) => (
              <span className="text-xs text-fg-tertiary">{value ?? "—"}</span>
            ),
          },
        ]
      : descriptor?.assetColumns
        ? [
            {
              title: text("APT 路径", "APT path"),
              dataIndex: "coordinate",
              key: "coordinate",
              ellipsis: true,
              render: (value: string, record: ArtifactRow) => (
                <span
                  className="font-mono text-xs text-fg"
                  title={record.coordinate}
                >
                  {value}
                </span>
              ),
            },
            {
              title: text("内容类型", "Content type"),
              dataIndex: "contentType",
              key: "contentType",
              width: 220,
              ellipsis: true,
              render: (value: string | undefined) => (
                <span className="text-xs text-fg-tertiary">{value ?? "—"}</span>
              ),
            },
            {
              title: text("大小", "Size"),
              dataIndex: "size",
              key: "size",
              width: 120,
              render: (value: number | undefined) => (
                <span className="text-xs text-fg-secondary">
                  {formatBytes(value)}
                </span>
              ),
            },
            {
              title: text("摘要", "Digest"),
              dataIndex: "digest",
              key: "digest",
              width: 180,
              ellipsis: true,
              render: (value: string | undefined) => (
                <span
                  className="font-mono text-xs text-fg-tertiary"
                  title={value}
                >
                  {shortDigest(value)}
                </span>
              ),
            },
            {
              title:
                repo.type === "proxy"
                  ? text("首次缓存", "First cached")
                  : text("已发布", "Published"),
              dataIndex: "createdAt",
              key: "createdAt",
              width: 180,
              render: (value: string | undefined) => (
                <span className="whitespace-nowrap text-xs text-fg-tertiary">
                  {formatDate(value)}
                </span>
              ),
            },
          ]
        : descriptor?.versionColumns
          ? [
              {
                title: text(...descriptor.artifactColumn),
                dataIndex: "coordinate",
                key: "coordinate",
                ellipsis: true,
                render: (value: string, record) => (
                  <span
                    className="font-mono text-xs text-fg"
                    title={record.coordinate}
                  >
                    {value}
                  </span>
                ),
              },
              {
                title: text("版本", "Versions"),
                key: "versionCount",
                width: 120,
                render: (_, record) => (
                  <Badge tone="neutral">
                    {record.versionCount !== undefined
                      ? text(
                          `${record.versionCount} 个版本`,
                          `${record.versionCount} versions`,
                        )
                      : text("展开查看", "Open to inspect")}
                  </Badge>
                ),
              },
              {
                title: text("最新版本", "Latest version"),
                dataIndex: "latestVersion",
                key: "latestVersion",
                width: 160,
                render: (value: string | undefined) => (
                  <span className="font-mono text-xs text-[var(--ag-content-secondary)]">
                    {value ?? "—"}
                  </span>
                ),
              },
              {
                title: text("更新时间", "Updated"),
                dataIndex: "createdAt",
                key: "createdAt",
                width: 180,
                render: (value: string | undefined) => (
                  <span className="whitespace-nowrap text-xs text-fg-tertiary">
                    {formatDate(value)}
                  </span>
                ),
              },
              ...(showSecurityColumn ? [securityColumn] : []),
            ]
          : [
              {
                title: text("文件路径", "File path"),
                dataIndex: "coordinate",
                key: "coordinate",
                ellipsis: true,
                render: (value: string) => {
                  const displayPath = decodeRawPathForDisplay(value);
                  return (
                    <span
                      className="font-mono text-xs text-fg"
                      title={displayPath}
                    >
                      {displayPath}
                    </span>
                  );
                },
              },
              {
                title: text("摘要", "Digest"),
                dataIndex: "digest",
                key: "digest",
                width: 180,
                ellipsis: true,
                render: (value: string | undefined) => (
                  <span
                    className="font-mono text-xs text-fg-tertiary"
                    title={value}
                  >
                    {shortDigest(value)}
                  </span>
                ),
              },
              {
                title: text("大小", "Size"),
                dataIndex: "size",
                key: "size",
                width: 120,
                render: (value: number | undefined) => (
                  <span className="text-xs text-fg-secondary">
                    {formatBytes(value)}
                  </span>
                ),
              },
              {
                title: text("最后更新时间", "Last updated"),
                dataIndex: "createdAt",
                key: "createdAt",
                width: 180,
                render: (value: string | undefined) => (
                  <span className="whitespace-nowrap text-xs text-fg-tertiary">
                    {formatDate(value)}
                  </span>
                ),
              },
              ...(showSecurityColumn ? [securityColumn] : []),
            ];

  const FormatDetail = formatViews(format).ArtifactDetail;
  const expandedRowRender = (r: ArtifactRow) => {
    // Proxy Maven lists cache entries, which have their own detail.
    if (proxyMaven) {
      return <ProxyMavenCacheDetail repoName={repo.name} meta={r} />;
    }
    if (!FormatDetail) return null;
    return (
      <FormatDetail
        repositoryId={repo.id}
        repositoryName={repo.name}
        repositoryType={repo.type === "proxy" ? "proxy" : "hosted"}
        artifact={r}
        initialVersion={
          artifactTarget === r.coordinate ? versionTarget : undefined
        }
        initialReference={referenceTarget}
        managed={repo.type !== "proxy"}
        canDelete={canWrite && repo.type === "hosted"}
        canQuarantine={canQuarantine}
        onDeleted={() => void load(q)}
        onVersionChange={(version) => onVersionChange?.(r.coordinate, version)}
      />
    );
  };

  if (!descriptor) {
    return (
      <RepositoryFeatureUnavailable
        feature={text("制品浏览", "Artifact browser")}
        unsupportedFormat={format}
      />
    );
  }

  if (view === "directory" && supportsDirectory) {
    return (
      <div className="ag-artifact-browser">
        <div className="ag-artifact-view-toolbar">
          <Segmented<"directory" | "list">
            aria-label={text("制品浏览视图", "Artifact browse view")}
            options={[
              {
                value: "directory",
                label: text("目录", "Directory"),
                icon: <FolderOutlined />,
              },
              {
                value: "list",
                label: text("列表", "List"),
                icon: <UnorderedListOutlined />,
              },
            ]}
            value={view}
            onChange={setView}
          />
          {canUploadRaw && (
            <RawUploadDialog repo={repo} onUploaded={() => void load(q)} />
          )}
        </div>
        <RepositoryBrowseTree
          repo={repo}
          onOpenInList={(node) => {
            const coordinate = node.coordinate ?? node.path ?? "";
            setView("list");
            if (onBrowseArtifact) {
              onBrowseArtifact(node);
              return;
            }
            const query = descriptor?.encodedPaths
              ? artifactCoordinateForDisplay(format, coordinate)
              : (node.path ?? coordinate);
            setView("list");
            setQ(query);
            setExpandedImage(null);
            void load(query);
          }}
        />
      </div>
    );
  }

  return (
    <div className="ag-artifact-browser">
      <div className="ag-artifact-view-toolbar">
        {supportsDirectory && (
          <Segmented<"directory" | "list">
            aria-label={text("制品浏览视图", "Artifact browse view")}
            options={[
              {
                value: "directory",
                label: text("目录", "Directory"),
                icon: <FolderOutlined />,
              },
              {
                value: "list",
                label: text("列表", "List"),
                icon: <UnorderedListOutlined />,
              },
            ]}
            value={view}
            onChange={setView}
          />
        )}
        {!proxyMaven && descriptor?.serverKeywordSearch && (
          <ArtifactSearchMatchControl
            value={match}
            onChange={(value) => {
              matchRef.current = value;
              setMatch(value);
              setExpandedImage(null);
              void load(q);
            }}
          />
        )}
        <Input.Search
          loading={loading}
          allowClear
          className="ag-artifact-view-search"
          placeholder={
            descriptor?.serverKeywordSearch &&
            match === "contains" &&
            !proxyMaven
              ? searchPlaceholder
                  ?.replace("前缀", "关键词")
                  .replace("prefix", "keyword")
              : (searchPlaceholder ?? text("搜索…", "Search…"))
          }
          value={q}
          onChange={(e) => {
            const value = e.target.value;
            setQ(value);
            if (!value) {
              setExpandedImage(null);
              void load("");
            }
          }}
          onSearch={(value) => {
            setQ(value);
            setExpandedImage(null);
            void load(value);
          }}
          enterButton={text("搜索", "Search")}
        />
        {canUploadRaw && (
          <RawUploadDialog repo={repo} onUploaded={() => load(q)} />
        )}
        {proxyMaven && (
          <>
            <Select
              className="w-28"
              value={proxyAssetFilter}
              options={[
                { value: "primary", label: text("主资产", "Primary assets") },
                { value: "all", label: text("全部文件", "All files") },
                { value: "jar", label: text("仅 JAR", "JAR only") },
                { value: "pom", label: text("仅 POM", "POM only") },
              ]}
              onChange={(value: ProxyMavenAssetFilter) => {
                setProxyAssetFilter(value);
                setExpandedImage(null);
              }}
            />
            <span className="text-xs text-fg-tertiary">
              {text(
                `${formatNumber(proxyTotal)} 个 Maven 版本，当前显示 ${formatNumber(rows.length)} 个`,
                `${formatNumber(proxyTotal)} Maven versions, showing ${formatNumber(rows.length)}`,
              )}
            </span>
          </>
        )}
        {q && (
          <Button
            type="text"
            size="small"
            onClick={() => {
              setQ("");
              setExpandedImage(null);
              void load("");
            }}
          >
            {text("返回完整列表", "Return to full list")}
          </Button>
        )}
      </div>
      {error !== null && rows.length > 0 && (
        <ErrorBanner error={error} onRetry={() => load(q)} />
      )}
      {error !== null && rows.length === 0 ? (
        isNotFound(error) ? (
          <RepositoryFeatureUnavailable
            feature={text("制品浏览", "Artifact browser")}
          />
        ) : (
          <ErrorBanner error={error} onRetry={() => load(q)} />
        )
      ) : loading && rows.length === 0 ? (
        <Loading />
      ) : rows.length === 0 ? (
        <>
          {proxyMaven && (
            <ProxyMavenUsage
              repoId={repo.id}
              repoName={repo.name}
              token={token}
              onWarmed={() => void load(q)}
            />
          )}
          <EmptyState
            title={
              q
                ? text("没有匹配的制品", "No matching artifacts")
                : text("暂无制品", "No artifacts")
            }
            hint={
              q
                ? text("换个关键词试试", "Try a different search term")
                : emptyHint
                  ? text(...emptyHint)
                  : text(
                      `通过 ${format} 客户端推送制品后会显示在这里`,
                      "Push artifacts with the matching client to display them here.",
                    )
            }
          />
        </>
      ) : (
        <>
          {proxyMaven && (
            <ProxyMavenUsage
              repoId={repo.id}
              repoName={repo.name}
              token={token}
              onWarmed={() => void load(q)}
            />
          )}
          <ConsoleTable<ArtifactRow>
            rowKey="key"
            dataSource={rows}
            columns={columns}
            pagination={false}
            scroll={{
              x: descriptor?.nameListing ? 520 : 980,
              y: "calc(100vh - 350px)",
            }}
            expandable={{
              expandedRowKeys: expandedImage ? [expandedImage] : [],
              expandedRowRender,
              expandRowByClick: true,
              onExpand: (expanded, record) =>
                setExpandedImage(expanded ? record.key : null),
            }}
          />
          <Pagination
            hasMore={!!nextToken}
            loading={loadingMore}
            disabled={loading}
            onMore={() => load(submittedQuery.current, nextToken)}
          />
          {proxyMaven && proxyTotal > 0 && (
            <div className="border-t border-line px-4 py-2 text-center text-xs text-fg-tertiary">
              {text(
                `第 ${formatNumber(proxyPage)} 页，每页 ${formatNumber(PROXY_MAVEN_PAGE_SIZE)} 个 Maven 版本`,
                `Page ${formatNumber(proxyPage)} with ${formatNumber(PROXY_MAVEN_PAGE_SIZE)} Maven versions per page`,
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
}
