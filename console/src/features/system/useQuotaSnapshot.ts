import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef } from "react";
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
  const queryKey = quotaKeys.snapshot(key);
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

  // Set by `update`, so a refresh it overtook does not count as a success.
  const overtaken = useRef(false);
  const { refetch } = query;
  const refresh = useCallback(async () => {
    overtaken.current = false;
    const result = await refetch();
    return !overtaken.current && result.isSuccess;
  }, [refetch]);

  // A page coming back into view reads again at once. Unlike the query's own
  // focus refetch, this cancels a read already in flight, so an answer that
  // was requested earlier can never land after the newer one.
  useEffect(() => {
    const visibility = () => {
      if (document.visibilityState !== "hidden") void refetch();
    };
    document.addEventListener("visibilitychange", visibility);
    return () => document.removeEventListener("visibilitychange", visibility);
  }, [refetch]);

  const update = (change: (old: T) => T) => {
    overtaken.current = true;
    void queryClient.cancelQueries({ queryKey });
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
