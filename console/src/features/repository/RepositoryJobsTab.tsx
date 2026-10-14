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
  const { t } = usePreferences();
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
      t("repository.requeuedIntelligenceSyncJobS", {
        count: data?.requeued ?? 0,
      }),
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
      t("repository.inspectedArtifactsQueuedRetried", {
        inspected: data?.inspected ?? 0,
        enqueued: data?.enqueued ?? 0,
        retried: data?.retried ?? 0,
      }),
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
      {t("repository.reconcileScans")}
    </Button>
  );

  if (error !== null)
    return isNotFound(error) ? (
      <RepositoryFeatureUnavailable feature={t("repository.lifecycleJobs")} />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );

  if (!jobs) return <Loading />;
  if (jobs.length === 0)
    return (
      <EmptyState
        title={t("repository.noLifecycleJobs")}
        hint={t(
          "repository.retentionCleanupPromotionAndReplicationTasksAppearHere",
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
      title: t("repository.kind"),
      dataIndex: "kind",
      key: "kind",
      width: 150,
      render: (value: string) => <Badge tone="visualization-5">{value}</Badge>,
    },
    {
      title: t("repository.status"),
      dataIndex: "state",
      key: "state",
      width: 130,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: t("repository.created"),
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
      title: t("repository.completed"),
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
      title: t("repository.error"),
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
          {t(
            "repository.reconciliationFillsPublicationScanGapsAndRetriesFailed",
          )}
        </div>
        {scanReconcileAction}
      </div>
      {failedIntelligenceJobs.length > 0 && (
        <div className="flex items-center justify-between gap-4 rounded-md border border-[var(--ag-status-info-border)] bg-[var(--ag-status-info-soft)] px-4 py-3">
          <div className="text-xs text-[var(--ag-status-info)]">
            {t("repository.intelligenceSyncJobSNeedReconciliation", {
              length: failedIntelligenceJobs.length,
            })}
          </div>
          <Popconfirm
            title={t("repository.requeueFailedIntelligenceSyncs")}
            description={t("repository.onlyFailedOrCancelledSyncJobsAreReset")}
            okText={t("repository.requeue")}
            cancelText={t("repository.cancel")}
            onConfirm={() => void reconcileIntelligence()}
          >
            <Button
              size="small"
              type="primary"
              icon={<SyncOutlined />}
              loading={reconciling}
            >
              {t("repository.reconcileIntelligence")}
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
