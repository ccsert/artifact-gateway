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

export type Localize = (chinese: string, english: string) => string;
export type JobAction = "run" | "retry" | "cancel";

export function operationRowKey(row: OperationRow) {
  return `${row.kind}-${row.id}`;
}

export const KIND_LABELS: Record<string, [string, string]> = {
  retention: ["制品保留", "Artifact retention"],
  promotion: ["制品晋级", "Artifact promotion"],
  replication: ["仓库复制", "Repository replication"],
  reclaim: ["对象回收", "Object reclamation"],
  intelligence: ["情报同步", "Intelligence sync"],
  "audit-retention": ["审计保留", "Audit retention"],
};

/** Active jobs keep the history polling; settled ones do not. */
export const ACTIVE_JOB_STATES = ["pending", "running", "retrying"];

/** Lifecycle and audit-retention jobs as one list, newest first. */
export function toOperationRows(
  lifecycleRecords: ListLifecycleJobsResponse,
  auditRetention: ListAuditRetentionJobsResponse,
  text: Localize,
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
        ? text("历史任务", "Historical job")
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
