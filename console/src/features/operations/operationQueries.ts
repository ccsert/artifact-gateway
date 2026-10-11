import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import {
  cancelRepositoryLifecycleJob,
  listAuditRetentionJobs,
  listLifecycleJobs,
  retryRepositoryLifecycleJob,
  runRepositoryLifecycleJobNow,
  type ListAuditRetentionJobsResponse,
  type ListLifecycleJobsResponse,
} from "../../client";
import { unwrap } from "../../lib/query/client";
import { operationKeys } from "../../lib/query/keys";
import { usePreferences } from "../../lib/preferences";
import {
  ACTIVE_JOB_STATES,
  toOperationRows,
  type JobAction,
  type OperationRow,
} from "./operationModel";

const POLL_INTERVAL = 15_000;

/**
 * Lifecycle and audit-retention jobs. While `poll` is on and any job is still
 * active, the list is re-read every 15 seconds; jobs advance on the server
 * without anything in the Console invalidating them.
 */
export function useOperationJobs({ poll }: { poll: boolean }) {
  const { text } = usePreferences();
  // Selector failures are query errors, so the panel stays mounted and
  // its Retry can fetch a corrected answer instead of reusing bad data.
  const select = useCallback(
    (data: {
      lifecycle: ListLifecycleJobsResponse;
      auditRetention: ListAuditRetentionJobsResponse;
    }) => ({
      rows: toOperationRows(data.lifecycle, data.auditRetention, text),
      repositories: Array.from(
        new Map(
          data.lifecycle.map((record) => [
            record.repositoryId,
            { id: record.repositoryId, name: record.repositoryName },
          ]),
        ).values(),
      ).sort((a, b) => a.name.localeCompare(b.name)),
    }),
    [text],
  );
  const query = useQuery({
    queryKey: operationKeys.jobs(),
    queryFn: async ({ signal }) => {
      const [lifecycle, auditRetention] = await Promise.all([
        unwrap(listLifecycleJobs({ query: { limit: 500 }, signal })),
        unwrap(listAuditRetentionJobs({ signal })),
      ]);
      return { lifecycle, auditRetention };
    },
    select,
    staleTime: 0,
    refetchInterval: (current) => {
      const data = current.state.data;
      const active =
        data?.lifecycle.some((record) =>
          ACTIVE_JOB_STATES.includes(record.job.state),
        ) ||
        data?.auditRetention.some((job) =>
          ACTIVE_JOB_STATES.includes(job.state),
        );
      return poll && active ? POLL_INTERVAL : false;
    },
  });
  return {
    rows: query.data?.rows ?? null,
    repositories: query.data?.repositories ?? [],
    error: query.error,
    isFetching: query.isFetching,
    refetch: query.refetch,
  };
}

/** Run, retry or cancel one repository lifecycle job, then re-read the list. */
export function useControlJob() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ row, action }: { row: OperationRow; action: JobAction }) => {
      const options = {
        path: { repositoryId: row.repositoryId ?? "", lifecycleJobId: row.id },
      };
      return unwrap(
        action === "run"
          ? runRepositoryLifecycleJobNow(options)
          : action === "retry"
            ? retryRepositoryLifecycleJob(options)
            : cancelRepositoryLifecycleJob(options),
      );
    },
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: operationKeys.jobs() }),
  });
}
