import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { listNexusMigrationSchedules } from "../../client";
import { batchKeys } from "./nexusBatchQueries";
import { batchRequest } from "./nexusBatchModel";
import { migrationReadSnapshot } from "./migrationAccess";

export const scheduleKeys = {
  all: [...batchKeys.all, "schedules"] as const,
};

export function useSchedulePages() {
  const client = useQueryClient();
  return useInfiniteQuery({
    queryKey: scheduleKeys.all,
    initialPageParam: "",
    staleTime: 0,
    queryFn: async ({ signal, pageParam }) =>
      (
        await migrationReadSnapshot(client, () =>
          batchRequest(
            listNexusMigrationSchedules({
              query: { limit: 50, cursor: pageParam || undefined },
              signal,
            }),
          ),
        )
      ).value,
    getNextPageParam: (page, _pages, _last, cursors) =>
      page.nextCursor && !cursors.includes(page.nextCursor)
        ? page.nextCursor
        : undefined,
    refetchInterval: 5_000,
    refetchOnWindowFocus: "always",
  });
}
