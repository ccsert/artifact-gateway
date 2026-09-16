import { useCallback, useEffect, useState } from "react";
import { FolderOutlined, UnorderedListOutlined } from "@ant-design/icons";
import { Button, Input, Segmented, Select, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  listConanReferences,
  listMavenCoordinates,
  listOciImages,
  listProxyCacheEntries,
  searchRepositoryArtifacts,
} from "../../client";
import type { BrowseNode, Repository } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { ArtifactSecurityBadge } from "../../features/artifact-detail/ArtifactSecurityBadge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  isNotFound,
} from "../../components/ui/Feedback";
import { Pagination } from "../../components/ui/Layout";
import { OciImageDetail } from "../../features/artifact-detail/OciImageDetail";
import {
  ConanArtifactDetail,
  MavenArtifactDetail,
  RawArtifactDetail,
} from "../../features/artifact-detail/ArtifactRowDetail";
import { NpmPackageDetail } from "../../features/artifact-detail/NpmPackageDetail";
import { PyPIProjectDetail } from "../../features/artifact-detail/PyPIProjectDetail";
import { GoModuleDetail } from "../../features/artifact-detail/GoModuleDetail";
import { APTAssetDetail } from "../../features/artifact-detail/APTAssetDetail";
import { RawUploadDialog } from "../../components/RawUploadDialog";
import { useAuth } from "../../lib/auth";
import {
  formatBytes,
  formatDate,
  formatNumber,
  shortDigest,
} from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import {
  canonicalRawSearchPrefix,
  decodeRawPathForDisplay,
} from "../../lib/rawPath";
import { mavenGA, mavenVersion } from "../../lib/usage";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";
import { RepositoryBrowseTree } from "./RepositoryBrowseTree";
import {
  PROXY_MAVEN_PAGE_SIZE,
  ProxyMavenCacheDetail,
  ProxyMavenUsage,
  type ProxyMavenAssetFilter,
} from "./ProxyMavenCacheSection";
import type { ArtifactRow } from "./artifactRow";


async function fetchMavenArtifactPage(
  repositoryId: string,
  query: string,
  pageToken?: string,
): Promise<{
  items: ArtifactRow[];
  nextPageToken?: string;
  error: unknown;
}> {
  const response = await searchRepositoryArtifacts({
    path: { repositoryId },
    query: { q: query, pageSize: 50, pageToken },
  });
  return {
    items: (response.data?.items ?? []).map((item) => ({
      key: `${item.coordinate}-${item.buildNumber ?? 0}`,
      coordinate: item.coordinate,
      digest: item.digest,
      createdAt: item.createdAt,
      size: item.size,
      contentType: item.contentType,
      publisher: item.publisher,
      buildNumber: item.buildNumber,
      intelligence: item.intelligence,
    })),
    nextPageToken: response.data?.nextPageToken,
    error: response.error,
  };
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
  const [q, setQ] = useState(
    repo.format === "raw"
      ? decodeRawPathForDisplay(artifactTarget)
      : artifactTarget,
  );
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
  const proxyNpm = format === "npm" && repo.type === "proxy";
  const proxyPyPI = format === "pypi" && repo.type === "proxy";
  const proxyGo = format === "go" && repo.type === "proxy";
  const proxyAPT = format === "apt" && repo.type === "proxy";
  const hostedAPT = format === "apt" && repo.type === "hosted";
  const canUploadRaw = format === "raw" && repo.type !== "proxy";
  const supportsDirectory =
    (repo.type === "hosted" || repo.type === "proxy") &&
    (format === "maven" || format === "raw");

  useEffect(() => {
    setView("list");
  }, [repo.id]);

  const load = useCallback(
    async (query: string, pageToken?: string) => {
      if (!pageToken) {
        setLoading(true);
        setError(null);
      }
      const page = { pageSize: 50, pageToken };
      let items: ArtifactRow[] = [];
      let next: string | undefined;
      let err: unknown = null;

      if (format === "oci") {
        const r = await listOciImages({
          path: { repositoryId: repo.id },
          query: { q: query || undefined, ...page },
        });
        err = r.error;
        items = (r.data?.items ?? []).map((x) => ({
          key: x.name,
          coordinate: x.name,
        }));
        next = r.data?.nextPageToken;
      } else if (format === "maven") {
        if (proxyMaven) {
          try {
            const { data: pageData, error: requestError } =
              await listProxyCacheEntries({
                path: { repositoryId: repo.id },
                query: {
                  groupBy: "version",
                  assetFilter: assetTarget ? "all" : proxyAssetFilter,
                  q: query || undefined,
                  pageSize: PROXY_MAVEN_PAGE_SIZE,
                  pageToken,
                },
              });
            if (requestError || !pageData)
              throw new Error(
                text("读取 Proxy 缓存失败", "Failed to load proxy cache"),
              );
            setProxyTotal(pageData.totalEstimate);
            items = pageData.items.map((item) => {
              const primary =
                item.assets?.filter((asset) => !asset.sidecar) ?? [];
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
            });
            next = pageData.nextPageToken;
            setProxyPage((current) => (pageToken ? current + 1 : 1));
          } catch (e) {
            err = e;
          }
        } else if (query) {
          let result = await fetchMavenArtifactPage(repo.id, query, pageToken);
          err = result.error;
          items = result.items;
          next = result.nextPageToken;
          if (!pageToken && artifactTarget && buildTarget && !err) {
            let target = items.find(
              (item) =>
                item.coordinate === artifactTarget &&
                item.buildNumber === buildTarget,
            );
            while (!target && next) {
              result = await fetchMavenArtifactPage(repo.id, query, next);
              if (result.error) {
                err = result.error;
                break;
              }
              target = result.items.find(
                (item) =>
                  item.coordinate === artifactTarget &&
                  item.buildNumber === buildTarget,
              );
              next = result.nextPageToken;
            }
            if (target) {
              items = [target];
              next = undefined;
            }
          }
        } else {
          const r = await listMavenCoordinates({
            path: { repositoryId: repo.id },
            query: page,
          });
          err = r.error;
          // 按 group:artifact 聚合：每个制品一行，显示版本数与最新版本
          const byGA = new Map<
            string,
            {
              versions: {
                coordinate: string;
                digest?: string;
                createdAt?: string;
                publisher?: string;
              }[];
            }
          >();
          for (const x of r.data?.items ?? []) {
            const ga = mavenGA(x.coordinate) ?? x.coordinate;
            if (!byGA.has(ga)) byGA.set(ga, { versions: [] });
            byGA.get(ga)!.versions.push(x);
          }
          items = Array.from(byGA.entries()).map(([ga, { versions }]) => {
            const sorted = [...versions].sort((a, b) =>
              (b.createdAt ?? "").localeCompare(a.createdAt ?? ""),
            );
            const latest = sorted[0];
            return {
              key: ga,
              coordinate: ga,
              digest: latest?.digest,
              createdAt: latest?.createdAt,
              publisher: latest?.publisher,
              versionCount: versions.length,
              latestVersion:
                mavenVersion(latest?.coordinate ?? "") ?? undefined,
            };
          });
          next = r.data?.nextPageToken;
        }
      } else if (format === "npm" || format === "pypi" || format === "go") {
        const r = await searchRepositoryArtifacts({
          path: { repositoryId: repo.id },
          query: { q: query || undefined, ...page },
        });
        err = r.error;
        items = (r.data?.items ?? []).map((item) => ({
          key: item.coordinate,
          coordinate: item.coordinate,
          digest: item.digest,
          createdAt: item.createdAt,
          size: item.size,
          publisher: item.publisher,
          intelligence: item.intelligence,
          versionCount: item.versionCount,
          latestVersion: item.version,
        }));
        next = r.data?.nextPageToken;
      } else if (format === "conan") {
        const r = await listConanReferences({
          path: { repositoryId: repo.id },
          query: page,
        });
        err = r.error;
        items = (r.data?.items ?? [])
          .filter((x) => !query || x.reference.includes(query))
          .map((x) => ({
            key: x.reference,
            coordinate: x.reference,
            publisher: x.publisher,
          }));
        next = r.data?.nextPageToken;
      } else if (format === "raw" || format === "apt") {
        const r = await searchRepositoryArtifacts({
          path: { repositoryId: repo.id },
          query: {
            q:
              (format === "raw" ? canonicalRawSearchPrefix(query) : query) ||
              undefined,
            ...page,
          },
        });
        err = r.error;
        items = (r.data?.items ?? []).map((x, i) => ({
          key: `${x.coordinate}-${i}`,
          coordinate: x.coordinate,
          digest: x.digest,
          createdAt: x.createdAt,
          size: x.size,
          contentType: x.contentType,
          cachedAt: x.cachedAt,
          sourceUrl: x.sourceUrl,
          intelligence: x.intelligence,
        }));
        next = r.data?.nextPageToken;
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
    ],
  );

  useEffect(() => {
    const targetQuery =
      format === "raw"
        ? decodeRawPathForDisplay(artifactTarget)
        : proxyMaven
          ? (assetTarget ?? artifactTarget)
          : artifactTarget;
    setQ(targetQuery);
    void load(targetQuery);
  }, [artifactTarget, assetTarget, format, proxyMaven, load]);

  const searchPlaceholder: Record<string, string> = {
    oci: text("按镜像名前缀过滤…", "Filter by image name prefix…"),
    maven: text("搜索 GAV 坐标…", "Search GAV coordinates…"),
    conan: text("按引用名过滤…", "Filter by reference…"),
    npm: text("按包名前缀过滤…", "Filter by package name prefix…"),
    pypi: text("按项目名前缀过滤…", "Filter by project name prefix…"),
    go: text("按模块路径前缀过滤…", "Filter by module path prefix…"),
    apt: text(
      "按 dists/ 或 pool/ 路径过滤…",
      "Filter by dists/ or pool/ path…",
    ),
    raw: text("搜索路径…", "Search paths…"),
  };

  const showSecurityColumn =
    format === "npm" ||
    format === "pypi" ||
    format === "go" ||
    format === "raw" ||
    (format === "maven" && Boolean(q) && !proxyMaven);
  const securityColumn: ColumnsType<ArtifactRow>[number] = {
    title: text("安全", "Security"),
    key: "intelligence",
    width: 150,
    render: (_, record) => (
      <ArtifactSecurityBadge summary={record.intelligence} text={text} />
    ),
  };

  const columns: ColumnsType<ArtifactRow> =
    format === "oci" || format === "conan"
      ? [
          {
            title: text("名称", "Name"),
            dataIndex: "coordinate",
            key: "coordinate",
            ellipsis: true,
            render: (value: string, record) => (
              <span
                className="font-mono text-xs text-zinc-200"
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
                  className="font-mono text-xs text-zinc-200"
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
                <span className="text-xs text-zinc-400">
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
                <span className="text-xs text-zinc-500">{value ?? "—"}</span>
              ),
            },
          ]
        : format === "apt"
          ? [
              {
                title: text("APT 路径", "APT path"),
                dataIndex: "coordinate",
                key: "coordinate",
                ellipsis: true,
                render: (value: string, record: ArtifactRow) => (
                  <span
                    className="font-mono text-xs text-zinc-200"
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
                  <span className="text-xs text-zinc-500">{value ?? "—"}</span>
                ),
              },
              {
                title: text("大小", "Size"),
                dataIndex: "size",
                key: "size",
                width: 120,
                render: (value: number | undefined) => (
                  <span className="text-xs text-zinc-400">
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
                    className="font-mono text-xs text-zinc-500"
                    title={value}
                  >
                    {shortDigest(value)}
                  </span>
                ),
              },
              {
                title: proxyAPT
                  ? text("首次缓存", "First cached")
                  : text("已发布", "Published"),
                dataIndex: "createdAt",
                key: "createdAt",
                width: 180,
                render: (value: string | undefined) => (
                  <span className="whitespace-nowrap text-xs text-zinc-500">
                    {formatDate(value)}
                  </span>
                ),
              },
            ]
          : format === "maven" ||
              format === "npm" ||
              format === "pypi" ||
              format === "go"
            ? [
                {
                  title:
                    format === "npm"
                      ? text("包", "Package")
                      : format === "pypi"
                        ? text("项目", "Project")
                        : format === "go"
                          ? text("模块", "Module")
                          : text("制品", "Artifact"),
                  dataIndex: "coordinate",
                  key: "coordinate",
                  ellipsis: true,
                  render: (value: string, record) => (
                    <span
                      className="font-mono text-xs text-zinc-200"
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
                    <span className="whitespace-nowrap text-xs text-zinc-500">
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
                        className="font-mono text-xs text-zinc-200"
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
                      className="font-mono text-xs text-zinc-500"
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
                    <span className="text-xs text-zinc-400">
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
                    <span className="whitespace-nowrap text-xs text-zinc-500">
                      {formatDate(value)}
                    </span>
                  ),
                },
                ...(showSecurityColumn ? [securityColumn] : []),
              ];

  const expandedRowRender = (r: ArtifactRow) => {
    if (format === "oci") {
      return (
        <OciImageDetail
          repositoryId={repo.id}
          repository={repo.name}
          image={r.coordinate}
          initialReference={referenceTarget}
          canQuarantine={canQuarantine}
          onDeleted={() => void load(q)}
        />
      );
    }
    if (format === "maven" && !proxyMaven) {
      return (
        <MavenArtifactDetail
          repoId={repo.id}
          repoName={repo.name}
          canQuarantine={canQuarantine}
          onDeleted={() => void load(q)}
          meta={{
            coordinate: r.coordinate,
            digest: r.digest,
            size: r.size,
            createdAt: r.createdAt,
            publisher: r.publisher,
            buildNumber: r.buildNumber,
          }}
        />
      );
    }
    if (proxyMaven) {
      return <ProxyMavenCacheDetail repoName={repo.name} meta={r} />;
    }
    if (format === "npm") {
      return (
        <NpmPackageDetail
          repositoryId={repo.id}
          repoName={repo.name}
          packageName={r.coordinate}
          initialVersion={
            artifactTarget === r.coordinate ? versionTarget : undefined
          }
          size={r.size}
          publisher={r.publisher}
          canQuarantine={canQuarantine}
          onVersionChange={(version) =>
            onVersionChange?.(r.coordinate, version)
          }
        />
      );
    }
    if (format === "pypi") {
      return (
        <PyPIProjectDetail
          repositoryId={repo.id}
          repoName={repo.name}
          project={r.coordinate}
          initialVersion={
            artifactTarget === r.coordinate ? versionTarget : undefined
          }
          size={r.size}
          publisher={r.publisher}
          canQuarantine={canQuarantine}
          onVersionChange={(version) =>
            onVersionChange?.(r.coordinate, version)
          }
        />
      );
    }
    if (format === "go") {
      return (
        <GoModuleDetail
          repositoryId={repo.id}
          repoName={repo.name}
          modulePath={r.coordinate}
          initialVersion={
            artifactTarget === r.coordinate ? versionTarget : undefined
          }
          size={r.size}
          publisher={r.publisher}
          canQuarantine={canQuarantine}
          onVersionChange={(version) =>
            onVersionChange?.(r.coordinate, version)
          }
        />
      );
    }
    if (format === "conan") {
      return (
        <ConanArtifactDetail
          repoId={repo.id}
          repoName={repo.name}
          managed={repo.type !== "proxy"}
          canDelete={repo.type !== "proxy" && canWrite}
          canQuarantine={canQuarantine}
          onDeleted={() => void load(q)}
          meta={{ coordinate: r.coordinate, publisher: r.publisher }}
        />
      );
    }
    if (format === "apt") {
      return (
        <APTAssetDetail
          repositoryId={repo.id}
          repoName={repo.name}
          canQuarantine={canQuarantine}
          published={hostedAPT}
          meta={{
            coordinate: r.coordinate,
            digest: r.digest,
            size: r.size,
            contentType: r.contentType,
            createdAt: r.createdAt,
            cachedAt: r.cachedAt,
            sourceUrl: r.sourceUrl,
          }}
        />
      );
    }
    return (
      <RawArtifactDetail
        canDelete={canWrite && repo.type === "hosted"}
        repositoryId={repo.id}
        repoName={repo.name}
        canQuarantine={canQuarantine}
        onDeleted={() => void load(q)}
        meta={{
          coordinate: r.coordinate,
          digest: r.digest,
          size: r.size,
          contentType: r.contentType,
          createdAt: r.createdAt,
        }}
      />
    );
  };

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
            const query =
              format === "raw"
                ? decodeRawPathForDisplay(coordinate)
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
        <Input.Search
          allowClear
          className="ag-artifact-view-search"
          placeholder={searchPlaceholder[format] ?? text("搜索…", "Search…")}
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
            <span className="text-xs text-zinc-500">
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
      {error !== null ? (
        isNotFound(error) ? (
          <RepositoryFeatureUnavailable
            feature={text("制品浏览", "Artifact browser")}
          />
        ) : (
          <ErrorBanner error={error} onRetry={() => load(q)} />
        )
      ) : loading ? (
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
                : proxyMaven
                  ? text(
                      "通过 Maven 客户端拉取依赖后会显示代理缓存",
                      "The proxy cache appears after a Maven client retrieves dependencies.",
                    )
                  : proxyNpm
                    ? text(
                        "通过 npm install 拉取包后会显示上游元数据与缓存状态",
                        "Upstream metadata and cache status appear after npm install retrieves a package.",
                      )
                    : proxyPyPI
                      ? text(
                          "通过 pip install 拉取项目后会显示上游文件与缓存状态",
                          "Upstream files and cache status appear after pip install retrieves a project.",
                        )
                      : proxyGo
                        ? text(
                            "通过 go mod download 拉取模块后会显示上游版本与缓存资产",
                            "Upstream versions and cached assets appear after go mod download retrieves a module.",
                          )
                        : proxyAPT
                          ? text(
                              "通过 apt update、apt install 或直接请求 APT 路径后会显示代理缓存",
                              "The proxy cache appears after apt update, apt install, or a direct APT path request.",
                            )
                          : hostedAPT
                            ? text(
                                "通过发布会话上传 .deb，并提交签名快照后会显示当前可见版本",
                                "Upload .deb files through publication sessions and publish a signed snapshot to display the current visible version.",
                              )
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
          <Table<ArtifactRow>
            className="ag-console-table"
            rowKey="key"
            size="middle"
            dataSource={rows}
            columns={columns}
            pagination={false}
            scroll={{
              x: format === "oci" || format === "conan" ? 520 : 980,
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
          <Pagination hasMore={!!nextToken} onMore={() => load(q, nextToken)} />
          {proxyMaven && proxyTotal > 0 && (
            <div className="border-t border-zinc-800/60 px-4 py-2 text-center text-xs text-zinc-500">
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
