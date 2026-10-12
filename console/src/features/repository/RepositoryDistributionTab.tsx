import { useState } from "react";
import {
  deleteRepositoryReplication,
  getRepositoryReplication,
} from "../../client";
import type {
  AptLifecyclePackage,
  ReplicationPlan,
  ReplicationPlanDetail,
  Repository,
} from "../../client";
import { StateBadge } from "../../components/ui/Badge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
} from "../../components/ui/Feedback";
import { Modal } from "../../components/ui/Modal";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { formatDescriptor } from "../../formats";

import { DistributionRequestForm } from "./DistributionRequestForm";
import {
  replicationCheckpointColumns,
  replicationPlanColumns,
} from "./distributionColumns";
import { useAccessRepositories } from "../access-control/accessQueries";
import { useReplicationPlans } from "./repositoryQueries";

/** Promotion and replication: the request form and this repository's plans. */
export function RepositoryDistributionTab({
  repo,
  aptPackages,
  disabled = false,
}: {
  repo: Repository;
  aptPackages?: AptLifecyclePackage[];
  disabled?: boolean;
}) {
  const { text } = usePreferences();
  // APT promotes and replicates into a named distribution suite.
  const suiteTarget = Boolean(formatDescriptor(repo.format)?.distributionSuite);
  // The same catalog read Access Control uses to name repositories.
  const reposQuery = useAccessRepositories();
  const plansQuery = useReplicationPlans(repo.id);
  const repos = reposQuery.data?.items ?? [];
  const plans = plansQuery.data ?? null;
  const error = reposQuery.error ?? plansQuery.error;
  const [detail, setDetail] = useState<ReplicationPlanDetail | null>(null);
  const [actionError, setActionError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const load = () => Promise.all([reposQuery.refetch(), plansQuery.refetch()]);

  const cancelPlan = async (planId: string) => {
    setActionError(null);
    try {
      const { error: err } = await deleteRepositoryReplication({
        path: { repositoryId: repo.id, replicationPlanId: planId },
      });
      if (err) {
        setActionError(err);
        return;
      }
      setNotice(
        text(
          "已取消复制计划，工作进程不再重试。",
          "Replication plan canceled. Workers will not retry it.",
        ),
      );
      void plansQuery.refetch();
    } catch (err) {
      setActionError(err);
    }
  };

  const showDetail = async (planId: string) => {
    try {
      const { data, error: err } = await getRepositoryReplication({
        path: { repositoryId: repo.id, replicationPlanId: planId },
      });
      if (err) {
        setActionError(err);
        return;
      }
      setDetail(data ?? null);
    } catch (err) {
      setActionError(err);
    }
  };

  const repoName = (id: string) =>
    repos.find((r) => r.id === id)?.name ?? id.slice(0, 8) + "…";
  if (error !== null && !plans)
    return <ErrorBanner error={error} onRetry={() => void load()} />;

  const planColumns = replicationPlanColumns({
    suiteTarget,
    repoName,
    cancelPlan,
    showDetail,
    text,
  });
  const checkpointColumns = replicationCheckpointColumns(text);

  return (
    <div className="ag-page-stack ag-distribution">
      {error !== null && (
        <ErrorBanner error={error} onRetry={() => void load()} />
      )}
      {actionError !== null && <ErrorBanner error={actionError} />}
      {notice && (
        <Notice tone="success" title={notice} onClose={() => setNotice("")} />
      )}

      <DistributionRequestForm
        repo={repo}
        repos={repos}
        aptPackages={aptPackages}
        disabled={disabled}
        setActionError={setActionError}
        setNotice={setNotice}
        onSubmitted={() => plansQuery.refetch()}
      />

      {/* 复制计划列表 */}
      <div>
        <div className="mb-2 text-sm font-medium text-fg">
          {text(
            `复制计划（${plans?.length ?? 0}）`,
            `Replication plans (${plans?.length ?? 0})`,
          )}
        </div>
        {!plans ? (
          <Loading />
        ) : plans.length === 0 ? (
          <EmptyState title={text("暂无复制计划", "No replication plans")} />
        ) : (
          <ConsoleTable<ReplicationPlan>
            rowKey="id"
            dataSource={plans}
            columns={planColumns}
            pagination={false}
            scroll={{ x: 1040 }}
          />
        )}
      </div>

      {/* 复制进度详情 */}
      <Modal
        open={!!detail}
        title={text("复制进度详情", "Replication progress")}
        onClose={() => setDetail(null)}
        wide
      >
        {detail && (
          <div className="space-y-4">
            <div className="flex flex-wrap gap-4 text-xs text-fg-secondary">
              <span>
                {text("状态：", "Status: ")}
                <StateBadge state={detail.state} />
              </span>
              <span>
                {text("目标：", "Target: ")}
                {repoName(detail.targetRepositoryId)}
              </span>
              <span>
                {text("创建：", "Created: ")}
                {formatDate(detail.createdAt)}
              </span>
              {detail.lastError && (
                <span className="text-[var(--ag-status-danger)]">
                  {detail.lastError}
                </span>
              )}
            </div>
            {detail.checkpoints.length === 0 ? (
              <p className="py-4 text-center text-sm text-fg-tertiary">
                {text("暂无检查点", "No checkpoints")}
              </p>
            ) : (
              <ConsoleTable<ReplicationPlanDetail["checkpoints"][number]>
                rowKey={(checkpoint, index) =>
                  `${checkpoint.objectKey}-${index}`
                }
                size="small"
                dataSource={detail.checkpoints}
                columns={checkpointColumns}
                pagination={false}
                scroll={{ x: 900 }}
              />
            )}
          </div>
        )}
      </Modal>
    </div>
  );
}
