import { useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import type { ArtifactSearchMatch } from "../../client";
import { Pagination } from "../../components/ui/Layout";
import { useSearchParams } from "react-router-dom";
import { Loading, ErrorBanner } from "../../components/ui/Feedback";
import { SectionBoundary } from "../../components/ui/SectionBoundary";
import type { UsageSnippet } from "../../lib/usage";
import { usePreferences } from "../../lib/preferences";
import type { PublicRepositoryFormat } from "../../lib/publicRepositoryUsage";
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
import type { PublicArtifactTableRow } from "./publicArtifactRows";
import {
  refetchPublicSources,
  usePublicArtifacts,
  usePublicCatalog,
} from "./publicBrowseQueries";
import { PUBLIC_FORMAT_ORDER } from "./publicFormats";
import { usePublicVersionPages } from "./usePublicVersionPages";
import { useArtifactBrowseLinks } from "./useArtifactBrowseLinks";
import {
  AnonymousReadsDisabled,
  PublicRepositoryMissing,
} from "./PublicBrowseStates";
import { formatDescriptor } from "../../formats";

/**
 * Anonymous browsing of public repositories (issue 275 split): this page owns
 * the URL state and wires the catalog, the repository workspace and the
 * usage card together; the sections render in their own components.
 */
export function PublicBrowsePage() {
  const { authenticated } = useAuth();
  const { t } = usePreferences();
  const [params, setParams] = useSearchParams();
  const repositoryId = params.get("repository") ?? "";
  const query = params.get("q") ?? "";
  const match: ArtifactSearchMatch =
    params.get("match") === "prefix" ? "prefix" : "contains";
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
  const {
    items,
    error,
    recoveryKeys: artifactRecoveryKeys,
    refetch: refetchArtifacts,
    hasMore,
    loadingMore,
    loadMore,
  } = usePublicArtifacts(
    repositoryId ? selectedRepository : undefined,
    query,
    artifactParam,
    match,
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

  const descriptor = formatDescriptor(selectedRepository?.format);
  const versionSource = descriptor?.publicVersionSource;
  useEffect(() => {
    if (
      !artifactParam ||
      !items?.some((item) => item?.coordinate === artifactParam)
    )
      return;
    if (versionSource === "ociTags" && !ociTagPages[artifactParam]) {
      void loadOciTags(artifactParam);
    }
    if (
      versionSource === "conanRevisions" &&
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
    versionSource,
  ]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!repositoryId) return;
    const next = queryDraft.trim();
    if (next === query) {
      void refetchArtifacts().catch(() => {});
      return;
    }
    setParams({
      repository: repositoryId,
      match,
      ...(next ? { q: next } : {}),
    });
  };

  const copyCoordinate = (coordinate: string) => copy(coordinate);

  const copyUsage = (snippet: UsageSnippet) => copy(snippet.code);

  const { artifactHref, openArtifact, clearArtifactParams } =
    useArtifactBrowseLinks(params, setParams);

  const copyPageLink = (href: string) =>
    copy(new URL(href, window.location.origin).toString(), href);

  const resetWorkspace = () =>
    refetchPublicSources([
      catalog.refetch({ throwOnError: true }),
      refetchArtifacts(),
    ]);
  const retryCatalog = () => {
    void catalog.refetch({ throwOnError: true }).catch(() => {});
  };
  const retryArtifacts = () => {
    void refetchArtifacts().catch(() => {});
  };

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
            match={match}
            onMatchChange={(value) =>
              setParams({
                repository: repositoryId,
                match: value,
                ...(query ? { q: query } : {}),
              })
            }
            onQueryDraftChange={setQueryDraft}
            onSubmit={submit}
            onBack={() => setParams({})}
          />
        )}
        <div
          data-public-browse-workspace
          className={
            selectedRepository && descriptor
              ? "grid gap-5 lg:grid-cols-[minmax(0,1fr)_360px] lg:items-start"
              : ""
          }
        >
          <section>
            <SectionBoundary
              resetKeys={[repositoryId, query]}
              recoveryKeys={[catalog.data, ...artifactRecoveryKeys]}
              onReset={resetWorkspace}
            >
              <div className="grid gap-4">
                {catalogError && repositories && (
                  <PublicBrowseStateSurface>
                    <ErrorBanner
                      error={catalogError}
                      tone="warning"
                      onRetry={retryCatalog}
                    />
                  </PublicBrowseStateSurface>
                )}
                {catalogError && !repositories ? (
                  <PublicBrowseStateSurface>
                    <ErrorBanner error={catalogError} onRetry={retryCatalog} />
                  </PublicBrowseStateSurface>
                ) : !repositories ? (
                  <PublicBrowseStateSurface>
                    <Loading
                      label={t("publicBrowse.loadingPublicRepositories")}
                    />
                  </PublicBrowseStateSurface>
                ) : anonymousEnabled === false ? (
                  <AnonymousReadsDisabled />
                ) : repositoryId && !selectedRepository ? (
                  <PublicRepositoryMissing onBack={() => setParams({})} />
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
                  <>
                    <PublicArtifactResults
                      selectedRepository={selectedRepository}
                      items={items}
                      error={error}
                      artifactRowOptions={{
                        expandedCoordinate,
                        artifactParam,
                        tagParam,
                        ociTagPages,
                        ociManifestDetails,
                        selectedProtocolVersions,
                      }}
                      onRetry={retryArtifacts}
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
                    {items && (
                      <Pagination
                        hasMore={hasMore}
                        loading={loadingMore}
                        onMore={() => {
                          void loadMore().catch(() => {});
                        }}
                      />
                    )}
                  </>
                )}
              </div>
            </SectionBoundary>
          </section>
          {selectedRepository && descriptor && (
            <aside className="lg:sticky lg:top-24">
              <SectionBoundary
                resetKeys={[repositoryId]}
                recoveryKeys={[catalog.data]}
                onReset={() => catalog.refetch({ throwOnError: true })}
              >
                <PublicUsageAside
                  selectedRepository={selectedRepository}
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
