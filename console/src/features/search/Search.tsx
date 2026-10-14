import { useEffect, useMemo, useState } from "react";
import { Button, Input } from "antd";
import { useNavigate, useSearchParams } from "react-router-dom";
import type {
  ArtifactSearchMatch,
  GlobalArtifactSearchHit,
} from "../../client";
import { PageHeader, Pagination } from "../../components/ui/Layout";
import { Loading, ErrorBanner, EmptyState } from "../../components/ui/Feedback";
import {
  ConsoleTable,
  MetricStrip,
} from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";

import { ArtifactSearchMatchControl } from "../../components/ui/ArtifactSearchMatchControl";

import {
  type SearchTableRow,
  isMavenGroup,
  artifactTarget,
  buildRows,
} from "./searchModel";
import { searchColumns } from "./searchColumns";
import { SearchHitDetail } from "./SearchHitDetail";
import { useArtifactSearch } from "./searchQueries";
import { useQueryClient } from "@tanstack/react-query";
import { searchKeys } from "../../lib/query/keys";

export { artifactTarget, artifactVersionSizeLabel } from "./searchModel";

export function SearchPage() {
  const { locale, t } = usePreferences();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const q = (params.get("q") ?? "").trim();
  const match: ArtifactSearchMatch =
    params.get("match") === "prefix" ? "prefix" : "contains";
  const [query, setQuery] = useState(q);
  const search = useArtifactSearch(q, match, t("search.artifactSearchFailed"));
  const hits = useMemo(
    () => search.data?.pages.flatMap((page) => page.items) ?? [],
    [search.data],
  );
  const lastPage = search.data?.pages.at(-1);
  const searchedRepos = lastPage?.searchedRepositories ?? 0;
  const nextPageToken = lastPage?.nextPageToken;
  const loading = search.isFetching && !search.isFetchingNextPage;
  const loadingMore = search.isFetchingNextPage;
  const error = search.error;
  // The open row and version belong to the search they were opened in.
  const scope = `${match}:${q}`;
  const [opened, setOpened] = useState<{
    scope: string;
    row: string | null;
    version: string | null;
  } | null>(null);
  const expandedRow = opened?.scope === scope ? opened.row : null;
  const selectedVersion = opened?.scope === scope ? opened.version : null;
  const setExpandedRow = (row: string | null) =>
    setOpened({ scope, row, version: selectedVersion });
  const setSelectedVersion = (version: string | null) =>
    setOpened((current) => ({
      scope,
      row: current?.scope === scope ? current.row : null,
      version,
    }));

  // The search box follows the URL when another search is opened.
  useEffect(() => {
    setQuery(q);
  }, [q]);

  const queryClient = useQueryClient();
  // Searching the same text again always asks again. A refetch alone would
  // join a first page still in flight instead of restarting it.
  const retry = async () => {
    await queryClient.cancelQueries({
      queryKey: searchKeys.artifacts(match, q),
    });
    await search.refetch();
  };

  const rows = useMemo(() => buildRows(hits), [hits]);
  const loadMore = () => {
    if (!nextPageToken || loading || loadingMore) return;
    void search.fetchNextPage();
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

  const setRowExpanded = (tableRow: SearchTableRow, next: boolean) =>
    setOpened({
      scope,
      row: next ? tableRow.key : null,
      version:
        next && isMavenGroup(tableRow.row)
          ? `${tableRow.row.key}:${tableRow.row.hits[0].coordinate}:${tableRow.row.hits[0].buildNumber ?? 0}`
          : null,
    });

  const columns = searchColumns({
    expandedRow,
    locale,
    openHit,
    setRowExpanded,
    t,
  });

  const expandedRowRender = (tableRow: SearchTableRow) => (
    <SearchHitDetail
      tableRow={tableRow}
      openHit={openHit}
      selectedVersion={selectedVersion}
      setSelectedVersion={setSelectedVersion}
    />
  );

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={t("search.globalSearch")}
        description={
          q
            ? t("search.searchingAllRepositoriesFor", { q })
            : t(
                "search.searchArtifactCoordinatesPackageNamesPathsReferencesAnd",
              )
        }
      />
      <div className="flex flex-wrap items-center gap-3">
        <ArtifactSearchMatchControl
          value={match}
          onChange={(value) =>
            navigate(
              `/search?${new URLSearchParams({ ...(q ? { q } : {}), match: value })}`,
            )
          }
        />
        <Input.Search
          allowClear
          enterButton={
            <Button type="primary" disabled={!query.trim()}>
              {t("search.search")}
            </Button>
          }
          className="max-w-3xl"
          placeholder={t("search.enterACoordinatePackagePathKeywordOrSHA")}
          value={query}
          loading={loading}
          onChange={(event) => {
            const value = event.target.value;
            setQuery(value);
            if (!value) navigate(`/search?${new URLSearchParams({ match })}`);
          }}
          onSearch={(value) => {
            const next = value.trim();
            if (!next) return;
            if (next === q) void retry();
            else navigate(`/search?${new URLSearchParams({ q: next, match })}`);
          }}
        />
      </div>
      {!q ? (
        <EmptyState
          title={t("search.enterAKeywordToSearch")}
          hint={t("search.supportsMavenCoordinatesNpmPackagesPyPIProjectsGo")}
        />
      ) : error && hits.length === 0 ? (
        <ErrorBanner error={error} />
      ) : loading && hits.length === 0 ? (
        <Loading
          label={
            searchedRepos
              ? t("search.searchingRepositories", { searchedRepos })
              : t("search.loading")
          }
        />
      ) : rows.length === 0 ? (
        <EmptyState
          title={t("search.noMatchingArtifacts")}
          hint={t("search.noResultsForAcrossRepositories", {
            searchedRepos,
            q,
          })}
        />
      ) : (
        <>
          <MetricStrip
            items={[
              {
                label: t("search.results"),
                value: rows.length,
                hint: t("search.versionsOrFiles", { length: hits.length }),
              },
              {
                label: t("search.repositoriesSearched"),
                value: searchedRepos,
                hint: t("search.onlyRepositoriesReadableByTheCurrentIdentity"),
              },
              {
                label: t("search.matchMode"),
                value:
                  hits[0]?.matchKind === "digest"
                    ? t("search.exactSHA256")
                    : (hits[0]?.match ?? match) === "contains"
                      ? t("search.literalContains")
                      : t("search.coordinatePrefix"),
                hint:
                  hits[0]?.matchKind === "digest"
                    ? t("search.includesOlderVisibleVersions")
                    : t("search.inputIsValidatedByFormat"),
              },
              {
                label: t("search.pagination"),
                value: nextPageToken
                  ? t("search.moreAvailable")
                  : t("search.complete"),
                hint: nextPageToken
                  ? t("search.loadingMorePreservesCurrentResults")
                  : t("search.endOfResults"),
              },
            ]}
          />
          {error && <ErrorBanner error={error} />}
          <div className="overflow-hidden rounded-lg border border-line bg-surface-translucent">
            <ConsoleTable<SearchTableRow>
              rowKey="key"
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
          <Pagination
            hasMore={!!nextPageToken}
            loading={loadingMore}
            disabled={loading}
            onMore={() => void loadMore()}
          />
        </>
      )}
    </div>
  );
}
