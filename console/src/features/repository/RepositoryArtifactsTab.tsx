import { useEffect, useState } from "react";
import { FolderOutlined, UnorderedListOutlined } from "@ant-design/icons";
import { Button, Input, Segmented, Select } from "antd";
import type { BrowseNode, Repository } from "../../client";
import { ErrorBanner, Loading, isNotFound } from "../../components/ui/Feedback";
import { Pagination } from "../../components/ui/Layout";
import { RawUploadDialog } from "./RawUploadDialog";
import {
  artifactCoordinateForDisplay,
  formatDescriptor,
  supportsDirectoryBrowse,
} from "../../formats";
import { formatViews } from "../../formats/views";
import { useAuth } from "../../lib/auth";
import { formatNumber } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";
import { RepositoryBrowseTree } from "./RepositoryBrowseTree";
import {
  PROXY_MAVEN_PAGE_SIZE,
  ProxyMavenCacheDetail,
  ProxyMavenUsage,
  type ProxyMavenAssetFilter,
} from "./ProxyMavenCacheSection";
import type { ArtifactRow } from "./artifactRow";
import { artifactColumns } from "./artifactColumns";
import { useArtifactPages } from "./artifactPages";
import { ArtifactsEmptyState } from "./ArtifactsEmptyState";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";

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
  const [view, setView] = useState<"directory" | "list">("list");
  const [proxyAssetFilter, setProxyAssetFilter] =
    useState<ProxyMavenAssetFilter>("primary");
  // The search that is loaded; `q` is what the search box holds.
  const [query, setQuery] = useState(q);
  // Which row is open: the reader's choice for the current answer, otherwise
  // the deep-linked artifact.
  const [expandedChoice, setExpandedChoice] = useState<{
    key: string | null;
    page: unknown;
  } | null>(null);

  const format = repo.format;
  const proxyMaven = format === "maven" && repo.type === "proxy";
  const canUploadRaw =
    canWrite && Boolean(descriptor?.consoleUpload) && repo.type !== "proxy";
  const supportsDirectory = supportsDirectoryBrowse(format, repo.type);

  useEffect(() => {
    setView("list");
  }, [repo.id]);

  const artifacts = useArtifactPages({
    repo,
    proxyMaven,
    query,
    assetFilter: assetTarget ? "all" : proxyAssetFilter,
    artifactTarget,
    buildTarget,
    unavailableMessage: text(
      "读取 Proxy 缓存失败",
      "Failed to load proxy cache",
    ),
  });
  const pages = artifacts.data?.pages;
  const rows = pages?.flatMap((page) => page.items) ?? [];
  const firstPage = pages?.[0];
  const nextToken = pages?.at(-1)?.nextPageToken;
  const proxyTotal = pages?.at(-1)?.totalEstimate ?? 0;
  const proxyPage = pages?.length ?? 1;
  const loading = artifacts.isPending;
  const error = artifacts.error;
  const targetKey =
    artifactTarget && firstPage
      ? (firstPage.items.find(
          (item) =>
            item.coordinate === artifactTarget &&
            (proxyMaven || !buildTarget || item.buildNumber === buildTarget),
        )?.key ?? null)
      : null;
  const expandedImage =
    expandedChoice && expandedChoice.page === firstPage
      ? expandedChoice.key
      : targetKey;
  const setExpandedImage = (key: string | null) =>
    setExpandedChoice({ key, page: firstPage });

  /** Load `value` from its first page, or reload it when it is the loaded search. */
  const load = (value: string) => {
    if (value !== query) {
      setQuery(value);
      return;
    }
    void artifacts.reload();
  };

  // A new deep link replaces both the search box and the loaded search.
  useEffect(() => {
    const targetQuery = proxyMaven
      ? (assetTarget ?? artifactTarget)
      : artifactCoordinateForDisplay(format, artifactTarget);
    setQ(targetQuery);
    setQuery(targetQuery);
  }, [artifactTarget, assetTarget, format, proxyMaven]);

  const searchPlaceholder = descriptor
    ? text(...descriptor.searchPlaceholder)
    : undefined;

  // Proxy Maven rows are cache entries, which carry no security summary.
  const showSecurityColumn =
    descriptor?.securityColumn === "always" ||
    (descriptor?.securityColumn === "versionSearch" &&
      Boolean(q) &&
      !proxyMaven);
  const columns = artifactColumns({
    descriptor,
    repo,
    proxyMaven,
    showSecurityColumn,
    text,
  });

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
        <Input.Search
          allowClear
          className="ag-artifact-view-search"
          placeholder={searchPlaceholder ?? text("搜索…", "Search…")}
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
          <ArtifactsEmptyState searching={Boolean(q)} repo={repo} />
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
            onMore={() => void artifacts.fetchNextPage()}
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
