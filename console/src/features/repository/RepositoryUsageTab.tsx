import { useCallback, useEffect, useState } from "react";
import { Button, Space, Table, Tag, Tooltip } from "antd";
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

type UsageRow = ArtifactUsageStat & { key: string };

export function RepositoryUsageTab({ repo }: { repo: Repository }) {
  const { text, locale } = usePreferences();
  const [usage, setUsage] = useState<RepositoryArtifactUsage | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    const { data, error: err } = await listRepositoryArtifactUsage({
      path: { repositoryId: repo.id },
      query: { limit: 200 },
    });
    setLoading(false);
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      setUsage(data);
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

  if (error) {
    return <ErrorBanner error={error} onRetry={load} />;
  }
  if (!usage) {
    return <Loading />;
  }

  const rows: UsageRow[] = usage.items.map((item) => ({
    ...item,
    key: `${item.format}\u0000${item.resource}`,
  }));

  const cards: Array<{ label: string; value: string }> = [
    {
      label: text("累计下载", "Lifetime downloads"),
      value: formatNumber(usage.totals.downloadCount, locale),
    },
    {
      label: text("制品地址", "Artifact addresses"),
      value: formatNumber(usage.totals.resources, locale),
    },
    {
      label: text("累计流量", "Total transferred"),
      value: formatBytes(usage.totals.totalBytes),
    },
  ];

  return (
    <Space direction="vertical" size="large" style={{ width: "100%" }}>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        {cards.map((card) => (
          <div
            key={card.label}
            className="rounded-lg border border-zinc-800 px-4 py-3"
          >
            <div className="text-xs uppercase tracking-wider text-zinc-500">
              {card.label}
            </div>
            <div className="mt-1 text-xl font-semibold text-zinc-100">
              {card.value}
            </div>
          </div>
        ))}
      </div>
      <Table<UsageRow>
        className="ag-console-table"
        size="small"
        columns={columns}
        dataSource={rows}
        loading={loading}
        pagination={false}
        locale={{
          emptyText: (
            <EmptyState
              compact
              title={text("暂无下载记录", "No downloads recorded yet")}
              hint={text(
                "该窗口内没有来自此仓库的读取请求。",
                "No reads from this repository were recorded in this window.",
              )}
            />
          ),
        }}
      />
      <div>
        <Button onClick={() => void load()} loading={loading}>
          {text("刷新", "Refresh")}
        </Button>
      </div>
    </Space>
  );
}
