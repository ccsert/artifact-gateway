import { useState } from "react";
import { Button, Input, Tag, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { type ArtifactUsageStat, type Repository } from "../../client";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { formatBytes, formatDate, formatNumber } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { ConsoleTable, FilterBar } from "../../components/ui/ConsolePrimitives";
import { CONSOLE_PAGE_SIZE } from "../../components/ui/useConsolePagination";
import { useArtifactUsage } from "./repositoryQueries";

type UsageRow = ArtifactUsageStat & { key: string };

export function RepositoryUsageTab({ repo }: { repo: Repository }) {
  return <RepositoryUsageContent key={repo.id} repo={repo} />;
}

function RepositoryUsageContent({ repo }: { repo: Repository }) {
  const { t, locale } = usePreferences();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(CONSOLE_PAGE_SIZE);
  const [input, setInput] = useState("");
  const [query, setQuery] = useState("");
  const usageQuery = useArtifactUsage(repo.id, page, pageSize, query);
  const usage = usageQuery.data ?? null;
  const error = usageQuery.error;
  const loading = usageQuery.isFetching;
  const load = () => usageQuery.refetch();
  // A live deletion can remove the last page; move back to the new last page.
  const lastPage = usage
    ? Math.max(1, Math.ceil(usage.totalCount / pageSize))
    : page;
  if (!usageQuery.isPlaceholderData && page > lastPage) setPage(lastPage);

  const columns: ColumnsType<UsageRow> = [
    {
      title: t("repository.artifactAddress"),
      dataIndex: "resource",
      render: (resource: string) => (
        <Tooltip title={resource}>
          <span className="block max-w-[32rem] truncate font-mono text-xs text-fg-secondary">
            {resource}
          </span>
        </Tooltip>
      ),
    },
    {
      title: t("repository.format"),
      dataIndex: "format",
      width: 96,
      render: (format: string) => <Tag>{format}</Tag>,
    },
    {
      title: t("repository.downloads"),
      dataIndex: "downloadCount",
      width: 128,
      render: (value: number) => formatNumber(value, locale),
    },
    {
      title: t("repository.totalBytes"),
      dataIndex: "totalBytes",
      width: 128,
      render: (value: number) => formatBytes(value),
    },
    {
      title: t("repository.firstDownload"),
      dataIndex: "firstDownloadedAt",
      width: 184,
      render: (value: string) => formatDate(value, locale),
    },
    {
      title: t("repository.lastDownload"),
      dataIndex: "lastDownloadedAt",
      width: 184,
      render: (value: string) => formatDate(value, locale),
    },
    {
      title: t("repository.lastDownloadedBy"),
      dataIndex: "lastActor",
      width: 160,
      render: (value: string | undefined) => value ?? "—",
    },
  ];

  if (!usage) {
    return error ? <ErrorBanner error={error} onRetry={load} /> : <Loading />;
  }

  const rows: UsageRow[] = usage.items.map((item) => ({
    ...item,
    key: `${item.format}\u0000${item.resource}`,
  }));

  return (
    <div className="ag-page-stack">
      {error !== null && <ErrorBanner error={error} onRetry={load} />}
      <FilterBar>
        <Input.Search
          className="max-w-md"
          aria-label={t("repository.searchArtifactAddress")}
          placeholder={t("repository.searchAddressCaseSensitive")}
          maxLength={512}
          allowClear
          value={input}
          onChange={(event) => setInput(event.target.value)}
          onSearch={(value) => {
            setQuery(value.trim());
            setPage(1);
          }}
        />
        <Button onClick={() => void load()} loading={loading}>
          {t("repository.refresh")}
        </Button>
      </FilterBar>
      <div className="text-sm text-fg-secondary" role="status">
        {t("repository.wholeRepositoryAddressesDownloads", {
          number: formatNumber(usage.totals.resources, locale),
          number2: formatNumber(usage.totals.downloadCount, locale),
          bytes: formatBytes(usage.totals.totalBytes),
        })}
      </div>
      <ConsoleTable<UsageRow>
        size="small"
        columns={columns}
        dataSource={rows}
        loading={loading}
        pagination={{
          current: page,
          pageSize,
          total: usage.totalCount,
          onChange: (nextPage, nextSize) => {
            setPage(nextPage);
            setPageSize(nextSize);
          },
          disabled: loading,
        }}
        scroll={{ x: 1000 }}
        locale={{
          emptyText: (
            <EmptyState
              compact
              title={
                query
                  ? t("repository.noMatchingArtifactAddresses")
                  : t("repository.noDownloadsRecordedYet")
              }
              hint={
                query
                  ? t("repository.tryADifferentAddressSearch")
                  : t(
                      "repository.noDownloadsFromThisRepositoryHaveBeenRecorded",
                    )
              }
            />
          ),
        }}
      />
    </div>
  );
}
