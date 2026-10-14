import { useMemo, useState } from "react";
import { SyncOutlined } from "@ant-design/icons";
import { Button } from "antd";
import type { ColumnsType } from "antd/es/table";
import { reconcileRepositoryArtifactScans } from "../../client";
import type { LifecycleJob, Repository } from "../../client";
import { StateBadge } from "../../components/ui/Badge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
} from "../../components/ui/Feedback";
import { Card, CardHeader } from "../../components/ui/Layout";
import { formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";

/** The repository's scan jobs, and a reconcile of missing scan results. */
export function ScanJobsCard({
  repo,
  jobs,
  loadError,
  canManage,
  canViewJobs,
  publicationScanning,
  onReload,
}: {
  repo: Repository;
  jobs: LifecycleJob[] | null;
  loadError: unknown;
  canManage: boolean;
  canViewJobs: boolean;
  /** New publications can enqueue scans, so a reconcile can backfill them. */
  publicationScanning: boolean;
  onReload: () => unknown;
}) {
  const { t } = usePreferences();
  const [reconcileError, setReconcileError] = useState<unknown>(null);
  const [reconcileNotice, setReconcileNotice] = useState("");
  const [reconciling, setReconciling] = useState(false);

  const reconcileScans = async () => {
    setReconciling(true);
    setReconcileError(null);
    setReconcileNotice("");
    const { data, error } = await reconcileRepositoryArtifactScans({
      path: { repositoryId: repo.id },
      query: { limit: 500 },
    });
    setReconciling(false);
    if (error) {
      setReconcileError(error);
      return;
    }
    setReconcileNotice(
      t("repository.inspectedArtifactsQueuedRetried", {
        inspected: data?.inspected ?? 0,
        enqueued: data?.enqueued ?? 0,
        retried: data?.retried ?? 0,
      }),
    );
    void onReload();
  };

  const columns = useMemo<ColumnsType<LifecycleJob>>(
    () => [
      {
        title: t("repository.artifact"),
        key: "artifact",
        width: 320,
        render: (_, job) => (
          <div className="space-y-0.5">
            <div
              className="truncate font-mono text-xs text-fg"
              title={job.details?.coordinate}
            >
              {job.details?.coordinate ?? t("repository.unknownArtifact")}
            </div>
            <div className="font-mono text-xs text-fg-disabled">
              {job.details ? shortDigest(job.details.digest) : job.id}
            </div>
            {job.details && (
              <div className="font-mono text-xs text-fg-disabled">{job.id}</div>
            )}
          </div>
        ),
      },
      {
        title: t("repository.status"),
        dataIndex: "state",
        key: "state",
        width: 130,
        render: (value: string) => <StateBadge state={value} />,
      },
      {
        title: t("repository.progress"),
        key: "progress",
        width: 170,
        render: (_, job) => (
          <span className="text-xs text-fg-tertiary">
            {job.progressTotal > 0
              ? `${job.progressCurrent} / ${job.progressTotal}`
              : job.progressMessage || "—"}
          </span>
        ),
      },
      {
        title: t("repository.created"),
        dataIndex: "createdAt",
        key: "createdAt",
        width: 190,
        render: (value: string) => (
          <span className="whitespace-nowrap text-xs text-fg-tertiary">
            {formatDate(value)}
          </span>
        ),
      },
      {
        title: t("repository.lastError"),
        dataIndex: "lastError",
        key: "lastError",
        render: (value?: string) => (
          <span className="text-xs text-[var(--ag-status-danger)]">
            {value ?? "—"}
          </span>
        ),
      },
    ],
    [t],
  );

  return (
    <>
      {reconcileNotice && (
        <Notice
          tone="success"
          title={reconcileNotice}
          onClose={() => setReconcileNotice("")}
        />
      )}
      {reconcileError !== null && <ErrorBanner error={reconcileError} />}

      <Card>
        <CardHeader
          title={t("repository.recentScanJobs")}
          extra={
            <Button
              size="small"
              aria-label={t("repository.reconcileHistoricalArtifacts")}
              icon={<SyncOutlined />}
              loading={reconciling}
              disabled={!publicationScanning || !canManage}
              onClick={() => void reconcileScans()}
            >
              {t("repository.reconcileHistoricalArtifacts")}
            </Button>
          }
        />
        {canViewJobs &&
          jobs !== null &&
          jobs.length > 0 &&
          loadError === null && (
            <p className="px-5 py-3 text-xs text-fg-tertiary">
              {t("repository.showsScansAmongTheLatest100LifecycleJobs")}
            </p>
          )}
        {!canViewJobs ? (
          <EmptyState
            compact
            title={t(
              "repository.recentJobsAreVisibleToRepositoryAdministrators",
            )}
            hint={t("repository.aSuccessfulSubmissionStillReturnsItsJobID")}
          />
        ) : loadError !== null ? (
          <div className="p-5">
            <ErrorBanner error={loadError} onRetry={() => void onReload()} />
          </div>
        ) : jobs === null ? (
          <Loading />
        ) : jobs.length === 0 ? (
          <EmptyState
            compact
            title={t("repository.noScansInRecentJobs")}
            hint={t("repository.checksTheLatest100LifecycleJobsOnlyJobs")}
          />
        ) : (
          <ConsoleTable<LifecycleJob>
            rowKey="id"
            dataSource={jobs}
            columns={columns}
            pagination={false}
            scroll={{ x: 900 }}
          />
        )}
      </Card>
    </>
  );
}
