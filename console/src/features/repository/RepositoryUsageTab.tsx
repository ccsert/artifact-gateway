import { useCallback, useEffect, useState } from "react";
import { Button, Tag, Tooltip } from "antd";
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
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";

type UsageRow = ArtifactUsageStat & { key: string };

export function RepositoryUsageTab({ repo }: { repo: Repository }) {
  const { text, locale } = usePreferences();
  const [usage, setUsage] = useState<RepositoryArtifactUsage | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const { data, error: err } = await listRepositoryArtifactUsage({
        path: { repositoryId: repo.id },
        query: { limit: 200 },
      });
      if (err) throw err;
      if (data) setUsage(data);
    } catch (nextError) {
      setError(nextError);
    } finally {
      setLoading(false);
    }
  }, [repo.id]);

  useEffect(() => {
    void load();
  }, [load]);

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
      <ConsoleTable<UsageRow>
        size="small"
        columns={columns}
        dataSource={rows}
        loading={loading}
        pagination={false}
        scroll={{ x: 1000 }}
        locale={{
          emptyText: (
            <EmptyState
              compact
              title={text("暂无下载记录", "No downloads recorded yet")}
              hint={text(
                "尚无来自此仓库的下载记录。",
                "No downloads from this repository have been recorded yet.",
              )}
            />
          ),
        }}
      />
      <div className="flex justify-end">
        <Button onClick={() => void load()} loading={loading}>
          {text("刷新", "Refresh")}
        </Button>
      </div>
    </div>
  );
}
