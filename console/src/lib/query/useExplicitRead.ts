import { useQuery, type QueryKey } from "@tanstack/react-query";
import { useCallback, useRef } from "react";

/**
 * A read that runs only when the person asks for it (a "refresh the latest
 * version and edit again" button), through the shared query layer: it is
 * cancelled when its component unmounts, dropped with the signed-in
 * identity, and never retried.
 *
 * `run(apply)` reads and hands the answer to `apply` only if no later `run`
 * started meanwhile. Each run carries its own generation: two clicks before
 * the first answer share one request (a bare refetch does not cancel a first
 * read that has no data yet), and only the newer click applies it.
 */
export function useExplicitRead<T>(
  queryKey: QueryKey,
  read: (signal: AbortSignal) => Promise<T>,
  onError?: (error: unknown) => void,
) {
  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      try {
        return await read(signal);
      } catch (cause) {
        if (!signal.aborted) onError?.(cause);
        throw cause;
      }
    },
    enabled: false,
    gcTime: 0,
    retry: false,
    meta: { authFailure: "ignore" },
  });
  const generation = useRef(0);
  const { refetch } = query;
  const run = useCallback(
    async (apply: (data: T) => void) => {
      const id = ++generation.current;
      const result = await refetch();
      if (id === generation.current && result.isSuccess) apply(result.data);
    },
    [refetch],
  );
  return {
    busy: query.isFetching,
    // A new attempt hides the previous failure until it settles.
    error: query.isFetching ? undefined : (query.error ?? undefined),
    run,
  };
}
