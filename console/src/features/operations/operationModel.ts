import type { MessageKey } from "../../messages";
import type { Translate } from "../../lib/preferences";
import type {
  LifecycleJob,
  ListAuditRetentionJobsResponse,
  ListLifecycleJobsResponse,
} from "../../client";

export type OperationRow = {
  id: string;
  kind: string;
  state: string;
  repository?: string;
  repositoryId?: string;
  createdAt: string;
  startedAt?: string;
  completedAt?: string;
  nextAttemptAt?: string;
  attempts?: number;
  maxAttempts?: number;
  progressCurrent?: number;
  progressTotal?: number;
  progressMessage?: string;
  lastError?: string;
  details?: LifecycleJob["details"];
};
export type JobAction = "run" | "retry" | "cancel";

export function operationRowKey(row: OperationRow) {
  return `${row.kind}-${row.id}`;
}

export const KIND_LABELS: Record<string, MessageKey> = {
  retention: "operations.artifactRetention",
  promotion: "operations.artifactPromotion",
  replication: "operations.repositoryReplication",
  reclaim: "operations.objectReclamation",
  intelligence: "operations.intelligenceSync",
  "audit-retention": "operations.auditRetention",
};

/** Active jobs keep the history polling; settled ones do not. */
export const ACTIVE_JOB_STATES = ["pending", "running", "retrying"];

/** Lifecycle and audit-retention jobs as one list, newest first. */
export function toOperationRows(
  lifecycleRecords: ListLifecycleJobsResponse,
  auditRetention: ListAuditRetentionJobsResponse,
  t: Translate,
): OperationRow[] {
  const lifecycle: OperationRow[] = lifecycleRecords.map((record) => {
    const job: LifecycleJob = record.job;
    const isHistoricalJob =
      job.state === "completed" && job.attempts === 0 && !job.progressTotal;
    return {
      id: job.id,
      kind: job.kind,
      state: job.state,
      repository: record.repositoryName,
      repositoryId: record.repositoryId,
      createdAt: job.createdAt,
      startedAt: job.startedAt,
      completedAt: job.completedAt,
      nextAttemptAt: job.nextAttemptAt,
      attempts: isHistoricalJob ? undefined : job.attempts,
      maxAttempts: isHistoricalJob ? undefined : job.maxAttempts,
      progressCurrent: job.progressCurrent,
      progressTotal: job.progressTotal,
      progressMessage: isHistoricalJob
        ? t("operations.historicalJob")
        : job.progressMessage,
      lastError: job.lastError,
      details: job.details,
    };
  });
  const auditRows: OperationRow[] = auditRetention.map((job) => ({
    id: job.id,
    kind: "audit-retention",
    state: job.state,
    createdAt: job.createdAt,
    startedAt: job.startedAt,
    completedAt: job.completedAt,
    lastError: job.lastError,
  }));
  return [...lifecycle, ...auditRows].sort((a, b) =>
    b.createdAt.localeCompare(a.createdAt),
  );
}
