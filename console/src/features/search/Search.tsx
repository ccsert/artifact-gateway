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
  const { locale, text } = usePreferences();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const q = (params.get("q") ?? "").trim();
  const match: ArtifactSearchMatch =
    params.get("match") === "prefix" ? "prefix" : "contains";
  const [query, setQuery] = useState(q);
  const search = useArtifactSearch(
    q,
    match,
    text("搜索制品失败", "Artifact search failed"),
  );
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
    text,
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
        title={text("全局搜索", "Global search")}
        description={
          q
            ? text(
                `在所有仓库中搜索 “${q}”`,
                `Searching all repositories for “${q}”`,
              )
            : text(
                "跨仓库搜索制品坐标、包名、路径、引用与 SHA-256",
                "Search artifact coordinates, package names, paths, references, and SHA-256 digests across repositories",
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
              {text("搜索", "Search")}
            </Button>
          }
          className="max-w-3xl"
          placeholder={text(
            "输入坐标、包名、路径关键词或 SHA-256…",
            "Enter a coordinate, package, path keyword, or SHA-256 digest…",
          )}
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
          title={text("输入关键词开始搜索", "Enter a keyword to search")}
          hint={text(
            "支持 Maven 坐标、npm 包名、PyPI 项目、Go 模块、OCI 镜像、Conan 引用、Raw 路径和完整 SHA-256",
            "Supports Maven coordinates, npm packages, PyPI projects, Go modules, OCI images, Conan references, Raw paths, and full SHA-256 digests",
          )}
        />
      ) : error && hits.length === 0 ? (
        <ErrorBanner error={error} />
      ) : loading && hits.length === 0 ? (
        <Loading
          label={
            searchedRepos
              ? text(
                  `正在搜索 ${searchedRepos} 个仓库…`,
                  `Searching ${searchedRepos} repositories…`,
                )
              : text("加载中…", "Loading…")
          }
        />
      ) : rows.length === 0 ? (
        <EmptyState
          title={text("没有匹配的制品", "No matching artifacts")}
          hint={text(
            `已在 ${searchedRepos} 个仓库中搜索 “${q}”，未找到结果`,
            `No results for “${q}” across ${searchedRepos} repositories`,
          )}
        />
      ) : (
        <>
          <MetricStrip
            items={[
              {
                label: text("结果条目", "Results"),
                value: rows.length,
                hint: text(
                  `${hits.length} 个版本或文件`,
                  `${hits.length} versions or files`,
                ),
              },
              {
                label: text("检索仓库", "Repositories searched"),
                value: searchedRepos,
                hint: text(
                  "仅包含当前身份可读仓库",
                  "Only repositories readable by the current identity",
                ),
              },
              {
                label: text("匹配方式", "Match mode"),
                value:
                  hits[0]?.matchKind === "digest"
                    ? text("SHA-256 精确", "Exact SHA-256")
                    : (hits[0]?.match ?? match) === "contains"
                      ? text("关键词包含", "Literal contains")
                      : text("坐标前缀", "Coordinate prefix"),
                hint:
                  hits[0]?.matchKind === "digest"
                    ? text(
                        "包含历史可见版本",
                        "Includes older visible versions",
                      )
                    : text("按格式校验输入", "Input is validated by format"),
              },
              {
                label: text("分页状态", "Pagination"),
                value: nextPageToken
                  ? text("还有更多", "More available")
                  : text("已完成", "Complete"),
                hint: nextPageToken
                  ? text(
                      "继续加载不会丢失结果",
                      "Loading more preserves current results",
                    )
                  : text("已到达末尾", "End of results"),
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
