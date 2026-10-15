import { useState } from "react";
import { deleteRepositoryReplication } from "../../client";
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
import { useReplicationPlan, useReplicationPlans } from "./repositoryQueries";

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
  const { t } = usePreferences();
  // APT promotes and replicates into a named distribution suite.
  const suiteTarget = Boolean(formatDescriptor(repo.format)?.distributionSuite);
  // The same catalog read Access Control uses to name repositories.
  const reposQuery = useAccessRepositories();
  const plansQuery = useReplicationPlans(repo.id);
  const repos = reposQuery.data?.items ?? [];
  const plans = plansQuery.data ?? null;
  const error = reposQuery.error ?? plansQuery.error;
  // The open plan's detail is a query: reopening shows the last answer while
  // it is read again, and a cancelled read never lands in a newer detail.
  const [detailPlanId, setDetailPlanId] = useState<string | null>(null);
  const detailQuery = useReplicationPlan(repo.id, detailPlanId);
  const detail: ReplicationPlanDetail | null = detailPlanId
    ? (detailQuery.data ?? null)
    : null;
  const detailError =
    detailPlanId && !detailQuery.isFetching ? detailQuery.error : null;
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
      setNotice(t("repository.replicationPlanCanceledWorkersWillNotRetryIt"));
      void plansQuery.refetch();
    } catch (err) {
      setActionError(err);
    }
  };

  const showDetail = (planId: string) => {
    if (planId === detailPlanId) void detailQuery.refetch();
    else setDetailPlanId(planId);
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
    t,
  });
  const checkpointColumns = replicationCheckpointColumns(t);

  return (
    <div className="ag-page-stack ag-distribution">
      {error !== null && (
        <ErrorBanner error={error} onRetry={() => void load()} />
      )}
      {actionError !== null && <ErrorBanner error={actionError} />}
      {detailError && (
        <ErrorBanner
          error={detailError}
          onRetry={() => void detailQuery.refetch()}
        />
      )}
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
          {t("repository.replicationPlans", { count: plans?.length ?? 0 })}
        </div>
        {!plans ? (
          <Loading />
        ) : plans.length === 0 ? (
          <EmptyState title={t("repository.noReplicationPlans")} />
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
        title={t("repository.replicationProgress")}
        onClose={() => setDetailPlanId(null)}
        wide
      >
        {detail && (
          <div className="space-y-4">
            <div className="flex flex-wrap gap-4 text-xs text-fg-secondary">
              <span>
                {t("repository.status2")}
                <StateBadge state={detail.state} />
              </span>
              <span>
                {t("repository.target")}
                {repoName(detail.targetRepositoryId)}
              </span>
              <span>
                {t("repository.created2")}
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
                {t("repository.noCheckpoints")}
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
