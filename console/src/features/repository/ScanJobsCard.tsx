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
  const { text } = usePreferences();
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
      text(
        `已检查 ${data?.inspected ?? 0} 个制品，补入 ${data?.enqueued ?? 0} 个，重试 ${data?.retried ?? 0} 个`,
        `Inspected ${data?.inspected ?? 0} artifacts, queued ${data?.enqueued ?? 0}, retried ${data?.retried ?? 0}`,
      ),
    );
    void onReload();
  };

  const columns = useMemo<ColumnsType<LifecycleJob>>(
    () => [
      {
        title: text("制品", "Artifact"),
        key: "artifact",
        width: 320,
        render: (_, job) => (
          <div className="space-y-0.5">
            <div
              className="truncate font-mono text-xs text-fg"
              title={job.details?.coordinate}
            >
              {job.details?.coordinate ?? text("未知制品", "Unknown artifact")}
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
        title: text("状态", "Status"),
        dataIndex: "state",
        key: "state",
        width: 130,
        render: (value: string) => <StateBadge state={value} />,
      },
      {
        title: text("进度", "Progress"),
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
        title: text("创建时间", "Created"),
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
        title: text("最近错误", "Last error"),
        dataIndex: "lastError",
        key: "lastError",
        render: (value?: string) => (
          <span className="text-xs text-[var(--ag-status-danger)]">
            {value ?? "—"}
          </span>
        ),
      },
    ],
    [text],
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
          title={text("最近扫描任务", "Recent scan jobs")}
          extra={
            <Button
              size="small"
              aria-label={text(
                "对账历史制品",
                "Reconcile historical artifacts",
              )}
              icon={<SyncOutlined />}
              loading={reconciling}
              disabled={!publicationScanning || !canManage}
              onClick={() => void reconcileScans()}
            >
              {text("对账历史制品", "Reconcile historical artifacts")}
            </Button>
          }
        />
        {canViewJobs &&
          jobs !== null &&
          jobs.length > 0 &&
          loadError === null && (
            <p className="px-5 py-3 text-xs text-fg-tertiary">
              {text(
                "仅显示最近 100 条生命周期任务中的扫描任务；不是全部扫描历史。",
                "Shows scans among the latest 100 lifecycle jobs; this is not the complete scan history.",
              )}
            </p>
          )}
        {!canViewJobs ? (
          <EmptyState
            compact
            title={text(
              "最近任务仅对仓库管理员可见",
              "Recent jobs are visible to repository administrators",
            )}
            hint={text(
              "扫描提交成功后仍会在上方返回本次任务 ID。",
              "A successful submission still returns its job ID above.",
            )}
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
            title={text("最近任务中暂无扫描", "No scans in recent jobs")}
            hint={text(
              "仅检查最近 100 条生命周期任务。手动提交或对账历史制品后，任务会显示在这里。",
              "Checks the latest 100 lifecycle jobs only. Jobs appear here after a manual submission or historical reconciliation.",
            )}
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
