import { useEffect, useState } from "react";
import { Button, Input, Select } from "antd";
import type { ArtifactSearchMatch, BrowseNode, Repository } from "../../client";
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
import { ArtifactViewSwitch } from "./ArtifactViewSwitch";
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
import { ArtifactSearchMatchControl } from "../../components/ui/ArtifactSearchMatchControl";

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
  const { t } = usePreferences();
  const descriptor = formatDescriptor(repo.format);
  const [q, setQ] = useState(
    artifactCoordinateForDisplay(repo.format, artifactTarget),
  );
  const [view, setView] = useState<"directory" | "list">("list");
  const [proxyAssetFilter, setProxyAssetFilter] =
    useState<ProxyMavenAssetFilter>("primary");
  // The search that is loaded; `q` is what the search box holds.
  const [query, setQuery] = useState(q);
  // A deep link names an exact artifact, so it searches by prefix.
  const [match, setMatch] = useState<ArtifactSearchMatch>(
    artifactTarget ? "prefix" : "contains",
  );
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
    match,
    assetFilter: assetTarget ? "all" : proxyAssetFilter,
    artifactTarget,
    buildTarget,
    unavailableMessage: t("repository.failedToLoadProxyCache"),
  });
  const pages = artifacts.data?.pages;
  const rows = pages?.flatMap((page) => page.items) ?? [];
  const firstPage = pages?.[0];
  const nextToken = artifacts.hasNextPage
    ? pages?.at(-1)?.nextPageToken
    : undefined;
  const proxyTotal = pages?.at(-1)?.totalEstimate ?? 0;
  const proxyPage = pages?.length ?? 1;
  const loading = artifacts.isPending;
  const refreshing = artifacts.isFetching && !artifacts.isFetchingNextPage;
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
    setMatch(artifactTarget ? "prefix" : "contains");
  }, [artifactTarget, assetTarget, format, proxyMaven]);

  const searchPlaceholder = descriptor
    ? t(descriptor.searchPlaceholder)
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
    t,
  });

  const FormatDetail = formatViews(format).ArtifactDetail;
  const containExpandedDetail = format === "npm" || format === "pypi";
  const expandedRowRender = (r: ArtifactRow) => {
    // Proxy Maven lists cache entries, which have their own detail.
    if (proxyMaven) {
      return <ProxyMavenCacheDetail repoName={repo.name} meta={r} />;
    }
    if (!FormatDetail) return null;
    const detail = (
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
        canWrite={canWrite && repo.type === "hosted"}
        canQuarantine={canQuarantine}
        onDeleted={() => void load(q)}
        onVersionChange={(version) => onVersionChange?.(r.coordinate, version)}
      />
    );
    // Expanded rows share the table's scroll width. Keep these reading panes
    // within the visible table container, minus its two 16px cell gutters.
    return containExpandedDetail ? (
      <div style={{ maxWidth: "calc(100cqi - 32px)" }}>{detail}</div>
    ) : (
      detail
    );
  };

  if (!descriptor) {
    return (
      <RepositoryFeatureUnavailable
        feature={t("repository.artifactBrowser")}
        unsupportedFormat={format}
      />
    );
  }

  if (view === "directory" && supportsDirectory) {
    return (
      <div className="ag-artifact-browser">
        <div className="ag-artifact-view-toolbar">
          <ArtifactViewSwitch value={view} onChange={setView} />
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
          <ArtifactViewSwitch value={view} onChange={setView} />
        )}
        {!proxyMaven && descriptor?.serverKeywordSearch && (
          <ArtifactSearchMatchControl
            value={match}
            onChange={(value) => {
              setMatch(value);
              // Changing the match runs the search box's current text.
              setQuery(q);
            }}
          />
        )}
        <Input.Search
          loading={refreshing}
          allowClear
          className="ag-artifact-view-search"
          placeholder={
            descriptor?.serverKeywordSearch &&
            match === "contains" &&
            !proxyMaven &&
            descriptor.searchKeywordPlaceholder
              ? t(descriptor.searchKeywordPlaceholder)
              : (searchPlaceholder ?? t("repository.search2"))
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
          enterButton={t("repository.search")}
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
                { value: "primary", label: t("repository.primaryAssets") },
                { value: "all", label: t("repository.allFiles") },
                { value: "jar", label: t("repository.jAROnly") },
                { value: "pom", label: t("repository.pOMOnly") },
              ]}
              onChange={(value: ProxyMavenAssetFilter) => {
                setProxyAssetFilter(value);
                setExpandedImage(null);
              }}
            />
            <span className="text-xs text-fg-tertiary">
              {t("repository.mavenVersionsShowing", {
                number: formatNumber(proxyTotal),
                number2: formatNumber(rows.length),
              })}
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
            {t("repository.returnToFullList")}
          </Button>
        )}
      </div>
      {error !== null && rows.length > 0 && (
        <ErrorBanner error={error} onRetry={() => load(q)} />
      )}
      {error !== null && rows.length === 0 ? (
        isNotFound(error) ? (
          <RepositoryFeatureUnavailable
            feature={t("repository.artifactBrowser")}
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
            style={
              containExpandedDetail
                ? { containerType: "inline-size" }
                : undefined
            }
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
            loading={artifacts.isFetchingNextPage}
            onMore={() => {
              if (!artifacts.isFetchingNextPage) void artifacts.fetchNextPage();
            }}
          />
          {proxyMaven && proxyTotal > 0 && (
            <div className="border-t border-line px-4 py-2 text-center text-xs text-fg-tertiary">
              {t("repository.pageWithMavenVersionsPerPage", {
                number: formatNumber(proxyPage),
                number2: formatNumber(PROXY_MAVEN_PAGE_SIZE),
              })}
            </div>
          )}
        </>
      )}
    </div>
  );
}
