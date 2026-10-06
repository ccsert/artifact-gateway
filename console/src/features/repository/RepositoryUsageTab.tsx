import { useCallback, useEffect, useRef, useState } from "react";
import { Button, Input, Tag, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  listRepositoryArtifactUsage,
  type ArtifactUsageStat,
  type Repository,
  type RepositoryArtifactUsage,
} from "../../client";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { formatBytes, formatDate, formatNumber } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { ConsoleTable, FilterBar } from "../../components/ui/ConsolePrimitives";
import { CONSOLE_PAGE_SIZE } from "../../components/ui/useConsolePagination";

type UsageRow = ArtifactUsageStat & { key: string };

export function RepositoryUsageTab({ repo }: { repo: Repository }) {
  return <RepositoryUsageContent key={repo.id} repo={repo} />;
}

function RepositoryUsageContent({ repo }: { repo: Repository }) {
  const { text, locale } = usePreferences();
  const [usage, setUsage] = useState<RepositoryArtifactUsage | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(CONSOLE_PAGE_SIZE);
  const [input, setInput] = useState("");
  const [query, setQuery] = useState("");
  const sequence = useRef(0);
  const invalidatePending = useCallback(() => {
    sequence.current++;
  }, []);

  const load = useCallback(async () => {
    const request = ++sequence.current;
    setLoading(true);
    setError(null);
    try {
      const { data, error: err } = await listRepositoryArtifactUsage({
        path: { repositoryId: repo.id },
        query: {
          limit: pageSize,
          offset: (page - 1) * pageSize,
          q: query || undefined,
        },
      });
      if (request !== sequence.current) return;
      if (err) throw err;
      if (data) {
        // A live deletion can remove the last page; recover with one bounded read.
        const lastPage = Math.max(1, Math.ceil(data.totalCount / pageSize));
        if (page > lastPage) {
          setPage(lastPage);
          return;
        }
        setUsage(data);
      }
    } catch (nextError) {
      if (request === sequence.current) setError(nextError);
    } finally {
      if (request === sequence.current) setLoading(false);
    }
  }, [repo.id, page, pageSize, query]);

  useEffect(() => {
    void load();
    return invalidatePending;
  }, [load, invalidatePending]);

  const columns: ColumnsType<UsageRow> = [
    {
      title: text("制品地址", "Artifact address"),
      dataIndex: "resource",
      render: (resource: string) => (
        <Tooltip title={resource}>
          <span className="block max-w-[32rem] truncate font-mono text-xs text-zinc-300">
            {resource}
          </span>
        </Tooltip>
      ),
    },
    {
      title: text("格式", "Format"),
      dataIndex: "format",
      width: 96,
      render: (format: string) => <Tag>{format}</Tag>,
    },
    {
      title: text("下载次数", "Downloads"),
      dataIndex: "downloadCount",
      width: 128,
      render: (value: number) => formatNumber(value, locale),
    },
    {
      title: text("累计流量", "Total bytes"),
      dataIndex: "totalBytes",
      width: 128,
      render: (value: number) => formatBytes(value),
    },
    {
      title: text("首次下载", "First download"),
      dataIndex: "firstDownloadedAt",
      width: 184,
      render: (value: string) => formatDate(value, locale),
    },
    {
      title: text("最近下载", "Last download"),
      dataIndex: "lastDownloadedAt",
      width: 184,
      render: (value: string) => formatDate(value, locale),
    },
    {
      title: text("最近使用者", "Last downloaded by"),
      dataIndex: "lastActor",
      width: 160,
      render: (value: string | undefined) => value ?? "—",
    },
  ];

  if (!usage || usage.repositoryId !== repo.id) {
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
          aria-label={text("搜索制品地址", "Search artifact address")}
          placeholder={text(
            "搜索制品地址（区分大小写）",
            "Search address (case-sensitive)",
          )}
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
          {text("刷新", "Refresh")}
        </Button>
      </FilterBar>
      <div className="text-sm text-zinc-400" role="status">
        {text(
          `全仓库：${formatNumber(usage.totals.resources, locale)} 个地址 · ${formatNumber(usage.totals.downloadCount, locale)} 次下载 · ${formatBytes(usage.totals.totalBytes)}`,
          `Whole repository: ${formatNumber(usage.totals.resources, locale)} addresses · ${formatNumber(usage.totals.downloadCount, locale)} downloads · ${formatBytes(usage.totals.totalBytes)}`,
        )}
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
                  ? text("没有匹配的制品地址", "No matching artifact addresses")
                  : text("暂无下载记录", "No downloads recorded yet")
              }
              hint={
                query
                  ? text(
                      "调整地址搜索条件后重试。",
                      "Try a different address search.",
                    )
                  : text(
                      "尚无来自此仓库的下载记录。",
                      "No downloads from this repository have been recorded yet.",
                    )
              }
            />
          ),
        }}
      />
    </div>
  );
}
