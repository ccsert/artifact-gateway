import { useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import { SafetyCertificateOutlined, SearchOutlined } from "@ant-design/icons";
import { Button } from "antd";
import { Link, useSearchParams } from "react-router-dom";
import { Loading, ErrorBanner, EmptyState } from "../../components/ui/Feedback";
import { SectionBoundary } from "../../components/ui/SectionBoundary";
import type { UsageSnippet } from "../../lib/usage";
import { usePreferences } from "../../lib/preferences";
import {
  artifactBrowseParams,
  artifactBrowsePath,
  clearArtifactBrowseParams,
} from "../../lib/publicBrowseModel";
import {
  publicRepositoryUsage,
  type PublicRepositoryFormat,
} from "../../lib/publicRepositoryUsage";
import { useClipboardAction } from "../../components/ui/ConsolePrimitives";
import { useAuth } from "../../lib/auth";
import { PublicArtifactResults } from "./PublicArtifactResults";
import {
  PublicBrowseHeader,
  PublicBrowseStateSurface,
} from "./PublicBrowseChrome";
import { PublicCatalog } from "./PublicCatalog";
import { PublicCatalogHero } from "./PublicCatalogHero";
import { PublicRepositoryBar } from "./PublicRepositoryBar";
import { PublicUsageAside } from "./PublicUsageAside";
import {
  buildPublicArtifactRows,
  type PublicArtifactTableRow,
} from "./publicArtifactRows";
import { usePublicArtifacts, usePublicCatalog } from "./publicBrowseQueries";
import { PUBLIC_FORMAT_ORDER, publicUsageLabel } from "./publicFormats";
import { usePublicVersionPages } from "./usePublicVersionPages";

/**
 * Anonymous browsing of public repositories (issue 275 split): this page owns
 * the URL state and wires the catalog, the repository workspace and the
 * usage card together; the sections render in their own components.
 */
export function PublicBrowsePage() {
  const { authenticated } = useAuth();
  const { text } = usePreferences();
  const [params, setParams] = useSearchParams();
  const repositoryId = params.get("repository") ?? "";
  const query = params.get("q") ?? "";
  const artifactParam = params.get("artifact") ?? "";
  const buildParam = params.get("build") ?? "";
  const tagParam = params.get("tag") ?? "";
  const revisionParam = params.get("revision") ?? "";
  const versionParam = params.get("version") ?? "";
  const [queryDraft, setQueryDraft] = useState(query);
  const catalog = usePublicCatalog();
  const repositories = catalog.data?.repositories ?? null;
  const anonymousEnabled = catalog.data?.anonymousEnabled ?? null;
  const catalogError = catalog.error;
  const [catalogQuery, setCatalogQuery] = useState("");
  const [catalogFormat, setCatalogFormat] = useState<
    PublicRepositoryFormat | "all"
  >("all");
  const { copiedValue: copiedCoordinate, copy } = useClipboardAction(1400);
  const [expandedCoordinate, setExpandedCoordinate] = useState<string | null>(
    null,
  );
  const [protocolVersionFilter, setProtocolVersionFilter] = useState("");
  const [selectedProtocolVersions, setSelectedProtocolVersions] = useState<
    Record<string, string>
  >({});
  const selectedRepository = repositories?.find(
    (repository) => repository.id === repositoryId,
  );
  const { items, error } = usePublicArtifacts(
    repositoryId ? selectedRepository : undefined,
    query,
    artifactParam,
  );
  const {
    ociTagPages,
    ociManifestDetails,
    conanRevisionPages,
    loadOciTags,
    loadOciManifest,
    loadConanRevisions,
  } = usePublicVersionPages({
    repositoryId,
    selectedRepository,
    artifactParam,
    tagParam,
  });
  const publicFormats = useMemo(
    () =>
      PUBLIC_FORMAT_ORDER.filter((format) =>
        repositories?.some((repository) => repository.format === format),
      ),
    [repositories],
  );
  const visibleRepositories = useMemo(() => {
    const normalizedQuery = catalogQuery.trim().toLocaleLowerCase();
    return (repositories ?? []).filter((repository) => {
      if (catalogFormat !== "all" && repository.format !== catalogFormat) {
        return false;
      }
      if (!normalizedQuery) return true;
      return [repository.name, repository.format, repository.type ?? "hosted"]
        .join(" ")
        .toLocaleLowerCase()
        .includes(normalizedQuery);
    });
  }, [catalogFormat, catalogQuery, repositories]);

  useEffect(() => {
    if (
      !artifactParam ||
      !items?.some((item) => item.coordinate === artifactParam)
    )
      return;
    if (selectedRepository?.format === "oci" && !ociTagPages[artifactParam]) {
      void loadOciTags(artifactParam);
    }
    if (
      selectedRepository?.format === "conan" &&
      !conanRevisionPages[artifactParam]
    ) {
      if (revisionParam) setProtocolVersionFilter(revisionParam);
      void loadConanRevisions(artifactParam, revisionParam);
    }
  }, [
    artifactParam,
    conanRevisionPages,
    items,
    loadConanRevisions,
    loadOciTags,
    ociTagPages,
    revisionParam,
    selectedRepository?.format,
  ]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!repositoryId) return;
    setParams({
      repository: repositoryId,
      ...(queryDraft.trim() ? { q: queryDraft.trim() } : {}),
    });
  };

  const globalUsage = selectedRepository
    ? publicRepositoryUsage({
        format: selectedRepository.format,
        repositoryName: selectedRepository.name,
        origin: window.location.origin,
        host: window.location.host,
        text,
      })
    : [];

  const copyCoordinate = (coordinate: string) => copy(coordinate);

  const copyUsage = (snippet: UsageSnippet) => copy(snippet.code);

  const artifactHref = (
    coordinate: string,
    buildNumber?: number,
    tag?: string,
    revision?: string,
    version?: string,
  ) => {
    return artifactBrowsePath(params, {
      coordinate,
      buildNumber,
      tag,
      revision,
      version,
    });
  };

  const openArtifact = (
    coordinate: string,
    buildNumber?: number,
    tag?: string,
    revision?: string,
    version?: string,
  ) => {
    const next = artifactBrowseParams(params, {
      coordinate,
      buildNumber,
      tag,
      revision,
      version,
    });
    setParams(next, { replace: true, preventScrollReset: true });
  };

  const clearArtifactParams = () => {
    const next = clearArtifactBrowseParams(params);
    setParams(next, { replace: true, preventScrollReset: true });
  };

  const copyPageLink = (href: string) =>
    copy(new URL(href, window.location.origin).toString(), href);

  const usageLabel = publicUsageLabel(selectedRepository?.format, text);

  const artifactTableRows = buildPublicArtifactRows({
    items: items ?? [],
    selectedRepository,
    expandedCoordinate,
    artifactParam,
    tagParam,
    ociTagPages,
    ociManifestDetails,
    selectedProtocolVersions,
    artifactHref,
  });

  const toggleArtifactRow = (row: PublicArtifactTableRow) => {
    if (row.expanded) {
      setExpandedCoordinate(null);
      clearArtifactParams();
      return;
    }
    setExpandedCoordinate(row.item.coordinate);
    setProtocolVersionFilter("");
    if (row.isOci && !row.page) void loadOciTags(row.item.coordinate);
    if (row.selectedProtocolVersionValue) {
      setSelectedProtocolVersions((current) => ({
        ...current,
        [row.item.coordinate]: row.selectedProtocolVersionValue,
      }));
    }
  };

  const selectOciVersion = (coordinate: string, value: string) => {
    setSelectedProtocolVersions((current) => ({
      ...current,
      [coordinate]: value,
    }));
    void loadOciManifest(coordinate, value);
    openArtifact(coordinate, undefined, value);
  };

  return (
    <main className="ag-public-browse-shell min-h-screen px-4 py-6 text-fg sm:px-6 sm:py-8 lg:px-8 lg:py-10">
      <div className="ag-public-browse-page ag-page-stack mx-auto w-full max-w-[1440px]">
        <PublicBrowseHeader authenticated={authenticated} />
        {!repositoryId ? (
          <PublicCatalogHero
            repositories={repositories}
            publicFormats={publicFormats}
            catalogQuery={catalogQuery}
            onCatalogQueryChange={setCatalogQuery}
          />
        ) : (
          <PublicRepositoryBar
            selectedRepository={selectedRepository}
            queryDraft={queryDraft}
            onQueryDraftChange={setQueryDraft}
            onSubmit={submit}
            onBack={() => setParams({})}
          />
        )}
        <div
          data-public-browse-workspace
          className={
            selectedRepository
              ? "grid gap-5 lg:grid-cols-[minmax(0,1fr)_360px] lg:items-start"
              : ""
          }
        >
          <section>
            <SectionBoundary resetKeys={[repositoryId, query]}>
              {catalogError ? (
                <PublicBrowseStateSurface>
                  <ErrorBanner error={catalogError} />
                </PublicBrowseStateSurface>
              ) : !repositories ? (
                <PublicBrowseStateSurface>
                  <Loading
                    label={text(
                      "正在读取公开仓库…",
                      "Loading public repositories…",
                    )}
                  />
                </PublicBrowseStateSurface>
              ) : anonymousEnabled === false ? (
                <PublicBrowseStateSurface>
                  <EmptyState
                    icon={<SafetyCertificateOutlined />}
                    title={text(
                      "全局匿名读取未启用",
                      "Global anonymous reads are disabled",
                    )}
                    hint={text(
                      "请管理员在访问控制中启用全局匿名读取，仓库的匿名读取设置才会生效。",
                      "An administrator must enable global anonymous reads before repository settings take effect.",
                    )}
                    action={
                      <Link
                        to="/access"
                        className="text-sm text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
                      >
                        {text("前往访问控制", "Open access control")}
                      </Link>
                    }
                  />
                </PublicBrowseStateSurface>
              ) : repositoryId && !selectedRepository ? (
                <PublicBrowseStateSurface>
                  <EmptyState
                    icon={<SearchOutlined />}
                    title={text(
                      "公开仓库不存在或不可见",
                      "Public repository not found or visible",
                    )}
                    hint={text(
                      "返回公开仓库目录，选择一个已启用匿名读取的仓库。",
                      "Return to the catalog and choose a repository with anonymous reads enabled.",
                    )}
                    action={
                      <Button type="primary" onClick={() => setParams({})}>
                        {text("返回目录", "Back to catalog")}
                      </Button>
                    }
                  />
                </PublicBrowseStateSurface>
              ) : !repositoryId ? (
                <PublicCatalog
                  authenticated={authenticated}
                  repositories={repositories}
                  publicFormats={publicFormats}
                  visibleRepositories={visibleRepositories}
                  catalogFormat={catalogFormat}
                  onCatalogFormatChange={setCatalogFormat}
                  onClearFilters={() => {
                    setCatalogQuery("");
                    setCatalogFormat("all");
                  }}
                />
              ) : (
                <PublicArtifactResults
                  selectedRepository={selectedRepository}
                  items={items}
                  error={error}
                  artifactTableRows={artifactTableRows}
                  artifactParam={artifactParam}
                  buildParam={buildParam}
                  revisionParam={revisionParam}
                  versionParam={versionParam}
                  copiedCoordinate={copiedCoordinate}
                  conanRevisionPages={conanRevisionPages}
                  selectedProtocolVersions={selectedProtocolVersions}
                  protocolVersionFilter={protocolVersionFilter}
                  actions={{
                    artifactHref,
                    openArtifact,
                    clearArtifactParams,
                    copyCoordinate,
                    copyPageLink,
                    copyUsage,
                    toggleArtifactRow,
                    selectOciVersion,
                    loadOciTags,
                    loadOciManifest,
                    loadConanRevisions,
                    setSelectedProtocolVersions,
                    setProtocolVersionFilter,
                  }}
                />
              )}
            </SectionBoundary>
          </section>
          {selectedRepository && (
            <aside className="lg:sticky lg:top-24">
              <SectionBoundary resetKeys={[repositoryId]}>
                <PublicUsageAside
                  selectedRepository={selectedRepository}
                  usageLabel={usageLabel}
                  globalUsage={globalUsage}
                  copiedCoordinate={copiedCoordinate}
                  onCopyUsage={copyUsage}
                />
              </SectionBoundary>
            </aside>
          )}
        </div>
      </div>
    </main>
  );
}
