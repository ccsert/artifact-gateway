import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Popconfirm, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  listRepositoryLifecycleJobs,
  listRepositoryTombstones,
  restoreRepositoryArtifact,
} from "../../client";
import type { ArtifactTombstone, LifecycleJob, Repository } from "../../client";
import { Badge, StateBadge } from "../../components/Badge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  isNotFound,
} from "../../components/Feedback";
import { Pagination } from "../../components/Layout";
import { formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";

export function RepositoryJobsTab({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  const [jobs, setJobs] = useState<LifecycleJob[] | null>(null);
  const [error, setError] = useState<unknown>(null);

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await listRepositoryLifecycleJobs({
      path: { repositoryId: repo.id },
    });
    if (err) {
      setError(err);
      return;
    }
    setJobs(data ?? []);
  }, [repo.id]);

  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), 10000);
    return () => clearInterval(timer);
  }, [load]);

  if (error !== null)
    return isNotFound(error) ? (
      <RepositoryFeatureUnavailable
        feature={text("生命周期任务", "Lifecycle jobs")}
      />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );

  if (!jobs) return <Loading />;
  if (jobs.length === 0)
    return (
      <EmptyState
        title={text("暂无生命周期任务", "No lifecycle jobs")}
        hint={text(
          "保留清理、晋升、复制任务会显示在这里",
          "Retention cleanup, promotion, and replication tasks appear here.",
        )}
      />
    );

  const jobColumns: ColumnsType<LifecycleJob> = [
    {
      title: "ID",
      dataIndex: "id",
      key: "id",
      width: 150,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-500" title={value}>
          {value.slice(0, 8)}…
        </span>
      ),
    },
    {
      title: text("类型", "Kind"),
      dataIndex: "kind",
      key: "kind",
      width: 150,
      render: (value: string) => <Badge tone="blue">{value}</Badge>,
    },
    {
      title: text("状态", "Status"),
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: text("创建时间", "Created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: text("完成时间", "Completed"),
      dataIndex: "completedAt",
      key: "completedAt",
      width: 180,
      render: (value?: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: text("错误", "Error"),
      dataIndex: "lastError",
      key: "lastError",
      width: 300,
      render: (value?: string) => (
        <span
          className="block max-w-72 truncate text-xs text-rose-400"
          title={value}
        >
          {value ?? "—"}
        </span>
      ),
    },
  ];

  return (
    <Table<LifecycleJob>
      className="ag-console-table"
      rowKey="id"
      size="middle"
      dataSource={jobs}
      columns={jobColumns}
      pagination={false}
      scroll={{ x: 1100 }}
    />
  );
}

export function RepositoryTombstonesTab({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  const [items, setItems] = useState<ArtifactTombstone[]>([]);
  const [nextToken, setNextToken] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [restoreError, setRestoreError] = useState<unknown>(null);
  const [restoreNotice, setRestoreNotice] = useState("");
  const [restoring, setRestoring] = useState<string | null>(null);

  const load = useCallback(
    async (pageToken?: string) => {
      if (!pageToken) {
        setLoading(true);
        setError(null);
      }
      const { data, error: err } = await listRepositoryTombstones({
        path: { repositoryId: repo.id },
        query: { pageSize: 50, pageToken },
      });
      setLoading(false);
      if (err) {
        setError(err);
        return;
      }
      setItems((prev) =>
        pageToken ? [...prev, ...(data?.items ?? [])] : (data?.items ?? []),
      );
      setNextToken(data?.nextPageToken);
    },
    [repo.id],
  );

  useEffect(() => {
    void load();
  }, [load]);

  const restore = async (coordinate: string) => {
    setRestoring(coordinate);
    setRestoreError(null);
    setRestoreNotice("");
    const { error: err } = await restoreRepositoryArtifact({
      path: { repositoryId: repo.id },
      body: { coordinate },
    });
    setRestoring(null);
    if (err) {
      setRestoreError(err);
      return;
    }
    setRestoreNotice(text(`已恢复 ${coordinate}`, `Restored ${coordinate}`));
    void load();
  };

  if (error !== null)
    return isNotFound(error) ? (
      <RepositoryFeatureUnavailable
        feature={text("墓碑管理", "Tombstone management")}
      />
    ) : (
      <ErrorBanner error={error} onRetry={() => load()} />
    );
  if (loading) return <Loading />;

  const tombstoneColumns: ColumnsType<ArtifactTombstone> = [
    {
      title: text("坐标", "Coordinate"),
      dataIndex: "coordinate",
      key: "coordinate",
      width: 360,
      render: (value: string) => (
        <span
          className="block max-w-md truncate font-mono text-xs text-zinc-200"
          title={value}
        >
          {value}
        </span>
      ),
    },
    {
      title: text("摘要", "Digest"),
      dataIndex: "digest",
      key: "digest",
      width: 180,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-500">
          {shortDigest(value)}
        </span>
      ),
    },
    {
      title: text("删除时间", "Deleted"),
      dataIndex: "tombstonedAt",
      key: "tombstonedAt",
      width: 190,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 100,
      render: (_, item) => (
        <Popconfirm
          title={text("确认恢复此制品？", "Restore this artifact?")}
          description={text(
            "恢复后制品会重新出现在仓库浏览与协议读取中。",
            "After restoration, the artifact is available again in repository browsing and protocol reads.",
          )}
          okText={text("恢复", "Restore")}
          cancelText={text("取消", "Cancel")}
          onConfirm={() => restore(item.coordinate)}
        >
          <Button size="small" loading={restoring === item.coordinate}>
            {text("恢复", "Restore")}
          </Button>
        </Popconfirm>
      ),
    },
  ];

  return (
    <div className="space-y-3">
      {restoreError !== null && (
        <div className="mb-3">
          <ErrorBanner error={restoreError} />
        </div>
      )}
      {restoreNotice && <Alert type="success" showIcon title={restoreNotice} />}
      {items.length === 0 ? (
        <EmptyState
          title={text("暂无墓碑", "No tombstones")}
          hint={text(
            "被删除的制品会保留墓碑记录，可在此恢复",
            "Deleted artifacts retain tombstone records and can be restored here.",
          )}
        />
      ) : (
        <>
          <Table<ArtifactTombstone>
            className="ag-console-table"
            rowKey={(item) =>
              `${item.coordinate}:${item.digest}:${item.tombstonedAt}`
            }
            size="middle"
            dataSource={items}
            columns={tombstoneColumns}
            pagination={false}
            scroll={{ x: 830 }}
          />
          <Pagination hasMore={!!nextToken} onMore={() => load(nextToken)} />
        </>
      )}
    </div>
  );
}
