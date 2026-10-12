import { useState } from "react";
import { SyncOutlined } from "@ant-design/icons";
import { Button, Popconfirm } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  reconcileRepositoryArtifactIntelligence,
  reconcileRepositoryArtifactScans,
} from "../../client";
import type { LifecycleJob, Repository } from "../../client";
import { Badge, StateBadge } from "../../components/ui/Badge";
import { LifecycleJobDetails } from "../operations/LifecycleJobDetails";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
  isNotFound,
} from "../../components/ui/Feedback";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";

import { useLifecycleJobs } from "./repositoryQueries";

/** The repository's lifecycle jobs, polled every 10 seconds, with reconciles. */
export function RepositoryJobsTab({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  const jobsQuery = useLifecycleJobs(repo.id);
  const jobs = jobsQuery.data ?? null;
  const error = jobsQuery.error;
  const [expandedJobId, setExpandedJobId] = useState<string | null>(null);
  const [reconcileError, setReconcileError] = useState<unknown>(null);
  const [reconcileNotice, setReconcileNotice] = useState("");
  const [reconciling, setReconciling] = useState(false);
  const [scanReconciling, setScanReconciling] = useState(false);
  const [scanReconcileError, setScanReconcileError] = useState<unknown>(null);
  const [scanReconcileNotice, setScanReconcileNotice] = useState("");

  const load = () => jobsQuery.refetch();

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
        <span className="font-mono text-xs text-fg-tertiary" title={value}>
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
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
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
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
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
        <Notice
          tone="success"
          title={reconcileNotice}
          onClose={() => setReconcileNotice("")}
        />
      )}
      {reconcileError !== null && <ErrorBanner error={reconcileError} />}
      {scanReconcileNotice && (
        <Notice
          tone="success"
          title={scanReconcileNotice}
          onClose={() => setScanReconcileNotice("")}
        />
      )}
      {scanReconcileError !== null && (
        <ErrorBanner error={scanReconcileError} />
      )}
      <div className="flex items-center justify-between gap-4 border-b border-line pb-3">
        <div className="text-xs text-fg-tertiary">
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
      <ConsoleTable<LifecycleJob>
        rowKey="id"
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
