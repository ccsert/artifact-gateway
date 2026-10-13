import { useQuery, useQueryClient } from "@tanstack/react-query";

/**
 * One overview source with its own loading state and Retry. A newer read
 * always owns the answer: Retry cancels a read still in flight and asks
 * again, and a refresh failure keeps the last answer on screen.
 */
export function useDashboardResource<T>(
  queryKey: readonly unknown[],
  read: (signal: AbortSignal) => Promise<T>,
) {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => read(signal),
    // The overview is a live summary; every visit reads it again.
    staleTime: 0,
  });
  // A refetch restarts a read that already has an answer, but joins a first
  // read still in flight; cancel that one so Retry really asks again.
  const reload = async () => {
    if (query.isFetching && query.data === undefined)
      await queryClient.cancelQueries({ queryKey, exact: true });
    await query.refetch();
  };
  return {
    data: query.data,
    // A retry in progress clears the previous error, as each source's
    // banner is replaced by its loading state until the new answer.
    error: query.isFetching ? undefined : (query.error ?? undefined),
    loading: query.isFetching,
    reload,
  };
}
