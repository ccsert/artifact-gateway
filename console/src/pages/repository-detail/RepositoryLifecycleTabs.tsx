import { useCallback, useEffect, useState } from "react";
import { SyncOutlined } from "@ant-design/icons";
import { Alert, Button, Popconfirm, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  reconcileRepositoryArtifactIntelligence,
  reconcileRepositoryArtifactScans,
  listRepositoryLifecycleJobs,
  listRepositoryTombstones,
  restoreRepositoryArtifact,
} from "../../client";
import type { ArtifactTombstone, LifecycleJob, Repository } from "../../client";
import { Badge, StateBadge } from "../../components/Badge";
import { LifecycleJobDetails } from "../../components/LifecycleJobDetails";
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
  const [expandedJobId, setExpandedJobId] = useState<string | null>(null);
  const [reconcileError, setReconcileError] = useState<unknown>(null);
  const [reconcileNotice, setReconcileNotice] = useState("");
  const [reconciling, setReconciling] = useState(false);
  const [scanReconciling, setScanReconciling] = useState(false);
  const [scanReconcileError, setScanReconcileError] = useState<unknown>(null);
  const [scanReconcileNotice, setScanReconcileNotice] = useState("");

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

  const failedIntelligenceJobs =
    jobs?.filter(
      (job) =>
        job.kind === "intelligence" &&
        (job.state === "failed" || job.state === "cancelled"),
    ) ?? [];

  const reconcileIntelligence = async () => {
    setReconciling(true);
    setReconcileError(null);
    setReconcileNotice("");
    const { data, error: err } = await reconcileRepositoryArtifactIntelligence({
      path: { repositoryId: repo.id },
      query: { limit: 100 },
    });
    setReconciling(false);
    if (err) {
      setReconcileError(err);
      return;
    }
    setReconcileNotice(
      text(
        `已重新排队 ${data?.requeued ?? 0} 个情报同步任务`,
        `Requeued ${data?.requeued ?? 0} intelligence sync job(s)`,
      ),
    );
    void load();
  };

  const reconcileScans = async () => {
    setScanReconciling(true);
    setScanReconcileError(null);
    setScanReconcileNotice("");
    const { data, error: err } = await reconcileRepositoryArtifactScans({
      path: { repositoryId: repo.id },
      query: { limit: 500 },
    });
    setScanReconciling(false);
    if (err) {
      setScanReconcileError(err);
      return;
    }
    setScanReconcileNotice(
      text(
        `已检查 ${data?.inspected ?? 0} 个制品，补入 ${data?.enqueued ?? 0} 个，重试 ${data?.retried ?? 0} 个`,
        `Inspected ${data?.inspected ?? 0} artifacts, queued ${data?.enqueued ?? 0}, retried ${data?.retried ?? 0}`,
      ),
    );
    void load();
  };

  const scanReconcileAction = (
    <Button
      size="small"
      type="primary"
      icon={<SyncOutlined />}
      loading={scanReconciling}
      onClick={() => void reconcileScans()}
    >
      {text("对账并补扫", "Reconcile scans")}
    </Button>
  );

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
        action={scanReconcileAction}
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
      render: (value: string) => <Badge tone="visualization-5">{value}</Badge>,
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
          className="block max-w-72 truncate text-xs text-[var(--ag-status-danger)]"
          title={value}
        >
          {value ?? "—"}
        </span>
      ),
    },
  ];

  return (
    <div className="space-y-3">
      {reconcileNotice && (
        <Alert type="success" showIcon title={reconcileNotice} />
      )}
      {reconcileError !== null && <ErrorBanner error={reconcileError} />}
      {scanReconcileNotice && (
        <Alert type="success" showIcon title={scanReconcileNotice} />
      )}
      {scanReconcileError !== null && (
        <ErrorBanner error={scanReconcileError} />
      )}
      <div className="flex items-center justify-between gap-4 border-b border-zinc-800/80 pb-3">
        <div className="text-xs text-zinc-500">
          {text(
            "对账可补入发布时漏掉的扫描任务，并重试失败任务。",
            "Reconciliation fills publication scan gaps and retries failed jobs.",
          )}
        </div>
        {scanReconcileAction}
      </div>
      {failedIntelligenceJobs.length > 0 && (
        <div className="flex items-center justify-between gap-4 rounded-md border border-[var(--ag-status-info-border)] bg-[var(--ag-status-info-soft)] px-4 py-3">
          <div className="text-xs text-[var(--ag-status-info)]">
            {text(
              `${failedIntelligenceJobs.length} 个情报同步任务需要补偿`,
              `${failedIntelligenceJobs.length} intelligence sync job(s) need reconciliation`,
            )}
          </div>
          <Popconfirm
            title={text(
              "重新排队失败的情报同步？",
              "Requeue failed intelligence syncs?",
            )}
            description={text(
              "只会重置失败或取消的情报同步任务，不会修改制品内容。",
              "Only failed or cancelled sync jobs are reset; artifact content is unchanged.",
            )}
            okText={text("重新排队", "Requeue")}
            cancelText={text("取消", "Cancel")}
            onConfirm={() => void reconcileIntelligence()}
          >
            <Button
              size="small"
              type="primary"
              icon={<SyncOutlined />}
              loading={reconciling}
            >
              {text("补偿情报同步", "Reconcile intelligence")}
            </Button>
          </Popconfirm>
        </div>
      )}
      <Table<LifecycleJob>
        className="ag-console-table"
        rowKey="id"
        size="middle"
        dataSource={jobs}
        columns={jobColumns}
        expandable={{
          expandedRowKeys: expandedJobId ? [expandedJobId] : [],
          onExpand: (expanded, job) =>
            setExpandedJobId(expanded ? job.id : null),
          rowExpandable: (job) => Boolean(job.details || job.lastError),
          expandedRowRender: (job) => (
            <div className="space-y-2 py-1">
              {job.details && <LifecycleJobDetails details={job.details} />}
              {job.lastError && (
                <div className="rounded-md border border-[var(--ag-status-danger-border)] bg-[var(--ag-status-danger-soft)] px-4 py-2 text-xs text-[var(--ag-status-danger)]">
                  {job.lastError}
                </div>
              )}
            </div>
          ),
        }}
        pagination={false}
        scroll={{ x: 1100 }}
      />
    </div>
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
