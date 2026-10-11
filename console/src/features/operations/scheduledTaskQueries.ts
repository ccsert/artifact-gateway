import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import {
  listRepositories,
  listScheduledTaskRuns,
  listScheduledTasks,
} from "../../client";
import type { Repository } from "../../client";
import { unwrap } from "../../lib/query/client";
import { operationKeys } from "../../lib/query/keys";
import { RETENTION_FORMATS } from "./scheduledTaskModel";

/** Schedules advance on the server (next and last run), so always re-read. */
export function useScheduledTasks() {
  return useQuery({
    queryKey: operationKeys.scheduledTasks(),
    queryFn: ({ signal }) => unwrap(listScheduledTasks({ signal })),
    staleTime: 0,
  });
}

/**
 * Repositories a retention schedule can target: active hosted repositories
 * of a format with retention support, across every catalog page.
 */
export function useRetentionRepositories() {
  return useQuery({
    queryKey: operationKeys.retentionRepositories(),
    queryFn: async ({ signal }) => {
      const items: Repository[] = [];
      let pageToken: string | undefined;
      do {
        const page = await unwrap(
          listRepositories({ query: { pageSize: 200, pageToken }, signal }),
        );
        items.push(...(page?.items ?? []));
        pageToken = page?.nextPageToken;
      } while (pageToken);
      return items.filter(
        (repo) =>
          repo.state === "active" &&
          (repo.type ?? "hosted") === "hosted" &&
          RETENTION_FORMATS.has(repo.format),
      );
    },
    staleTime: 0,
  });
}

/** One task's dispatch history, read while its row is expanded. */
export function useTaskRuns(taskId: string) {
  return useQuery({
    queryKey: operationKeys.taskRuns(taskId),
    queryFn: ({ signal }) =>
      unwrap(
        listScheduledTaskRuns({
          path: { taskId },
          query: { limit: 100 },
          signal,
        }),
      ),
    staleTime: 0,
  });
}

/**
 * Re-read the schedules after a write. Passing a task also re-reads its
 * dispatch history, which a manual run appends to.
 */
export function useInvalidateScheduledTasks() {
  const queryClient = useQueryClient();
  return useCallback(
    async (taskId?: string) => {
      await queryClient.invalidateQueries({
        queryKey: taskId
          ? operationKeys.taskRuns(taskId)
          : operationKeys.scheduledTasks(),
        exact: !taskId,
      });
      if (taskId) {
        await queryClient.invalidateQueries({
          queryKey: operationKeys.scheduledTasks(),
          exact: true,
        });
      }
    },
    [queryClient],
  );
}
