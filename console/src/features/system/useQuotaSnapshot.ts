import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef } from "react";
import { quotaKeys } from "../../lib/query/keys";
import { isQuotaAuthError } from "./quotaAlertPresentation";

/**
 * A quota or email snapshot read through the shared query layer.
 *
 * The rules these panels rely on, and how the query keeps them:
 *
 * - A failed refresh retains the last data (a query keeps its data on error).
 * - A password-change or permission denial calls `onAuthBlocked`, and the
 *   panel blocks its workspace until an explicit `refresh` succeeds; the
 *   denial never ends the session (`authFailure: "ignore"`).
 * - An older read never lands after a newer write: `update` cancels the read
 *   in flight before it writes, and a `refresh` overtaken that way reports
 *   failure, so it cannot lift a block.
 * - Each `key` starts empty (`gcTime: 0` drops a key nobody watches), is read
 *   again every 30 seconds while the page is visible (`refetchInterval` pauses
 *   while hidden) and at once on return to it, and is not retried: the next
 *   poll is the retry.
 */
export function useQuotaSnapshot<T>(
  read: (signal: AbortSignal) => Promise<T>,
  key: string,
  onAuthBlocked?: (error: unknown) => void,
) {
  const queryClient = useQueryClient();
  const queryKey = useMemo(() => quotaKeys.snapshot(key), [key]);
  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      try {
        return await read(signal);
      } catch (cause) {
        if (!signal.aborted && isQuotaAuthError(cause)) onAuthBlocked?.(cause);
        throw cause;
      }
    },
    meta: { authFailure: "ignore" },
    staleTime: 0,
    gcTime: 0,
    retry: false,
    refetchInterval: 30_000,
    // Returning to the page re-reads below, replacing a read in flight.
    refetchOnWindowFocus: false,
  });

  // A completion belongs to both this cached query and its request generation.
  const scope = queryClient.getQueryCache().find({ queryKey, exact: true });
  const generation = useRef(0);
  useEffect(
    () => () => {
      ++generation.current;
    },
    [scope],
  );
  const { refetch } = query;
  const refresh = useCallback(async () => {
    const id = ++generation.current;
    const isCurrent = () =>
      id === generation.current &&
      queryClient.getQueryCache().find({ queryKey, exact: true }) === scope;
    // refetch alone reuses an initial read when there is no cached data yet.
    await queryClient.cancelQueries({ queryKey, exact: true });
    if (!isCurrent()) return false;
    const result = await refetch();
    return isCurrent() && result.isSuccess;
  }, [queryClient, queryKey, refetch, scope]);

  // A page coming back into view reads again at once. Unlike the query's own
  // focus refetch, this cancels a read already in flight, so an answer that
  // was requested earlier can never land after the newer one.
  useEffect(() => {
    const visibility = () => {
      if (document.visibilityState !== "hidden") void refresh();
    };
    document.addEventListener("visibilitychange", visibility);
    return () => document.removeEventListener("visibilitychange", visibility);
  }, [refresh]);

  const update = (change: (old: T) => T) => {
    if (queryClient.getQueryCache().find({ queryKey, exact: true }) !== scope)
      return;
    ++generation.current;
    void queryClient.cancelQueries({ queryKey, exact: true });
    const updatedAt = queryClient.getQueryState(queryKey)?.dataUpdatedAt;
    queryClient.setQueryData<T>(
      queryKey,
      (old) => (old === undefined ? old : change(old)),
      // A local write is not a read: keep the time the data was last read.
      { updatedAt },
    );
  };

  return {
    data: query.data,
    error: query.error ?? undefined,
    loading: query.isFetching,
    readAt: query.dataUpdatedAt
      ? new Date(query.dataUpdatedAt).toISOString()
      : undefined,
    refresh,
    update,
  };
}

export function quotaResult<T>(result: { data?: T; error?: unknown }): T {
  if (result.error || result.data === undefined)
    throw result.error ?? new Error("unavailable");
  return result.data;
}
